package bot

// These tests mirror the reference engine's behaviour spec (test_flow.py of
// the EVA bot). Each conversation starts with the USER writing first (stage
// NEW) unless it calls Start(), which is the bot-writes-first opener.

import (
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"chitchatbot/internal/model"
)

func testEVAEngine(t *testing.T) *EVAChatEngine {
	t.Helper()
	root, err := EnsureEVAChatData(t.TempDir())
	if err != nil {
		t.Fatalf("EnsureEVAChatData: %v", err)
	}
	engine, err := LoadEVAChatEngine(root)
	if err != nil {
		t.Fatalf("LoadEVAChatEngine: %v", err)
	}
	return engine
}

type evaConv struct {
	t      *testing.T
	engine *EVAChatEngine
	rng    *rand.Rand
	state  model.EVAChatSession
	last   EVAReplyResult
}

func newEVAConv(t *testing.T, seed int64) *evaConv {
	t.Helper()
	return &evaConv{t: t, engine: testEVAEngine(t), rng: rand.New(rand.NewSource(seed)), state: NewEVAChatSession()}
}

func (c *evaConv) say(msg string) EVAReplyResult {
	c.t.Helper()
	c.last = c.engine.Reply(msg, &c.state, c.rng)
	return c.last
}

func (c *evaConv) expect(msg, pool string) EVAReplyResult {
	c.t.Helper()
	r := c.say(msg)
	if r.SourcePool != pool {
		c.t.Fatalf("%q -> expected %s, got %s (%s) text=%q state=%+v", msg, pool, r.SourcePool, r.Reason, r.Text, c.state)
	}
	if r.Text == "" {
		c.t.Fatalf("%q -> empty reply from %s", msg, pool)
	}
	return r
}

func TestEVAChatPackLoadsAllCategories(t *testing.T) {
	engine := testEVAEngine(t)
	for _, cat := range engine.InputCategories() {
		if len(engine.pools[cat]) == 0 {
			t.Errorf("input/%s has no output counterpart", cat)
		}
	}
	for _, pool := range []string{"age_ask.txt", "flirty_questions.txt", "ask_snap.txt", "retry_nudge.txt", "closer.txt", "horny_answer.txt"} {
		if len(engine.pools[pool]) == 0 {
			t.Errorf("bot-initiated pool missing: %s", pool)
		}
	}
	if _, ok := engine.pools["readme.txt"]; ok {
		t.Error("README.txt must not be loaded as a pool")
	}
	order := engine.ScanOrder()
	if len(order) < 3 || order[0] != "share_snap.txt" || order[1] != "snapchat.txt" || order[2] != "horny.txt" {
		t.Fatalf("unexpected scan order: %v", order)
	}
	if len(engine.inputs["greeting.txt"]) < 1000 {
		t.Fatalf("greeting triggers not loaded: %d", len(engine.inputs["greeting.txt"]))
	}
}

func TestEVAChatCommentTriggersIgnored(t *testing.T) {
	m := newEVAMatcher([]string{"# comment", "hello"}, false, nil)
	if m.match("# comment") != "" {
		t.Fatal("comment trigger must not match")
	}
	if m.match("hello there") == "" {
		t.Fatal("real trigger must match")
	}
}

func TestEVAChatBasicFunnel(t *testing.T) {
	c := newEVAConv(t, 1)
	c.expect("hi", "greeting.txt")
	if c.state.Stage != evaStageWaitAgeGender {
		t.Fatalf("stage after greeting: %s", c.state.Stage)
	}
	c.expect("m21", "age_gender.txt")
	if c.state.UserGender != "m" || c.state.UserAge != 21 {
		t.Fatalf("age/gender not captured: %+v", c.state)
	}
	c.expect("from?", "country.txt")
	c.expect("usa", "flirty_questions.txt")
	if c.state.Stage != evaStageMiddle || c.state.UserCountry != "usa" {
		t.Fatalf("stage/country after country answer: %+v", c.state)
	}
}

func TestEVAChatFirstSMSVariants(t *testing.T) {
	newEVAConv(t, 2).expect("m21", "age_gender.txt")
	newEVAConv(t, 3).expect("where u from", "country.txt")
	newEVAConv(t, 4).expect("horny?", "horny_answer.txt")
}

func TestEVAChatOpenerThenFlow(t *testing.T) {
	c := newEVAConv(t, 5)
	opener := c.engine.Start(&c.state, c.rng)
	if opener.SourcePool != "greeting.txt" || opener.Text == "" || c.state.Stage != evaStageWaitAgeGender || c.state.BotReplyCount != 1 {
		t.Fatalf("unexpected opener: %+v state=%+v", opener, c.state)
	}
	c.expect("f 19", "age_gender.txt")
	c.expect("bangladesh", "flirty_questions.txt")
	c.expect("how are u", "how_are_you.txt")
}

