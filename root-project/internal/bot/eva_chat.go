package bot

// EVAChat Flow engine — a 1:1 Go port of the reference EVA bot's TXT-driven
// chat engine (eva_flow.py / EvaFlowBot). The behaviour is documented in
// docs/CHAT_ENGINE_MAPPING.md; keep both in sync.
//
//   input/<category>.txt  -> what the USER writes (triggers, '#' = comment)
//   output/<category>.txt -> what the BOT replies (pool, '|' also splits)
//   same file name        -> same category
//
// Stages: NEW -> WAIT_AGE_GENDER -> WAIT_COUNTRY -> MIDDLE -> END.

import (
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode"

	"chitchatbot/internal/model"
)

const (
	evaStageNew           = "NEW"
	evaStageWaitAgeGender = "WAIT_AGE_GENDER"
	evaStageWaitCountry   = "WAIT_COUNTRY"
	evaStageMiddle        = "MIDDLE"
	evaStageEnd           = "END"

	// evaMaxReplies is the reply cap: after this many bot lines the MIDDLE
	// stage says goodbye with closer.txt (reference: EVA_MAX_REPLIES=8).
	evaMaxReplies = 8
	// evaAskAfterMiddleReplies: if the user never gets horny/flirty and
	// never asks for the snap, the bot still asks (ask_snap.txt) after this
	// many MIDDLE-stage replies so the ask -> yes -> share chain can run.
	evaAskAfterMiddleReplies = 3
	// evaTranscriptLimit bounds the per-user memory kept in bot state.
	evaTranscriptLimit = 60

	evaUsernamePlaceholder = "%username%"

	poolGreeting        = "greeting.txt"
	poolAgeGender       = "age_gender.txt"
	poolAgeAsk          = "age_ask.txt"
	poolCountry         = "country.txt"
	poolFlirtyQuestions = "flirty_questions.txt"
	poolAskSnap         = "ask_snap.txt"
	poolRetryNudge      = "retry_nudge.txt"
	poolShareSnap       = "share_snap.txt"
	poolSnapchat        = "snapchat.txt"
	poolHorny           = "horny.txt"
	poolHornyAnswer     = "horny_answer.txt"
	poolCloser          = "closer.txt"
	poolEnglishOnly     = "english_only.txt"
	poolFlirtyReply     = "middle_chat/flirty_reply.txt"
	poolBusyLater       = "middle_chat/busy_later.txt"
)

// evaCategoryPriority is the scan order of input categories (first match
// wins). Any other input file is appended after these, sorted by name.
var evaCategoryPriority = []string{
	poolShareSnap,
	poolSnapchat,
	poolHorny,
	poolFlirtyReply,
	"how_are_you.txt",
	"your_age.txt",
	poolEnglishOnly,
	poolCountry,
	poolGreeting,
	poolAgeGender,
	poolBusyLater,
	"middle_chat/warm_reply.txt",
}

// evaTerminalCategories end the chat with a snap share.
var evaTerminalCategories = map[string]bool{poolShareSnap: true, poolSnapchat: true}

// evaNonPoolOutputs are output files never used as reply pools.
var evaNonPoolOutputs = map[string]bool{"info.txt": true, "readme.txt": true}

// evaRepeatablePools may fire more than once per user (always with a
// different line). Every other output pool fires at most once per user.
var evaRepeatablePools = map[string]bool{
	poolFlirtyQuestions: true,
	poolCloser:          true,
	poolHorny:           true,
	poolFlirtyReply:     true,
	poolAskSnap:         true,
	poolHornyAnswer:     true,
}

// evaSpecialCategories are handled only by dedicated logic, never by the
// regular category scan.
var evaSpecialCategories = map[string]bool{poolHornyAnswer: true}

// evaRequiredPools must exist in output/ for the funnel to work.
var evaRequiredPools = []string{poolGreeting, poolAgeGender, poolFlirtyQuestions, poolAskSnap, poolShareSnap, poolCloser}

