package bot

import (
	"context"
	"encoding/json"
	"math/rand"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"chitchatbot/internal/model"
)

// mockAIServer imitates an OpenAI-compatible /chat/completions endpoint and
// lets the test choose the model's answer.
func mockAIServer(t *testing.T, answer func(system, user string) string) (*httptest.Server, *int32) {
	t.Helper()
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		if r.URL.Path != "/v1/chat/completions" || r.Header.Get("Authorization") != "Bearer test-key" {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error":{"message":"bad auth or path"}}`))
			return
		}
		var req evaAIChatRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || len(req.Messages) != 2 {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		content := answer(req.Messages[0].Content, req.Messages[1].Content)
		resp := map[string]any{"choices": []map[string]any{{"message": map[string]string{"role": "assistant", "content": content}}}}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	t.Cleanup(srv.Close)
	return srv, &calls
}

func testEVAAI(t *testing.T, srv *httptest.Server) (*EVAChatEngine, *EVAAIConfig) {
	t.Helper()
	root, err := EnsureEVAChatData(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	engine, err := LoadEVAChatEngine(root)
	if err != nil {
		t.Fatal(err)
	}
	ai, err := LoadEVAAI(root, model.MessageSettings{AIEnabled: true, AIAPIKey: "test-key", AIBaseURL: srv.URL + "/v1/", AIModel: "mock"})
	if err != nil {
		t.Fatal(err)
	}
	if ai.Persona == "" || len(ai.Countries) < 5 {
		t.Fatalf("persona pack not loaded: %+v", ai)
	}
	return engine, ai
}

func TestEVAAIDisabledWhenNotEnabled(t *testing.T) {
	ai, err := LoadEVAAI(t.TempDir(), model.MessageSettings{})
	if err != nil || ai != nil {
		t.Fatalf("disabled AI must return nil: %v %v", ai, err)
	}
	if _, err := LoadEVAAI(t.TempDir(), model.MessageSettings{AIEnabled: true}); err == nil {
		t.Fatal("enabled AI without key must fail")
	}
}

func TestEVAAIComposesContextAwareReplyAndKeepsFlow(t *testing.T) {
	var lastUser string
	n := 0
	srv, calls := mockAIServer(t, func(system, user string) string {
		lastUser = user
		if !strings.Contains(system, "year old girl from") || !strings.Contains(system, "never share any username") {
			return "@leak"
		}
		n++
		if n == 1 {
			return "Haha chilling, u?"
		}
		return "F 22 here, Wbu?"
	})
	engine, ai := testEVAAI(t, srv)
	cfg := Config{ChatMode: model.ChatModeEVAChat, EVAChat: &EVAChatConfig{Engine: engine, AI: ai}, MessageUsernames: []string{"snapqueen"}}
	state := &model.AccountState{}
	rng := rand.New(rand.NewSource(1))

	opener := evaChatStart(cfg, state, "u1", rng)
	if opener.Text != "haha chilling, u?" || !strings.Contains(opener.Reason, "ai composed") {
		t.Fatalf("opener not composed: %+v", opener)
	}
	session := state.EVAChatSessions["u1"]
	if session.PersonaAge < 20 || session.PersonaAge > 25 || session.PersonaCountry == "" {
		t.Fatalf("persona not assigned: %+v", session)
	}
	if session.Transcript[len(session.Transcript)-1].Text != "haha chilling, u?" {
		t.Fatalf("transcript must hold the sent text: %+v", session.Transcript)
	}

	reply := evaChatReply(cfg, state, "u1", "m21 wyd", rng)
	if reply.SourcePool != "age_gender.txt" || reply.Text != "f 22 here, wbu?" {
		t.Fatalf("reply not composed from the engine pool: %+v", reply)
	}
	if !strings.Contains(lastUser, `stranger just said: "m21 wyd"`) || !strings.Contains(lastUser, "you: haha chilling, u?") || !strings.Contains(lastUser, "stranger is m21") {
		t.Fatalf("prompt lacks context:\n%s", lastUser)
	}
	// the share line is never rewritten and still carries the username
	share := evaChatReply(cfg, state, "u1", "whats ur snap", rng)
	before := atomic.LoadInt32(calls)
	if share.SourcePool != "share_snap.txt" || !strings.Contains(share.Text, "snapqueen") || strings.Contains(share.Reason, "ai composed") {
		t.Fatalf("share line must stay TXT: %+v", share)
	}
	if atomic.LoadInt32(calls) != before {
		t.Fatal("AI must not be called for the share line")
	}
}

func TestEVAAIFallsBackToTXTOnBadOutput(t *testing.T) {
	answers := []string{"add me @ snapqueen", "", strings.Repeat("x", 300), "As an AI I cannot", "visit http://x.com"}
	idx := 0
	srv, _ := mockAIServer(t, func(_, _ string) string {
		a := answers[idx%len(answers)]
		idx++
		return a
	})
	engine, ai := testEVAAI(t, srv)
	cfg := Config{ChatMode: model.ChatModeEVAChat, EVAChat: &EVAChatConfig{Engine: engine, AI: ai}, MessageUsernames: []string{"snapqueen"}}
	state := &model.AccountState{}
	rng := rand.New(rand.NewSource(2))
	for i := 0; i < len(answers); i++ {
		r := evaChatReply(cfg, state, "u2", "random words here", rng)
		if r.Text == "" || strings.Contains(r.Reason, "ai composed") || strings.Contains(r.Text, "snapqueen") {
			t.Fatalf("bad model output %d must fall back to TXT: %+v", i, r)
		}
	}
}

func TestEVAAIFallsBackOnAPIError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"error":{"message":"rate limited"}}`))
	}))
	defer srv.Close()
	engine, ai := testEVAAI(t, srv)
	cfg := Config{ChatMode: model.ChatModeEVAChat, EVAChat: &EVAChatConfig{Engine: engine, AI: ai}}
	state := &model.AccountState{}
	r := evaChatStart(cfg, state, "u3", rand.New(rand.NewSource(3)))
	if r.SourcePool != "greeting.txt" || r.Text == "" || strings.Contains(r.Reason, "ai composed") {
		t.Fatalf("API error must fall back: %+v", r)
	}
	if _, err := ai.Test(context.Background()); err == nil || !strings.Contains(err.Error(), "429") {
		t.Fatalf("Test must surface the API error: %v", err)
	}
}

func TestEVAAITestEndpoint(t *testing.T) {
	srv, _ := mockAIServer(t, func(_, _ string) string { return "nm just chillin, u?" })
	_, ai := testEVAAI(t, srv)
	answer, err := ai.Test(context.Background())
	if err != nil || answer != "nm just chillin, u?" {
		t.Fatalf("test call: %q %v", answer, err)
	}
	if strings.Contains(ai.Describe(), "test-key") {
		t.Fatal("Describe must never contain the API key")
	}
}

func TestEVAAISanitize(t *testing.T) {
	state := &model.EVAChatSession{Transcript: []model.EVATranscriptLine{{Who: "bot", Text: "hey u"}}}
	if got, err := evaAISanitize("  \"You: Haha OK\"\nsecond line", state, nil); err != nil || got != "haha ok" {
		t.Fatalf("sanitize: %q %v", got, err)
	}
	if _, err := evaAISanitize("hey u", state, nil); err == nil {
		t.Fatal("repeat of an earlier bot line must be rejected")
	}
	if _, err := evaAISanitize("hit me on snapqueen", state, []string{"@SnapQueen"}); err == nil {
		t.Fatal("username leak must be rejected")
	}
}
