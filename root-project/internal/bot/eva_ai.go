package bot

// EVAChat Flow — AI compose layer (hybrid mode).
//
// The TXT engine (eva_chat.go) still decides WHAT happens: stage, category,
// snap ask → yes → share chain, age gate, reply cap, no-repeat rules. When an
// OpenAI-compatible API is configured, this layer rewrites the reply TEXT so it
// answers what the stranger actually said, using the per-user transcript as
// memory and a per-user random persona (age / country). Any failure falls
// back to the TXT line, so the bot never goes silent. Lines that carry the
// snap username (share_snap.txt / snapchat.txt) are never rewritten.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"chitchatbot/internal/model"
)

const (
	evaAIDefaultBaseURL = "https://api.openai.com/v1"
	evaAIDefaultModel   = "gpt-4o-mini"
	evaAITimeout        = 25 * time.Second
	evaAIMaxTokens      = 60
	evaAITemperature    = 0.9
	evaAIPersonaAgeMin  = 20
	evaAIPersonaAgeMax  = 25
	evaAIMaxReplyRunes  = 160
	evaAIHistoryLines   = 24
	evaAIStyleExamples  = 4
)

// EVAAIConfig is the loaded AI compose configuration (shared by workers).
type EVAAIConfig struct {
	BaseURL     string
	APIKey      string
	Model       string
	Temperature float64
	Timeout     time.Duration
	Persona     string   // persona.txt body (comments stripped)
	Countries   []string // persona_countries.txt
	HTTPClient  *http.Client
}

// evaAIIntents map an output pool to what the composed line must do.
var evaAIIntents = map[string]string{
	poolGreeting:                 "greet the stranger back casually and briefly, like a first hi.",
	poolAgeGender:                "tell your gender and age in chat style (like 'f 22') and ask theirs if they did not say it. use your persona age.",
	poolAgeAsk:                   "say your gender and age briefly (like 'f 22') and ask their age / gender.",
	poolCountry:                  "say which country you are from (your persona country) and ask where they are from.",
	poolFlirtyQuestions:          "react to what they just said, then ask one short playful / flirty question to keep the chat going.",
	"how_are_you.txt":            "answer how you are (bored / chilling) and ask them back.",
	"your_age.txt":               "tell your age (persona age) casually.",
	poolEnglishOnly:              "say you only speak english, casually.",
	poolHorny:                    "respond to the horny / explicit message in a teasing, playful, non-explicit way. keep it short, do not escalate.",
	poolHornyAnswer:              "answer the horny question with a short playful yes-ish tease (no explicit content).",
	poolFlirtyReply:              "react flirty and playful to their compliment / flirty line.",
	"middle_chat/warm_reply.txt": "give a short warm reaction to what they said (one or two words is fine).",
	poolBusyLater:                "tell them no worries, text you later, casually.",
	poolAskSnap:                  "ask if they have snapchat / want to move to snap. do NOT mention any username. do not ask for theirs, just ask if they have it.",
	poolRetryNudge:               "playfully nudge them to add you on snap anyway. do NOT mention any username.",
	poolCloser:                   "say bye casually and tell them to hit you up on snap. do NOT mention any username.",
}

var evaAIForbidden = regexp.MustCompile(`(?i)(@|https?://|www\.|\.com|\.gg|telegram|t\.me|whatsapp|as an ai|language model|i am a bot|i'm a bot|i am an ai)`)

// LoadEVAAI builds the AI config from the saved message settings and the
// persona files in the EVAChat data folder. Returns nil when AI is disabled.
func LoadEVAAI(dataRoot string, settings model.MessageSettings) (*EVAAIConfig, error) {
	if !settings.AIEnabled {
		return nil, nil
	}
	key := strings.TrimSpace(settings.AIAPIKey)
	if key == "" {
		return nil, fmt.Errorf("EVAChat AI is enabled but the API key is empty")
	}
	base := strings.TrimRight(strings.TrimSpace(settings.AIBaseURL), "/")
	if base == "" {
		base = evaAIDefaultBaseURL
	}
	modelName := strings.TrimSpace(settings.AIModel)
	if modelName == "" {
		modelName = evaAIDefaultModel
	}
	persona, err := loadEVAPersona(filepath.Join(dataRoot, "persona.txt"))
	if err != nil {
		return nil, err
	}
	countries, err := loadEVALines(filepath.Join(dataRoot, "persona_countries.txt"), true)
	if err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("read persona_countries.txt: %w", err)
	}
	if len(countries) == 0 {
		countries = []string{"uk", "germany", "france", "italy", "spain", "netherlands", "sweden", "poland"}
	}
	return &EVAAIConfig{
		BaseURL:     base,
		APIKey:      key,
		Model:       modelName,
		Temperature: evaAITemperature,
		Timeout:     evaAITimeout,
		Persona:     persona,
		Countries:   countries,
		HTTPClient:  &http.Client{Timeout: evaAITimeout},
	}, nil
}