var (
	// Leet/typo fallback for direct horny QUESTIONS (message must carry "?").
	evaHornyQuestionLeet = []*regexp.Regexp{
		regexp.MustCompile(`\bh[o0*]rn[yi1]\b`),
		regexp.MustCompile(`\bdtf\b`),
		regexp.MustCompile(`\bturned? on\b`),
		regexp.MustCompile(`\bin (the )?m[o0]od\b`),
		regexp.MustCompile(`\bu down\b`),
		regexp.MustCompile(`\bwet\b`),
	}
	// Leet/obfuscated horny keywords on top of input/horny.txt.
	evaHornyLeet = []*regexp.Regexp{
		regexp.MustCompile(`\bh[o0*]rn[yi1]\b`),
		regexp.MustCompile(`\bs[e3]x[yi]?\b`),
		regexp.MustCompile(`\bs[e3]xt(ing|in|me)?\b`),
		regexp.MustCompile(`\bn[o0]{1,2}d[e3]s?\b`),
		regexp.MustCompile(`\bd[i1]ck\b`),
		regexp.MustCompile(`\bc[o0]ck\b`),
		regexp.MustCompile(`\bp[u*]ss[yi]?\b`),
		regexp.MustCompile(`\bc[u*]m\b`),
		regexp.MustCompile(`\bf[u*]{1,2}ck\w*\b`),
		regexp.MustCompile(`\bnsfw\b`),
		regexp.MustCompile(`\b18\+`),
		regexp.MustCompile(`\bo[n]?ly ?fans\b`),
		regexp.MustCompile(`\bmakeout\b`),
		regexp.MustCompile(`\bhook ?up\b`),
	}
	evaGenderAgeRe = []*regexp.Regexp{
		regexp.MustCompile(`\b([mf])[\s\-_. ]{0,2}(\d{1,2})\b`),
		regexp.MustCompile(`\b(\d{1,2})[\s\-_. ]{0,2}([mf])\b`),
	}
	// User asks for the bot's snap/handle -> share immediately.
	evaSnapAskRe = regexp.MustCompile(`(\bsnap\b|\bsc\b|\bsnapchat\b|\bsnp\b|\bsn-?p\b|\bghost\b|\binsta\b|\binstagram\b|\btelegram\b|\btg\b|\bdiscord\b|\bdc\b|@)`)
	// Firm refusal ("dont have snap") never triggers a share.
	evaSnapRefusalRe = regexp.MustCompile(`\b(dont have|don't have|do not have|no snap|not on snap|not on sc|deleted (my )?snap|no sc)\b`)
	// Bare yes-words — only count AFTER the bot's ask.
	evaYesRe = regexp.MustCompile(`\b(yes+|yes i got|i got it|i got u|got it|got u|yea+h*|yep+|yeap*|yeop+|yopp|yas|sure|ofc|of course|ok+|okay+|okie|okies|okey|k+|send|send it|send it over|drop|drop it|drop ur|share|share me|gimme|give me|here|here you go|here u go|add me|added|done|deal|bet|say less|fr|im down|i am down|down|whats ur|what's ur|whats ur snap|whats ur sc|whats ur snapchat|ur user|ur snap|ur sc|ur username)\b`)
	// Negative words after the ask -> one retry_nudge.txt line.
	evaNegativeRe = regexp.MustCompile(`\b(no|nope|nah|naw|not really|never|later|nvm|nevermind)\b`)
	evaTokenSplit = regexp.MustCompile(`[^a-z0-9+]+`)
	evaCountryRe  = regexp.MustCompile(`(?i)\{country\}`)
	evaSnapPhRe   = regexp.MustCompile(`(?i)\{snap\}|%username%`)
)

var evaSlang = map[string]string{
	"u": "you", "ur": "your", "yr": "your", "yur": "your",
	"r": "are", "im": "i am", "ims": "i am",
	"wat": "what", "wats": "what is", "wut": "what", "wts": "what is",
	"whats": "what is", "what's": "what is",
	"whr": "where", "wbu": "what about you", "wby": "what about you",
	"hbu": "how about you", "sc": "snapchat",
}

var evaCountryFallback = map[string]string{
	"usa": "usa", "us": "usa", "america": "usa", "united states": "usa",
	"uk": "uk", "england": "uk", "united kingdom": "uk", "london": "uk",
	"canada": "canada", "australia": "australia", "germany": "germany",
	"france": "france", "india": "india", "bangladesh": "bangladesh",
	"pakistan": "pakistan", "brazil": "brazil", "mexico": "mexico",
}

var evaSilentFallback = []string{"hii", "hey", "lol", "hmm", "oh nice"}

// EVAChatConfig is attached to a worker only when EVAChat Flow is selected.
// The engine is immutable after loading and can safely be shared by workers.
type EVAChatConfig struct {
	DataRoot string
	Engine   *EVAChatEngine
	// AI is the optional compose layer (eva_ai.go); nil = TXT lines only.
	AI *EVAAIConfig
}

// EVAReplyResult describes one decision from the stateful EVAChat engine.
type EVAReplyResult struct {
	Text string
	// Extra holds additional bot lines to send right after Text (the 2-line
	// horny-question answer: horny_answer.txt line, then a horny.txt line).
	Extra      []string
	SourcePool string
	Ended      bool
	Reason     string
	// HasUsername reports that Text contained %username%/{snap} and was
	// rendered with an assigned snap/tg username. Callers use it for the
	// existing "snap/tg/user" counter and flagged-user bookkeeping.
	HasUsername bool
	// SnapShared reports that this reply is the snap share line.
	SnapShared bool
}

// EVAReplyOptions carries per-worker facts the shared engine cannot know.
type EVAReplyOptions struct {
	// HasUsernames is true when the worker has at least one snap/tg username
	// configured in the dashboard. Without it a %username% share line would
	// render empty, so the engine closes politely instead of sharing.
	HasUsernames bool
}

// EVAChatEngine holds the loaded TXT database and the compiled matchers.
type EVAChatEngine struct {
	dataRoot  string
	matchers  map[string]*evaMatcher
	inputs    map[string][]string
	pools     map[string][]string
	countries map[string]string
	countryKw []string // longest first
	scanOrder []string
	maxReply  int
}