func TestEVAChatHornyQuestionTwoLinesThenAskThenShare(t *testing.T) {
	c := newEVAConv(t, 6)
	c.expect("hi", "greeting.txt")
	r := c.expect("u horny?", "horny_answer.txt")
	if len(r.Extra) != 1 || r.Extra[0] == "" {
		t.Fatalf("second horny.txt line not queued: %+v", r)
	}
	if !c.state.AskNext || c.state.LastPool != "horny.txt" {
		t.Fatalf("ask chain not armed: %+v", c.state)
	}
	c.expect("lol what", "ask_snap.txt")
	if !c.state.AskedSnap {
		t.Fatal("asked_snap not set")
	}
	r = c.expect("yeop", "share_snap.txt")
	if !r.HasUsername || !strings.Contains(r.Text, "%username%") || !r.SnapShared || !r.Ended || c.state.Stage != evaStageEnd {
		t.Fatalf("share line wrong: %+v state=%+v", r, c.state)
	}
	c.expect("ok", "closer.txt")
}

func TestEVAChatLeetHorny(t *testing.T) {
	c := newEVAConv(t, 7)
	c.expect("hi", "greeting.txt")
	c.expect("h0rny?", "horny_answer.txt")

	c = newEVAConv(t, 8)
	c.expect("hi", "greeting.txt")
	c.expect("s3x", "horny.txt")
	c.expect("whatever", "ask_snap.txt")
}

func TestEVAChatMiddleCategories(t *testing.T) {
	c := newEVAConv(t, 9)
	c.expect("hi", "greeting.txt")
	c.expect("m21", "age_gender.txt")
	c.expect("usa", "flirty_questions.txt")
	c.expect("qwerty zxcv", "flirty_questions.txt")
	c.expect("how are u", "how_are_you.txt")
	c.expect("how old are u", "your_age.txt")
	c.expect("lol", "middle_chat/warm_reply.txt")

	c = newEVAConv(t, 10)
	c.expect("hi", "greeting.txt")
	c.expect("m21", "age_gender.txt")
	c.expect("usa", "flirty_questions.txt")
	c.expect("gtg bye", "middle_chat/busy_later.txt")
	r := c.expect("gtg", "closer.txt")
	if !r.Ended {
		t.Fatal("second busy must end the chat")
	}

	c = newEVAConv(t, 11)
	c.expect("hi", "greeting.txt")
	c.expect("m21", "age_gender.txt")
	c.expect("usa", "flirty_questions.txt")
	c.expect("ur cute", "middle_chat/flirty_reply.txt")
	c.expect("thanks", "ask_snap.txt")
	c.expect("yes", "share_snap.txt")

	c = newEVAConv(t, 12)
	c.expect("hi", "greeting.txt")
	c.expect("m21", "age_gender.txt")
	c.expect("usa", "flirty_questions.txt")
	c.expect("hola como estas", "english_only.txt")
}

func TestEVAChatDirectSnapAskSharesImmediately(t *testing.T) {
	for _, msg := range []string{"ur sc?", "whats ur snap", "snap?", "u got insta", "ghost?"} {
		c := newEVAConv(t, 13)
		c.expect("hi", "greeting.txt")
		r := c.say(msg)
		if r.SourcePool != "share_snap.txt" || !r.Ended {
			t.Fatalf("direct ask %q -> %s (%s)", msg, r.SourcePool, r.Reason)
		}
	}
	c := newEVAConv(t, 14)
	c.expect("hi", "greeting.txt")
	r := c.say("my snap is here")
	if !evaTerminalCategories[r.SourcePool] || !r.Ended || !r.HasUsername {
		t.Fatalf("user offering their snap must share + END: %+v", r)
	}
}

func TestEVAChatYesWordsWithoutAskDoNotShare(t *testing.T) {
	for _, msg := range []string{"yes", "yeop", "ok", "sure", "bet"} {
		c := newEVAConv(t, 15)
		c.expect("hi", "greeting.txt")
		r := c.say(msg)
		if r.SourcePool == "share_snap.txt" || r.SourcePool == "snapchat.txt" || c.state.Stage == evaStageEnd {
			t.Fatalf("yes-word %q without ask shared: %s", msg, r.SourcePool)
		}
	}
}