func loadEVAPersona(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", fmt.Errorf("read persona.txt: %w", err)
	}
	var lines []string
	for _, line := range strings.Split(strings.ReplaceAll(string(data), "\r", ""), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n"), nil
}

// EnsurePersona assigns the per-user persona once (random age + country).
func (ai *EVAAIConfig) EnsurePersona(state *model.EVAChatSession, rng *rand.Rand) {
	if state.PersonaAge == 0 {
		state.PersonaAge = evaAIPersonaAgeMin + rng.Intn(evaAIPersonaAgeMax-evaAIPersonaAgeMin+1)
	}
	if state.PersonaCountry == "" && len(ai.Countries) > 0 {
		state.PersonaCountry = ai.Countries[rng.Intn(len(ai.Countries))]
	}
}

// Composable reports whether a result's text may be rewritten by the AI.
func evaAIComposable(result EVAReplyResult) bool {
	if result.Text == "" || result.HasUsername || result.SnapShared {
		return false
	}
	if evaTerminalCategories[result.SourcePool] {
		return false
	}
	_, ok := evaAIIntents[result.SourcePool]
	return ok || strings.HasSuffix(result.SourcePool, ".txt")
}

// Compose asks the model for the reply text. It returns the validated text or
// an error (caller keeps the TXT line on error).
func (ai *EVAAIConfig) Compose(ctx context.Context, engine *EVAChatEngine, state *model.EVAChatSession, userMsg string, result EVAReplyResult, usernames []string, rng *rand.Rand) (string, error) {
	if ai == nil {
		return "", errors.New("ai disabled")
	}
	ai.EnsurePersona(state, rng)

	system := ai.systemPrompt(state)
	user := ai.userPrompt(engine, state, userMsg, result, rng)

	raw, err := ai.chat(ctx, system, user)
	if err != nil {
		return "", err
	}
	text, err := evaAISanitize(raw, state, usernames)
	if err != nil {
		return "", err
	}
	return text, nil
}

func (ai *EVAAIConfig) systemPrompt(state *model.EVAChatSession) string {
	persona := ai.Persona
	if strings.TrimSpace(persona) == "" {
		persona = "you are a {age} year old girl from {country} chatting with a stranger on an anonymous chat app. playful, chill, flirty, all lowercase, very short messages."
	}
	persona = strings.ReplaceAll(persona, "{age}", fmt.Sprintf("%d", state.PersonaAge))
	persona = strings.ReplaceAll(persona, "{country}", state.PersonaCountry)

	var b strings.Builder
	b.WriteString("you are roleplaying a real person in a text chat. stay in character no matter what. never say you are an ai, a bot, or a model.\n")
	b.WriteString(persona)
	b.WriteString("\n\nhard rules:\n")
	b.WriteString("- reply to what the stranger actually said; keep the context of the whole conversation.\n")
	b.WriteString("- one short line only (max 10 words), all lowercase, texting slang, no lists, no quotes.\n")
	b.WriteString("- never share any username, handle, link, phone number or social account, and never write the @ sign. the app sends the snapchat username itself when needed.\n")
	b.WriteString("- never claim to be under 18 and never talk sexually about minors.\n")
	b.WriteString("- never repeat a line you already said in this conversation.\n")
	b.WriteString("- english only.\n")
	b.WriteString("- output only the message text, nothing else.")
	return b.String()
}