// LoadEVAChatEngine validates and loads the user-editable TXT data pack.
func LoadEVAChatEngine(dataRoot string) (*EVAChatEngine, error) {
	inputs, err := loadEVATextTree(filepath.Join(dataRoot, "input"), true)
	if err != nil {
		return nil, fmt.Errorf("load EVAChat input triggers: %w", err)
	}
	pools, err := loadEVATextTree(filepath.Join(dataRoot, "output"), false)
	if err != nil {
		return nil, fmt.Errorf("load EVAChat reply pools: %w", err)
	}
	for name := range pools {
		if evaNonPoolOutputs[strings.ToLower(name)] {
			delete(pools, name)
		}
	}
	for _, required := range evaRequiredPools {
		if len(pools[required]) == 0 {
			return nil, fmt.Errorf("EVAChat requires output/%s with at least one line", required)
		}
	}
	if len(inputs[poolGreeting]) == 0 {
		return nil, fmt.Errorf("EVAChat requires input/%s with at least one trigger", poolGreeting)
	}

	countries, err := loadEVACountries(filepath.Join(dataRoot, "countries.txt"))
	if err != nil {
		return nil, err
	}
	if len(countries) == 0 {
		countries = make(map[string]string, len(evaCountryFallback))
		for k, v := range evaCountryFallback {
			countries[k] = v
		}
	}
	countryKw := make([]string, 0, len(countries))
	for kw := range countries {
		countryKw = append(countryKw, kw)
	}
	sort.Slice(countryKw, func(i, j int) bool {
		if len(countryKw[i]) != len(countryKw[j]) {
			return len(countryKw[i]) > len(countryKw[j])
		}
		return countryKw[i] < countryKw[j]
	})

	matchers := make(map[string]*evaMatcher, len(inputs))
	for category, triggers := range inputs {
		var extra []*regexp.Regexp
		if category == poolHorny {
			extra = evaHornyLeet
		}
		matchers[category] = newEVAMatcher(triggers, category == poolEnglishOnly, extra)
	}

	known := make(map[string]bool, len(inputs))
	for name := range inputs {
		known[name] = true
	}
	order := make([]string, 0, len(inputs))
	for _, name := range evaCategoryPriority {
		if known[name] {
			order = append(order, name)
			delete(known, name)
		}
	}
	custom := make([]string, 0, len(known))
	for name := range known {
		custom = append(custom, name)
	}
	sort.Strings(custom)
	order = append(order, custom...)

	return &EVAChatEngine{
		dataRoot:  dataRoot,
		matchers:  matchers,
		inputs:    inputs,
		pools:     pools,
		countries: countries,
		countryKw: countryKw,
		scanOrder: order,
		maxReply:  evaMaxReplies,
	}, nil
}

func loadEVATextTree(root string, isInput bool) (map[string][]string, error) {
	if info, err := os.Stat(root); err != nil {
		return nil, err
	} else if !info.IsDir() {
		return nil, fmt.Errorf("%s is not a directory", root)
	}
	loaded := make(map[string][]string)
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || !strings.EqualFold(filepath.Ext(path), ".txt") {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		key := strings.ToLower(filepath.ToSlash(rel))
		lines, err := loadEVALines(path, isInput)
		if err != nil {
			return err
		}
		if len(lines) > 0 {
			loaded[key] = lines
		}
		return nil
	})
	return loaded, err
}

// loadEVALines reads an input file (1 trigger per line, '#' comments,
// lowercased) or an output file (1 reply per line, '|' also splits).
func loadEVALines(path string, isInput bool) ([]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	text := strings.ReplaceAll(string(data), "\r", "\n")
	var parts []string
	if isInput {
		parts = strings.Split(text, "\n")
	} else {
		parts = strings.FieldsFunc(text, func(r rune) bool { return r == '\n' || r == '|' })
	}
	result := make([]string, 0, len(parts))
	for _, line := range parts {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if isInput {
			if strings.HasPrefix(line, "#") {
				continue
			}
			line = strings.ToLower(line)
		}
		result = append(result, line)
	}
	return result, nil
}

func loadEVACountries(path string) (map[string]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]string{}, nil
		}
		return nil, fmt.Errorf("read EVAChat countries: %w", err)
	}
	countries := make(map[string]string)
	for _, line := range strings.Split(strings.ReplaceAll(string(data), "\r", ""), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") || !strings.Contains(line, "=") {
			continue
		}
		key, value, _ := strings.Cut(line, "=")
		key = strings.ToLower(strings.TrimSpace(key))
		value = strings.ToLower(strings.TrimSpace(value))
		if key != "" && value != "" {
			countries[key] = value
		}
	}
	return countries, nil
}

// InputCategories returns the loaded trigger categories (sorted).
func (e *EVAChatEngine) InputCategories() []string {
	if e == nil {
		return nil
	}
	categories := make([]string, 0, len(e.inputs))
	for category := range e.inputs {
		categories = append(categories, category)
	}
	sort.Strings(categories)
	return categories
}

// OutputPools returns the loaded reply pools (sorted).
func (e *EVAChatEngine) OutputPools() []string {
	if e == nil {
		return nil
	}
	pools := make([]string, 0, len(e.pools))
	for pool := range e.pools {
		pools = append(pools, pool)
	}
	sort.Strings(pools)
	return pools
}

// ScanOrder returns the category matching order (priority + custom).
func (e *EVAChatEngine) ScanOrder() []string {
	if e == nil {
		return nil
	}
	return append([]string(nil), e.scanOrder...)
}

// ---------------------------------------------------------------------------
// Trigger matcher — the 4 txt-line passes + regex layer
// ---------------------------------------------------------------------------

type evaMatcher struct {
	strict  bool
	exact   map[string]bool
	ordered []string // triggers in file order (stable scan)
	sub     []string // triggers >= 3 chars (word-boundary substring)
	extra   []*regexp.Regexp
	norm    map[string]string // trigger -> normalized text
	subsets map[string][]string
}