func TestEVAChatRefusalNudgeThenCloser(t *testing.T) {
	c := newEVAConv(t, 16)
	c.expect("hi", "greeting.txt")
	c.expect("u horny?", "horny_answer.txt")
	c.expect("hmm", "ask_snap.txt")
	c.expect("no", "retry_nudge.txt")
	c.expect("yes", "share_snap.txt")

	c = newEVAConv(t, 17)
	c.expect("hi", "greeting.txt")
	c.expect("u horny?", "horny_answer.txt")
	c.expect("hmm", "ask_snap.txt")
	c.expect("nope", "retry_nudge.txt")
	r := c.expect("nah", "closer.txt")
	if !r.Ended {
		t.Fatal("double refusal must end")
	}

	c = newEVAConv(t, 18)
	c.expect("hi", "greeting.txt")
	c.expect("u horny?", "horny_answer.txt")
	c.expect("hmm", "ask_snap.txt")
	r = c.say("dont have snap")
	if r.SourcePool == "share_snap.txt" {
		t.Fatal("firm refusal must never share")
	}
}

func TestEVAChatRepeatLoopHornyFlirty(t *testing.T) {
	c := newEVAConv(t, 19)
	c.expect("hi", "greeting.txt")
	c.expect("m21", "age_gender.txt")
	c.expect("usa", "flirty_questions.txt")
	first := c.expect("ur cute", "middle_chat/flirty_reply.txt")
	c.expect("hehe", "ask_snap.txt")
	second := c.expect("love u", "middle_chat/flirty_reply.txt")
	if first.Text == second.Text {
		t.Fatal("repeated flirty_reply used the same line")
	}
	c.expect("hmm", "ask_snap.txt")
	c.expect("ok", "share_snap.txt")
}

func TestEVAChatUnderageNeverShares(t *testing.T) {
	c := newEVAConv(t, 20)
	c.expect("hi", "greeting.txt")
	c.expect("m15", "age_gender.txt")
	if !c.state.Blocked {
		t.Fatal("underage not flagged")
	}
	r := c.say("ur sc?")
	if r.SourcePool != "closer.txt" || !r.Ended || r.HasUsername {
		t.Fatalf("underage share attempt: %+v", r)
	}
}

func TestEVAChatMultiLineUsesLastLine(t *testing.T) {
	c := newEVAConv(t, 21)
	c.expect("hi", "greeting.txt")
	c.expect("some text\nm21", "age_gender.txt")
	if c.state.UserAge != 21 {
		t.Fatalf("age not captured from last line: %+v", c.state)
	}
}

func TestEVAChatReplyCapCloser(t *testing.T) {
	c := newEVAConv(t, 22)
	c.expect("hi", "greeting.txt")
	c.expect("m21", "age_gender.txt")
	c.expect("usa", "flirty_questions.txt")
	ended := false
	for i := 0; i < 12; i++ {
		r := c.say("zzz qqq")
		if r.SourcePool == "closer.txt" && r.Ended {
			ended = true
			break
		}
		if r.SourcePool != "flirty_questions.txt" && r.SourcePool != "ask_snap.txt" {
			t.Fatalf("unexpected pool during cap run: %s", r.SourcePool)
		}
	}
	if !ended || c.state.BotReplyCount > evaMaxReplies+1 {
		t.Fatalf("reply cap not enforced: %+v", c.state)
	}
}

func TestEVAChatAgeAskProgression(t *testing.T) {
	c := newEVAConv(t, 23)
	c.expect("hi", "greeting.txt")
	c.expect("zzz", "age_ask.txt")
	c.expect("qqq", "country.txt")
	if c.state.Stage != evaStageWaitCountry {
		t.Fatalf("2 misses should progress to WAIT_COUNTRY: %+v", c.state)
	}
}

func TestEVAChatNoRepeatPools(t *testing.T) {
	c := newEVAConv(t, 24)
	c.expect("hi", "greeting.txt")
	r := c.say("hi")
	if r.SourcePool == "greeting.txt" {
		t.Fatal("greeting fired twice")
	}

	c = newEVAConv(t, 25)
	c.expect("hi", "greeting.txt")
	c.expect("m21", "age_gender.txt")
	r = c.say("m21")
	if r.SourcePool == "age_gender.txt" {
		t.Fatal("age_gender fired twice")
	}

	c = newEVAConv(t, 26)
	c.expect("hi", "greeting.txt")
	c.expect("m21", "age_gender.txt")
	c.expect("usa", "flirty_questions.txt")
	c.expect("how are u", "how_are_you.txt")
	r = c.say("how are u")
	if r.SourcePool == "how_are_you.txt" {
		t.Fatal("how_are_you fired twice")
	}

	// flirty_questions repeats but never the same line back-to-back
	seen := map[string]bool{}
	for i := 0; i < 6; i++ {
		r = c.say("zzz qqq")
		if r.SourcePool != "flirty_questions.txt" {
			break
		}
		if seen[r.Text] {
			t.Fatalf("flirty_questions repeated a line: %q", r.Text)
		}
		seen[r.Text] = true
	}
}