func (ai *EVAAIConfig) userPrompt(engine *EVAChatEngine, state *model.EVAChatSession, userMsg string, result EVAReplyResult, rng *rand.Rand) string {
	var b strings.Builder
	b.WriteString("conversation so far (oldest first):\n")
	lines := state.Transcript
	// the engine already appended this user message and the TXT draft of the
	// bot reply (plus a queued second line); show history without the draft.
	for n := len(lines); n > 0 && lines[n-1].Who == "bot"; n = len(lines) {
		lines = lines[:n-1]
	}
	if len(lines) > evaAIHistoryLines {
		lines = lines[len(lines)-evaAIHistoryLines:]
	}
	if len(lines) <= 1 {
		b.WriteString("(nothing yet)\n")
	}
	for i, line := range lines {
		if i == len(lines)-1 && line.Who == "user" {
			break
		}
		who := "stranger"
		if line.Who == "bot" {
			who = "you"
		}
		fmt.Fprintf(&b, "%s: %s\n", who, line.Text)
	}
	facts := make([]string, 0, 3)
	if state.UserGender != "" || state.HasUserAge {
		facts = append(facts, fmt.Sprintf("stranger is %s%d", state.UserGender, state.UserAge))
	}
	if state.UserCountry != "" {
		facts = append(facts, "stranger is from "+state.UserCountry)
	}
	if len(facts) > 0 {
		b.WriteString("known facts: " + strings.Join(facts, "; ") + "\n")
	}
	if userMsg != "" {
		fmt.Fprintf(&b, "\nstranger just said: %q\n", userMsg)
	} else {
		b.WriteString("\nyou are opening the chat (the stranger has not written yet).\n")
	}
	intent := evaAIIntents[result.SourcePool]
	if intent == "" {
		intent = "reply naturally in the same spirit as the example lines below."
	}
	fmt.Fprintf(&b, "\nyour next message must: %s\n", intent)

	examples := evaAIStyleSamples(engine, result.SourcePool, result.Text, rng)
	if len(examples) > 0 {
		b.WriteString("examples of your usual style for this kind of message (do not copy, match the vibe and length):\n")
		for _, ex := range examples {
			b.WriteString("- " + ex + "\n")
		}
	}
	b.WriteString("\nwrite the message now:")
	return b.String()
}

func evaAIStyleSamples(engine *EVAChatEngine, pool, draft string, rng *rand.Rand) []string {
	samples := []string{}
	if draft != "" && !strings.Contains(draft, evaUsernamePlaceholder) {
		samples = append(samples, draft)
	}
	if engine == nil {
		return samples
	}
	lines := engine.pools[pool]
	if len(lines) == 0 {
		return samples
	}
	perm := rng.Perm(len(lines))
	for _, idx := range perm {
		if len(samples) >= evaAIStyleExamples {
			break
		}
		line := lines[idx]
		if line == draft || strings.Contains(line, evaUsernamePlaceholder) || strings.Contains(strings.ToLower(line), "{snap}") {
			continue
		}
		samples = append(samples, line)
	}
	return samples
}

// evaAISanitize enforces the hard rules on the model output.
func evaAISanitize(raw string, state *model.EVAChatSession, usernames []string) (string, error) {
	text := strings.TrimSpace(raw)
	if i := strings.IndexAny(text, "\r\n"); i >= 0 {
		text = strings.TrimSpace(text[:i])
	}
	text = strings.Trim(text, "\"'“”`")
	for _, prefix := range []string{"you:", "me:", "bot:", "eva:", "reply:", "message:"} {
		if strings.HasPrefix(strings.ToLower(text), prefix) {
			text = strings.TrimSpace(text[len(prefix):])
		}
	}
	text = strings.ToLower(text)
	if text == "" {
		return "", errors.New("empty model reply")
	}
	if len([]rune(text)) > evaAIMaxReplyRunes {
		return "", errors.New("model reply too long")
	}
	if evaAIForbidden.MatchString(text) {
		return "", errors.New("model reply contains a forbidden token")
	}
	for _, u := range usernames {
		u = strings.ToLower(strings.TrimSpace(strings.TrimPrefix(u, "@")))
		if len(u) >= 3 && strings.Contains(text, u) {
			return "", errors.New("model reply leaked a username")
		}
	}
	for _, line := range state.Transcript {
		if line.Who == "bot" && strings.EqualFold(strings.TrimSpace(line.Text), text) {
			return "", errors.New("model repeated an earlier line")
		}
	}
	return text, nil
}