func newEVAMatcher(triggers []string, strict bool, extra []*regexp.Regexp) *evaMatcher {
	m := &evaMatcher{
		strict:  strict,
		exact:   make(map[string]bool, len(triggers)),
		extra:   extra,
		norm:    make(map[string]string),
		subsets: make(map[string][]string),
	}
	for _, raw := range triggers {
		trig := strings.ToLower(strings.TrimSpace(raw))
		if trig == "" || strings.HasPrefix(trig, "#") || m.exact[trig] {
			continue
		}
		m.exact[trig] = true
		m.ordered = append(m.ordered, trig)
		if len(trig) >= 3 {
			m.sub = append(m.sub, trig)
		}
		if strict {
			continue
		}
		nt := evaNormalizeText(trig)
		normTokens := strings.Fields(nt)
		// Guard: skip normalized matching for triggers whose meaning would
		// be inflated by dropping punctuation ("ur @" -> "your").
		normSafe := len(normTokens) >= 2 || evaIsAlnum(trig)
		if normSafe && len(nt) >= 3 {
			m.norm[trig] = nt
		}
		var tset []string
		seen := make(map[string]bool)
		for _, t := range evaNormalizeTokens(trig) {
			if len(t) >= 3 && !seen[t] {
				seen[t] = true
				tset = append(tset, t)
			}
		}
		if len(tset) >= 2 {
			m.subsets[trig] = tset
		}
	}
	return m
}

// match returns the matched trigger (or "" when nothing matches).
func (m *evaMatcher) match(userMsg string) string {
	if m == nil {
		return ""
	}
	raw := strings.ToLower(strings.TrimSpace(userMsg))
	simple := evaSimpleText(userMsg)

	// Pass 1: exact (raw / punctuation-stripped)
	if m.exact[raw] {
		return raw
	}
	if m.exact[simple] {
		return simple
	}
	// Pass 2: word-boundary substring (trigger >= 3 chars)
	for _, trig := range m.sub {
		if evaWordBoundaryContains(raw, trig) || evaWordBoundaryContains(simple, trig) {
			return trig
		}
	}
	// Pass 2.5: extra regex layer (leet/obfuscated words)
	for _, pat := range m.extra {
		if pat.MatchString(raw) || pat.MatchString(simple) {
			return "<leet:" + pat.String() + ">"
		}
	}
	if m.strict {
		return ""
	}
	normMsg := evaNormalizeText(userMsg)
	normSet := make(map[string]bool)
	for _, t := range evaNormalizeTokens(userMsg) {
		normSet[t] = true
	}
	// Pass 3: normalized (slang-expanded) exact + substring
	for _, trig := range m.ordered {
		nt, ok := m.norm[trig]
		if !ok {
			continue
		}
		if normMsg == nt || evaWordBoundaryContains(normMsg, nt) {
			return trig
		}
	}
	// Pass 4: token-subset (order-insensitive, needs one token >= 4 chars)
	for _, trig := range m.ordered {
		tset, ok := m.subsets[trig]
		if !ok {
			continue
		}
		long := false
		all := true
		for _, t := range tset {
			if len(t) >= 4 {
				long = true
			}
			if !normSet[t] {
				all = false
				break
			}
		}
		if long && all {
			return trig
		}
	}
	return ""
}

func evaIsWordByte(b byte) bool {
	return b == '_' || (b >= '0' && b <= '9') || (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') || b >= 0x80
}

// evaWordBoundaryContains mimics re.search(r"\b" + re.escape(needle) + r"\b").
func evaWordBoundaryContains(hay, needle string) bool {
	if needle == "" || len(needle) > len(hay) {
		return false
	}
	startWord := evaIsWordByte(needle[0])
	endWord := evaIsWordByte(needle[len(needle)-1])
	offset := 0
	for {
		idx := strings.Index(hay[offset:], needle)
		if idx < 0 {
			return false
		}
		start := offset + idx
		end := start + len(needle)
		beforeWord := start > 0 && evaIsWordByte(hay[start-1])
		afterWord := end < len(hay) && evaIsWordByte(hay[end])
		// \b holds when exactly one side is a word character.
		if beforeWord != startWord && afterWord != endWord {
			return true
		}
		offset = start + 1
		if offset >= len(hay) {
			return false
		}
	}
}

func evaIsAlnum(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if !unicode.IsLetter(r) && !unicode.IsDigit(r) {
			return false
		}
	}
	return true
}

// evaSimpleText lowercases and replaces punctuation with spaces.
func evaSimpleText(value string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(value) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' || unicode.IsSpace(r) {
			b.WriteRune(r)
		} else {
			b.WriteRune(' ')
		}
	}
	return strings.Join(strings.Fields(b.String()), " ")
}

func evaNormalizeTokens(value string) []string {
	raw := evaTokenSplit.Split(strings.ToLower(value), -1)
	result := make([]string, 0, len(raw))
	for _, token := range raw {
		if token == "" {
			continue
		}
		if expanded, ok := evaSlang[token]; ok {
			result = append(result, strings.Fields(expanded)...)
			continue
		}
		result = append(result, token)
	}
	return result
}

func evaNormalizeText(value string) string {
	return strings.Join(evaNormalizeTokens(value), " ")
}

// ---------------------------------------------------------------------------
// Smart fallback detectors (no txt file needed)
// ---------------------------------------------------------------------------