func TestEVAChatCountriesAndPlaceholders(t *testing.T) {
	engine := testEVAEngine(t)
	if engine.countryFromText("im from bangladesh.") != "bangladesh" {
		t.Fatal("country keyword not detected")
	}
	if engine.countryFromText("south africa") != "south africa" && engine.countryFromText("south africa") == "" {
		t.Fatal("longest country keyword should win")
	}
	state := NewEVAChatSession()
	state.UserCountry = "uk"
	engine.pools["custom_test.txt"] = []string{"{country} is dope, add {snap}"}
	r := engine.pick("custom_test.txt", &state, rand.New(rand.NewSource(1)), "")
	if r.Text != "uk is dope, add %username%" || !r.HasUsername {
		t.Fatalf("placeholder resolution wrong: %+v", r)
	}
}

func TestEVAChatTranscriptMemory(t *testing.T) {
	c := newEVAConv(t, 27)
	c.expect("hi", "greeting.txt")
	c.expect("m21", "age_gender.txt")
	if len(c.state.Transcript) != 4 || c.state.Transcript[0].Who != "user" || c.state.Transcript[1].Who != "bot" || c.state.Transcript[3].N != 4 {
		t.Fatalf("transcript wrong: %+v", c.state.Transcript)
	}
	if !c.state.PoolsUsed["greeting.txt"] || c.state.LastPool != "age_gender.txt" {
		t.Fatalf("pools_used/last_pool wrong: %+v", c.state)
	}
}

func TestEVAChatProactiveAskWhenUserStaysNeutral(t *testing.T) {
	c := newEVAConv(t, 28)
	c.expect("hi", "greeting.txt")
	c.expect("m21", "age_gender.txt")
	c.expect("usa", "flirty_questions.txt")
	asked := false
	for i := 0; i < 5; i++ {
		if c.say("zzz qqq").SourcePool == "ask_snap.txt" {
			asked = true
			break
		}
	}
	if !asked {
		t.Fatal("bot never asked for the snap in neutral middle chat")
	}
	c.expect("yes", "share_snap.txt")
}

func TestEVAChatNoUsernamesClosesInsteadOfSharing(t *testing.T) {
	c := newEVAConv(t, 29)
	c.expect("hi", "greeting.txt")
	r := c.engine.ReplyWithOptions("ur sc?", &c.state, c.rng, EVAReplyOptions{HasUsernames: false})
	if r.SourcePool != "closer.txt" || r.HasUsername || !r.Ended {
		t.Fatalf("without usernames the share must close politely: %+v", r)
	}
}

func TestEVAChatRuntimeAdapterRendersUsername(t *testing.T) {
	engine := testEVAEngine(t)
	cfg := Config{ChatMode: model.ChatModeEVAChat, EVAChat: &EVAChatConfig{Engine: engine}, MessageUsernames: []string{"snapqueen"}}
	state := &model.AccountState{}
	rng := rand.New(rand.NewSource(30))

	opener := evaChatStart(cfg, state, "user-1", rng)
	if opener.Text == "" || !state.EVAChatSessions["user-1"].Started {
		t.Fatalf("adapter did not persist opener: %+v", opener)
	}
	reply := evaChatReply(cfg, state, "user-1", "whats ur snap", rng)
	if reply.SourcePool != "share_snap.txt" || !reply.HasUsername || !strings.Contains(reply.Text, "snapqueen") || strings.Contains(reply.Text, "%username%") {
		t.Fatalf("username not rendered: %+v", reply)
	}
	if !state.EVAChatSessions["user-1"].Ended {
		t.Fatal("session not persisted as ended")
	}
}

func TestEVAChatLoadRejectsMissingRequiredPool(t *testing.T) {
	root := t.TempDir()
	for _, dir := range []string{"input", "output"} {
		if err := os.MkdirAll(filepath.Join(root, dir), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "input", "greeting.txt"), []byte("hi\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadEVAChatEngine(root); err == nil {
		t.Fatal("expected missing required output pool to be rejected")
	}
}

func TestEVAChatCustomCategoryAutoJoins(t *testing.T) {
	root, err := EnsureEVAChatData(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "input", "music.txt"), []byte("fav song?\nmusic?\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "output", "music.txt"), []byte("i love pop, u?|edm all the way\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	engine, err := LoadEVAChatEngine(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(engine.pools["music.txt"]) != 2 {
		t.Fatalf("'|' split not applied: %v", engine.pools["music.txt"])
	}
	c := &evaConv{t: t, engine: engine, rng: rand.New(rand.NewSource(31)), state: NewEVAChatSession()}
	c.expect("hi", "greeting.txt")
	c.expect("m21", "age_gender.txt")
	c.expect("usa", "flirty_questions.txt")
	c.expect("fav song?", "music.txt")
}