type evaAIChatRequest struct {
	Model       string             `json:"model"`
	Messages    []evaAIChatMessage `json:"messages"`
	Temperature float64            `json:"temperature"`
	MaxTokens   int                `json:"max_tokens"`
}

type evaAIChatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type evaAIChatResponse struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

// chat performs one OpenAI-compatible /chat/completions call.
func (ai *EVAAIConfig) chat(ctx context.Context, system, user string) (string, error) {
	payload := evaAIChatRequest{
		Model: ai.Model,
		Messages: []evaAIChatMessage{
			{Role: "system", Content: system},
			{Role: "user", Content: user},
		},
		Temperature: ai.Temperature,
		MaxTokens:   evaAIMaxTokens,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	if ai.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, ai.Timeout)
		defer cancel()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, ai.BaseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+ai.APIKey)
	client := ai.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: evaAITimeout}
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", err
	}
	var parsed evaAIChatResponse
	if jsonErr := json.Unmarshal(data, &parsed); jsonErr != nil && resp.StatusCode < 400 {
		return "", fmt.Errorf("bad API response: %v", jsonErr)
	}
	if resp.StatusCode >= 400 {
		msg := strings.TrimSpace(string(data))
		if parsed.Error != nil && parsed.Error.Message != "" {
			msg = parsed.Error.Message
		}
		if len(msg) > 300 {
			msg = msg[:300]
		}
		return "", fmt.Errorf("API %d: %s", resp.StatusCode, msg)
	}
	if len(parsed.Choices) == 0 {
		return "", errors.New("API returned no choices")
	}
	return parsed.Choices[0].Message.Content, nil
}

// Test sends a tiny request and returns the model's answer (dashboard button).
func (ai *EVAAIConfig) Test(ctx context.Context) (string, error) {
	if ai == nil {
		return "", errors.New("AI is disabled")
	}
	state := model.EVAChatSession{PersonaAge: 22, PersonaCountry: "uk"}
	raw, err := ai.chat(ctx, ai.systemPrompt(&state), "stranger just said: \"hey wyd\"\nyour next message must: answer casually and ask them back.\nwrite the message now:")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(raw), nil
}

// Describe returns a short, key-free description for logs.
func (ai *EVAAIConfig) Describe() string {
	if ai == nil {
		return "AI compose: off"
	}
	countries := append([]string(nil), ai.Countries...)
	sort.Strings(countries)
	return fmt.Sprintf("AI compose: on (%s, model %s, persona f %d-%d, %d countries)", ai.BaseURL, ai.Model, evaAIPersonaAgeMin, evaAIPersonaAgeMax, len(countries))
}

// evaChatCompose applies the AI layer to an engine result and keeps the
// session transcript consistent with what is actually sent.
func evaChatCompose(cfg Config, session *model.EVAChatSession, userMsg string, result EVAReplyResult, rng *rand.Rand) EVAReplyResult {
	if cfg.EVAChat == nil || cfg.EVAChat.AI == nil || !evaAIComposable(result) {
		return result
	}
	ctx, cancel := context.WithTimeout(context.Background(), cfg.EVAChat.AI.Timeout+5*time.Second)
	defer cancel()
	text, err := cfg.EVAChat.AI.Compose(ctx, cfg.EVAChat.Engine, session, userMsg, result, cfg.MessageUsernames, rng)
	if err != nil {
		logf(cfg, "  -> [evachat-ai] fallback to %s line: %v\n", result.SourcePool, err)
		return result
	}
	// keep memory truthful: replace the TXT draft with the sent text
	for i, seen := len(session.Transcript)-1, 0; i >= 0 && seen < 3; i, seen = i-1, seen+1 {
		if session.Transcript[i].Who == "bot" && session.Transcript[i].Text == result.Text {
			session.Transcript[i].Text = text
			break
		}
	}
	result.Text = text
	result.Reason += " | ai composed"
	return result
}