func evaGenderAge(text string) (string, int, bool) {
	low := strings.ToLower(text)
	for _, re := range evaGenderAgeRe {
		m := re.FindStringSubmatch(low)
		if len(m) != 3 {
			continue
		}
		g, a := m[1], m[2]
		if _, err := strconv.Atoi(g); err == nil {
			g, a = a, g
		}
		age, err := strconv.Atoi(a)
		if err == nil && age >= 13 && age <= 99 {
			return g, age, true
		}
	}
	return "", 0, false
}

func (e *EVAChatEngine) countryFromText(text string) string {
	lower := " " + strings.ToLower(text) + " "
	simple := " " + evaSimpleText(text) + " "
	for _, kw := range e.countryKw {
		if strings.Contains(lower, " "+kw+" ") || strings.Contains(simple, " "+kw+" ") {
			return e.countries[kw]
		}
	}
	return ""
}

// ---------------------------------------------------------------------------
// Conversation state
// ---------------------------------------------------------------------------

// NewEVAChatSession returns a fully initialized conversation state.
func NewEVAChatSession() model.EVAChatSession {
	return model.EVAChatSession{
		Stage:       evaStageNew,
		PoolsUsed:   make(map[string]bool),
		UsedReplies: make(map[string][]string),
	}
}

func (e *EVAChatEngine) normalizeState(state *model.EVAChatSession) {
	if state.Stage == "" {
		state.Stage = evaStageNew
	}
	if state.PoolsUsed == nil {
		state.PoolsUsed = make(map[string]bool)
	}
	if state.UsedReplies == nil {
		state.UsedReplies = make(map[string][]string)
	}
	if state.Stage == evaStageEnd {
		state.Ended = true
	}
}

func evaRemember(state *model.EVAChatSession, who, text string) {
	state.Transcript = append(state.Transcript, model.EVATranscriptLine{N: len(state.Transcript) + 1, Who: who, Text: text})
	if len(state.Transcript) > evaTranscriptLimit {
		state.Transcript = state.Transcript[len(state.Transcript)-evaTranscriptLimit:]
	}
}

// Start opens a conversation where the bot writes first (DM / match modes):
// the opener is a greeting.txt line and the funnel waits for age/gender.
func (e *EVAChatEngine) Start(state *model.EVAChatSession, rng *rand.Rand) EVAReplyResult {
	if e == nil {
		return EVAReplyResult{Reason: "EVAChat engine unavailable"}
	}
	e.normalizeState(state)
	if state.Ended {
		return EVAReplyResult{Ended: true, Reason: "conversation already ended"}
	}
	if state.Started {
		return EVAReplyResult{Reason: "conversation already started"}
	}
	state.Started = true
	state.Stage = evaStageWaitAgeGender
	state.SequenceStep = 1
	result := e.pick(poolGreeting, state, rng, "bot opens -> greeting.txt")
	state.BotReplyCount++
	e.recordBot(state, &result)
	return result
}

// Reply processes one incoming message and mutates only the supplied session.
// It assumes usernames are configured; workers use ReplyWithOptions.
func (e *EVAChatEngine) Reply(message string, state *model.EVAChatSession, rng *rand.Rand) EVAReplyResult {
	return e.ReplyWithOptions(message, state, rng, EVAReplyOptions{HasUsernames: true})
}

// ReplyWithOptions is Reply with per-worker facts such as whether a snap/tg
// username list exists.
func (e *EVAChatEngine) ReplyWithOptions(message string, state *model.EVAChatSession, rng *rand.Rand, opts EVAReplyOptions) EVAReplyResult {
	if e == nil {
		return EVAReplyResult{Reason: "EVAChat engine unavailable"}
	}
	e.normalizeState(state)
	state.Started = true

	// Multi-line message: only the LAST line drives the machine.
	msg := evaLastLine(message)
	if msg == "" {
		return EVAReplyResult{Reason: "empty message ignored"}
	}
	evaRemember(state, "user", msg)

	result := e.replyCore(msg, state, rng, opts)
	e.recordBot(state, &result)
	return result
}

// recordBot stores the bot lines in memory and arms the ask chain after a
// horny.txt / middle_chat/flirty_reply.txt turn.
func (e *EVAChatEngine) recordBot(state *model.EVAChatSession, result *EVAReplyResult) {
	if result.Text != "" {
		evaRemember(state, "bot", result.Text)
		state.LastPool = result.SourcePool
	}
	for _, extra := range result.Extra {
		evaRemember(state, "bot", extra)
		state.LastPool = poolHorny
	}
	if result.SourcePool == poolHorny || result.SourcePool == poolFlirtyReply {
		state.AskNext = true
	}
	if state.Stage == evaStageMiddle && result.Text != "" {
		state.MiddleReplies++
	}
	if state.Stage == evaStageEnd {
		state.Ended = true
		result.Ended = true
	}
}

func (e *EVAChatEngine) replyCore(msg string, state *model.EVAChatSession, rng *rand.Rand, opts EVAReplyOptions) EVAReplyResult {
	// ---- ALWAYS: harvest facts from the message -------------------
	gender, age, hasGA := evaGenderAge(msg)
	if hasGA {
		if state.UserGender == "" {
			state.UserGender = gender
		}
		if !state.HasUserAge {
			state.UserAge = age
			state.HasUserAge = true
		}
	}
	if country := e.countryFromText(msg); country != "" {
		state.UserCountry = country
	}

	// ---- ALWAYS: underage block (18+) -----------------------------
	if (hasGA && age < 18) || (state.HasUserAge && state.UserAge < 18) {
		state.Blocked = true
	}

	// ---- ALWAYS: END means END ------------------------------------
	if state.Stage == evaStageEnd {
		state.Ended = true
		result := e.pick(poolCloser, state, rng, "END: chat over -> closer.txt")
		state.BotReplyCount++
		return result
	}

	// ---- ALWAYS: share gating (ask first, then share) -------------
	if e.shouldShare(msg, state) {
		state.Stage = evaStageEnd
		state.SnapPivoted = true
		state.SequenceStep = 8
		state.AskNext = false
		var result EVAReplyResult
		switch {
		case state.Blocked:
			state.SnapPivoted = false
			result = e.pick(poolCloser, state, rng, "positive but blocked (underage) -> closer.txt")
		case !opts.HasUsernames:
			state.SnapPivoted = false
			result = e.pick(poolCloser, state, rng, "positive but no usernames configured -> closer.txt")
		default:
			pool := poolShareSnap
			if cat := e.matchCategory(msg, evaTerminalCategories); cat != "" && len(e.pools[cat]) > 0 {
				pool = cat
			}
			result = e.pick(pool, state, rng, "positive reply -> "+pool+" -> END")
			result.SnapShared = true
		}
		state.BotReplyCount++
		return result
	}

	// ---- ALWAYS: horny QUESTION -> 2-line reply -------------------
	if e.isHornyQuestion(msg) {
		state.HornyDetected = true
		state.Stage = evaStageMiddle
		hornyLine := e.pick(poolHorny, state, rng, "")
		answer := e.pick(poolHornyAnswer, state, rng, "horny question -> horny_answer.txt + horny.txt (2 lines) -> ask chain")
		answer.Extra = []string{hornyLine.Text}
		state.AskNext = true
		state.BotReplyCount += 2
		return answer
	}

	// ---- ALWAYS: pending ask (STRICT — whatever the user said) ----
	if state.AskNext {
		state.AskNext = false
		state.AskedSnap = true
		state.SequenceStep = 7
		result := e.pick(poolAskSnap, state, rng, "ask snap -> ask_snap.txt (whatever user said)")
		state.BotReplyCount++
		return result
	}

	// ---- ALWAYS: refusal handling after the ask --------------------
	if state.AskedSnap && (state.LastPool == poolAskSnap || state.LastPool == poolRetryNudge) &&
		evaNegativeRe.MatchString(strings.ToLower(msg)) {
		var result EVAReplyResult
		if state.SnapNudges < 1 {
			state.SnapNudges++
			result = e.pick(poolRetryNudge, state, rng, "negative after ask -> retry_nudge.txt")
		} else {
			state.Stage = evaStageEnd
			result = e.pick(poolCloser, state, rng, "refused twice after ask -> closer.txt -> END")
		}
		state.BotReplyCount++
		return result
	}

	// ---- stage machine --------------------------------------------
	var result EVAReplyResult
	switch state.Stage {
	case evaStageNew:
		result = e.stageFirstSMS(msg, state, rng)
	case evaStageWaitAgeGender:
		result = e.stageWaitAgeGender(msg, state, rng)
	case evaStageWaitCountry:
		result = e.stageWaitCountry(msg, state, rng)
	default:
		result = e.stageMiddle(msg, state, rng)
	}
	state.BotReplyCount++
	return result
}

// Stage 1: first SMS.
func (e *EVAChatEngine) stageFirstSMS(msg string, state *model.EVAChatSession, rng *rand.Rand) EVAReplyResult {
	if _, _, ok := evaGenderAge(msg); ok || e.hit(msg, poolAgeGender) {
		state.Stage = evaStageWaitCountry
		state.SequenceStep = 2
		return e.pick(poolAgeGender, state, rng, "first SMS: age/gender -> age_gender.txt")
	}
	if e.hit(msg, poolCountry) {
		state.Stage = evaStageWaitCountry
		state.SequenceStep = 3
		return e.pick(poolCountry, state, rng, "first SMS: country question -> country.txt")
	}
	state.Stage = evaStageWaitAgeGender
	state.SequenceStep = 1
	return e.pick(poolGreeting, state, rng, "first SMS -> greeting.txt")
}

// Stage 2: waiting for age/gender.
func (e *EVAChatEngine) stageWaitAgeGender(msg string, state *model.EVAChatSession, rng *rand.Rand) EVAReplyResult {
	if _, _, ok := evaGenderAge(msg); ok && e.available(poolAgeGender, state) {
		state.Stage = evaStageWaitCountry
		state.Misses = 0
		state.SequenceStep = 2
		return e.pick(poolAgeGender, state, rng, "age/gender captured -> age_gender.txt")
	}
	if e.hit(msg, poolCountry) && e.available(poolCountry, state) {
		state.Stage = evaStageWaitCountry
		state.Misses = 0
		return e.pick(poolCountry, state, rng, "country talk -> country.txt")
	}
	if cat := e.scanCategory(msg, state); cat != "" {
		state.Misses++
		result := e.pick(cat, state, rng, "category match -> "+cat)
		if state.Misses >= 2 {
			state.Stage = evaStageWaitCountry
			state.Misses = 0
			next := poolFlirtyQuestions
			if e.available(poolCountry, state) {
				next = poolCountry
			}
			result = e.pick(next, state, rng, result.Reason+" | progressing -> "+next)
		}
		return result
	}
	state.Misses++
	if state.Misses >= 2 || !e.available(poolAgeAsk, state) {
		state.Stage = evaStageWaitCountry
		state.Misses = 0
		next := poolFlirtyQuestions
		if e.available(poolCountry, state) {
			next = poolCountry
		}
		return e.pick(next, state, rng, "age asks exhausted -> "+next)
	}
	return e.pick(poolAgeAsk, state, rng, "waiting age/gender -> age_ask.txt")
}

// Stage 3+4: country question -> country.txt, country answer -> flirty question.
func (e *EVAChatEngine) stageWaitCountry(msg string, state *model.EVAChatSession, rng *rand.Rand) EVAReplyResult {
	// A country statement ("usa", "im from canada") is the user's answer;
	// a country QUESTION ("where u from?") is answered from country.txt.
	// input/country.txt also lists statements, so a message without "?"
	// that names a country counts as the answer.
	if e.countryFromText(msg) != "" && (!e.hit(msg, poolCountry) || !strings.Contains(msg, "?")) {
		state.Stage = evaStageMiddle
		state.SequenceStep = 4
		return e.pick(poolFlirtyQuestions, state, rng, "country captured -> flirty_questions.txt")
	}
	if e.hit(msg, poolCountry) && e.available(poolCountry, state) {
		return e.pick(poolCountry, state, rng, "country question -> country.txt")
	}
	if _, _, ok := evaGenderAge(msg); ok && state.UserGender == "" && e.available(poolAgeGender, state) {
		return e.pick(poolAgeGender, state, rng, "late age/gender -> age_gender.txt")
	}
	if cat := e.scanCategory(msg, state); cat != "" {
		state.Misses++
		result := e.pick(cat, state, rng, "category match -> "+cat)
		if state.Misses >= 2 {
			state.Stage = evaStageMiddle
			result = e.pick(poolFlirtyQuestions, state, rng, result.Reason+" | progressing -> flirty_questions.txt")
		}
		return result
	}
	state.Stage = evaStageMiddle
	return e.pick(poolFlirtyQuestions, state, rng, "country stage fallback -> flirty_questions.txt")
}

// Stage 5: MIDDLE — "detect next user msg".
func (e *EVAChatEngine) stageMiddle(msg string, state *model.EVAChatSession, rng *rand.Rand) EVAReplyResult {
	if state.BotReplyCount >= e.maxReply {
		state.Stage = evaStageEnd
		return e.pick(poolCloser, state, rng, fmt.Sprintf("max replies (%d) -> closer.txt", e.maxReply))
	}
	// user insists on leaving again -> polite END
	if e.hit(msg, poolBusyLater) && !e.available(poolBusyLater, state) {
		state.Stage = evaStageEnd
		return e.pick(poolCloser, state, rng, "user leaving again -> closer.txt -> END")
	}
	if cat := e.scanCategory(msg, state); cat != "" {
		return e.pick(cat, state, rng, "category match -> "+cat)
	}
	// user never got flirty and never asked: ask for the snap ourselves
	if !state.AskedSnap && state.MiddleReplies >= evaAskAfterMiddleReplies {
		state.AskedSnap = true
		state.SequenceStep = 7
		return e.pick(poolAskSnap, state, rng, "middle chat settled -> ask_snap.txt")
	}
	return e.pick(poolFlirtyQuestions, state, rng, "no match -> flirty_questions.txt")
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func (e *EVAChatEngine) hit(msg, category string) bool {
	return e.matchers[category].match(msg) != ""
}

// matchCategory scans in priority order, optionally restricted to `only`.
func (e *EVAChatEngine) matchCategory(msg string, only map[string]bool) string {
	for _, cat := range e.scanOrder {
		if only != nil && !only[cat] {
			continue
		}
		if e.hit(msg, cat) {
			return cat
		}
	}
	return ""
}

// scanCategory returns the first AVAILABLE category whose triggers match.
// Terminal, special and age_gender categories are skipped; spent pools too.
func (e *EVAChatEngine) scanCategory(msg string, state *model.EVAChatSession) string {
	for _, cat := range e.scanOrder {
		if evaTerminalCategories[cat] || evaSpecialCategories[cat] || cat == poolAgeGender {
			continue
		}
		if !e.available(cat, state) {
			continue
		}
		if e.hit(msg, cat) {
			return cat
		}
	}
	return ""
}

// available implements the NO-REPEAT rule: a non-repeatable pool fires once.
func (e *EVAChatEngine) available(cat string, state *model.EVAChatSession) bool {
	if evaRepeatablePools[cat] {
		return true
	}
	return !state.PoolsUsed[cat]
}

// isHornyQuestion: the user directly ASKS a horny question.
func (e *EVAChatEngine) isHornyQuestion(msg string) bool {
	if e.hit(msg, poolHornyAnswer) {
		return true
	}
	low := strings.ToLower(msg)
	if strings.Contains(low, "?") {
		for _, p := range evaHornyQuestionLeet {
			if p.MatchString(low) {
				return true
			}
		}
	}
	return false
}

// shouldShare decides whether this message triggers the snap share.
func (e *EVAChatEngine) shouldShare(msg string, state *model.EVAChatSession) bool {
	low := strings.ToLower(msg)
	if evaSnapRefusalRe.MatchString(low) {
		return false
	}
	if evaSnapAskRe.MatchString(low) || e.hit(msg, poolSnapchat) {
		return true
	}
	if state.AskedSnap && (e.hit(msg, poolShareSnap) || evaYesRe.MatchString(low) || strings.Contains(msg, "@")) {
		return true
	}
	return false
}

// pick selects a reply line from an output pool.
//
// Pool level: a non-repeatable pool is used once per user; a spent pool is
// redirected to flirty_questions.txt. Line level: unused lines first, never
// the same line back-to-back. Resolves {country}; %username%/{snap} are left
// for evaChatRender (dashboard username assignment).
func (e *EVAChatEngine) pick(poolFile string, state *model.EVAChatSession, rng *rand.Rand, reason string) EVAReplyResult {
	if !evaRepeatablePools[poolFile] && state.PoolsUsed[poolFile] {
		reason += " | " + poolFile + " already used once -> flirty_questions.txt"
		poolFile = poolFlirtyQuestions
	}
	pool := e.pools[poolFile]
	if len(pool) == 0 {
		return EVAReplyResult{
			Text:       evaSilentFallback[rng.Intn(len(evaSilentFallback))],
			SourcePool: poolFile,
			Reason:     reason + " (pool missing -> fallback)",
		}
	}

	used := state.UsedReplies[poolFile]
	usedSet := make(map[string]bool, len(used))
	for _, u := range used {
		usedSet[u] = true
	}
	fresh := make([]string, 0, len(pool))
	for _, line := range pool {
		if !usedSet[line] {
			fresh = append(fresh, line)
		}
	}
	if len(fresh) == 0 {
		fresh = append(fresh, pool...)
	}
	if len(fresh) > 1 && len(used) > 0 {
		last := used[len(used)-1]
		noRepeat := fresh[:0:0]
		for _, line := range fresh {
			if line != last {
				noRepeat = append(noRepeat, line)
			}
		}
		if len(noRepeat) > 0 {
			fresh = noRepeat
		}
	}
	line := fresh[rng.Intn(len(fresh))]
	used = append(used, line)
	if limit := max(len(pool)*2, 16); len(used) > limit {
		used = append([]string(nil), used[len(used)/2:]...)
	}
	state.UsedReplies[poolFile] = used
	if !evaRepeatablePools[poolFile] {
		state.PoolsUsed[poolFile] = true
	}

	text := line
	if evaCountryRe.MatchString(text) {
		country := state.UserCountry
		if country == "" {
			country = "usa"
		}
		text = evaCountryRe.ReplaceAllString(text, country)
	}
	hasUsername := evaSnapPhRe.MatchString(text)
	if hasUsername {
		text = evaSnapPhRe.ReplaceAllString(text, evaUsernamePlaceholder)
	}
	return EVAReplyResult{Text: text, SourcePool: poolFile, Reason: reason, HasUsername: hasUsername}
}

func evaLastLine(message string) string {
	lines := strings.Split(strings.ReplaceAll(message, "\r", ""), "\n")
	for index := len(lines) - 1; index >= 0; index-- {
		if line := strings.TrimSpace(lines[index]); line != "" {
			return line
		}
	}
	return ""
}

// ---------------------------------------------------------------------------
// Worker glue
// ---------------------------------------------------------------------------

func evaChatEnabled(cfg Config) bool {
	return cfg.ChatMode == model.ChatModeEVAChat && cfg.EVAChat != nil && cfg.EVAChat.Engine != nil
}

func evaChatSession(accountState *model.AccountState, userID string) model.EVAChatSession {
	if accountState.EVAChatSessions == nil {
		accountState.EVAChatSessions = make(map[string]model.EVAChatSession)
	}
	session, ok := accountState.EVAChatSessions[userID]
	if !ok {
		session = NewEVAChatSession()
	}
	return session
}

func evaChatStart(cfg Config, accountState *model.AccountState, userID string, rng *rand.Rand) EVAReplyResult {
	session := evaChatSession(accountState, userID)
	result := cfg.EVAChat.Engine.Start(&session, rng)
	result = evaChatCompose(cfg, &session, "", result, rng)
	accountState.EVAChatSessions[userID] = session
	return evaChatRender(cfg, accountState, userID, rng, result)
}

func evaChatReply(cfg Config, accountState *model.AccountState, userID, incoming string, rng *rand.Rand) EVAReplyResult {
	session := evaChatSession(accountState, userID)
	opts := EVAReplyOptions{HasUsernames: len(cfg.MessageUsernames) > 0}
	result := cfg.EVAChat.Engine.ReplyWithOptions(incoming, &session, rng, opts)
	result = evaChatCompose(cfg, &session, evaLastLine(incoming), result, rng)
	accountState.EVAChatSessions[userID] = session
	return evaChatRender(cfg, accountState, userID, rng, result)
}

// evaChatRender fills %username% in an EVAChat reply with the same per-user
// snap/tg username assignment used by Single/Folder sequence modes, so the
// dashboard "snap/tg/user" counter and flagged-user tracking keep working.
func evaChatRender(cfg Config, accountState *model.AccountState, userID string, rng *rand.Rand, result EVAReplyResult) EVAReplyResult {
	if !result.HasUsername || !strings.Contains(result.Text, evaUsernamePlaceholder) {
		result.HasUsername = false
		return result
	}
	assigned := getOrAssignUsername(cfg, accountState, userID, rng)
	result.Text = renderMessageTemplate(result.Text, rng, assigned)
	result.HasUsername = assigned != "" && strings.Contains(result.Text, assigned)
	return result
}
