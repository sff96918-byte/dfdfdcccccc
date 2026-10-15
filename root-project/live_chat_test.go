package main

import (
	"context"
	"fmt"
	"math/rand"
	"strings"
	"sync"
	"testing"
	"time"

	"chitchatbot/internal/browser"
	"chitchatbot/internal/model"
)

// fakeDOMBackend implements browser.Backend + browser.DOMChatBackend with a
// scripted page. No browser is launched: rows and controls are faked, typing
// is recorded, and sends echo back as own rows just like the real page.
type fakeDOMBackend struct {
	mu          sync.Mutex
	controls    browser.DOMControls
	rows        []browser.DOMMessage
	sent        []string
	clicks      []string
	serial      int
	generation  int
	lastPhase   string
	lastPartner string
	failSend    bool
	sendHook    func()
}

func (f *fakeDOMBackend) Name() string { return "rod" }
func (f *fakeDOMBackend) Start(context.Context, browser.Options) error {
	return nil
}
func (f *fakeDOMBackend) CaptureSession(context.Context) (model.LoginInfo, error) {
	return model.LoginInfo{}, nil
}
func (f *fakeDOMBackend) Fetch(context.Context, browser.FetchRequest) (browser.FetchResponse, error) {
	return browser.FetchResponse{}, nil
}
func (f *fakeDOMBackend) Close() error { return nil }

func (f *fakeDOMBackend) ReadDOMSnapshot(context.Context) (browser.DOMSnapshot, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	c := f.controls
	if c.Phase == "" {
		if c.HasInput && !c.ChatEnded {
			c.Phase = "chatting"
		} else {
			c.Phase = "lobby"
		}
	}
	if c.Partner == "" {
		c.Partner = "partner"
	}
	if c.OwnName == "" {
		c.OwnName = "me"
	}
	if c.Phase != f.lastPhase || c.Partner != f.lastPartner {
		f.generation++
		f.lastPhase = c.Phase
		f.lastPartner = c.Partner
	}
	if c.Conversation == "" {
		c.Conversation = fmt.Sprintf("fake-%d", f.generation)
	}
	c.CanSend = c.Phase == "chatting"
	c.ObserverReady = true
	return browser.DOMSnapshot{Controls: c, Messages: append([]browser.DOMMessage(nil), f.rows...)}, nil
}

func (f *fakeDOMBackend) SendDOMMessage(_ context.Context, text string) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sent = append(f.sent, text)
	if f.sendHook != nil {
		f.sendHook()
	}
	if f.failSend {
		return false, nil
	}
	f.serial++
	f.rows = append(f.rows, browser.DOMMessage{ID: fmt.Sprintf("own-%d", f.serial), Author: "me", Text: text})
	return true, nil
}

func (f *fakeDOMBackend) DOMProbe(context.Context) (string, error) {
	return "fake probe", nil
}

func (f *fakeDOMBackend) ClickDOMButton(_ context.Context, labels []string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, label := range labels {
		for _, b := range f.controls.Buttons {
			if !strings.EqualFold(b, label) {
				continue
			}
			f.clicks = append(f.clicks, label)
			switch strings.ToUpper(label) {
			case "START":
				f.controls.HasInput = true
				f.controls.ChatEnded = false
				f.controls.Buttons = []string{"Skip"}
				f.rows = nil
			case "SKIP":
				f.controls.HasInput = false
				f.controls.Buttons = []string{"START"}
				f.rows = nil
			}
			return label, nil
		}
	}
	return "", nil
}

func (f *fakeDOMBackend) addRow(author, text string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.serial++
	f.rows = append(f.rows, browser.DOMMessage{ID: fmt.Sprintf("row-%d", f.serial), Author: author, Text: text})
}

func (f *fakeDOMBackend) sentCount(text string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, s := range f.sent {
		if s == text {
			n++
		}
	}
	return n
}

func (f *fakeDOMBackend) totalSent() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.sent)
}

func (f *fakeDOMBackend) clickCount(label string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, c := range f.clicks {
		if strings.EqualFold(c, label) {
			n++
		}
	}
	return n
}

func waitForLive(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func testLiveRunner(a *App, fake *fakeDOMBackend) *liveRunner {
	return &liveRunner{
		dom:          fake,
		prefix:       "[test show-live]",
		logf:         func(string, string) {},
		replyDelay:   time.Millisecond,
		pollInterval: 20 * time.Millisecond,
		newResponder: a.newLiveResponder,
	}
}

func TestLiveChatDOMFlow(t *testing.T) {
	a := browserTestApp(t)
	if err := a.SaveMessageSettings(model.NewMessageSettings("hello first\nreply line", "")); err != nil {
		t.Fatal(err)
	}
	fake := &fakeDOMBackend{controls: browser.DOMControls{HasInput: false, Buttons: []string{"START"}}}
	r := testLiveRunner(a, fake)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go r.run(ctx)

	// Lobby: START is clicked automatically, greeting goes out.
	waitForLive(t, "greeting after START", func() bool { return fake.sentCount("hello first") == 1 })

	// Partner message is answered with the next sequence line.
	fake.addRow("partner", "hi")
	waitForLive(t, "reply", func() bool { return fake.sentCount("reply line") == 1 })

	// Our own echoed rows never trigger extra replies.
	time.Sleep(200 * time.Millisecond)
	if n := fake.totalSent(); n != 2 {
		t.Fatalf("own echo caused extra sends (total %d)", n)
	}

	// Sequence exhausted -> Skip is clicked, then a fresh START + greeting.
	fake.addRow("partner", "more")
	waitForLive(t, "skip click", func() bool { return fake.clickCount("Skip") >= 1 })
	waitForLive(t, "second greeting", func() bool { return fake.sentCount("hello first") >= 2 })

	// Partner leaves: bot returns to lobby and matches again.
	fake.mu.Lock()
	fake.controls.ChatEnded = true
	fake.controls.HasInput = false
	fake.controls.Buttons = []string{"START"}
	fake.rows = nil
	fake.mu.Unlock()
	waitForLive(t, "re-match after partner left", func() bool { return fake.clickCount("START") >= 3 })
}

func TestLiveFolderResponderPicksOneSequencePerConversation(t *testing.T) {
	a := browserTestApp(t)
	settings := model.NewMessageSettings("", "")
	settings.ChatMode = model.ChatModeFolder
	settings.LoadedSequences = []string{"greet one\nreply one", "greet two\nreply two"}
	settings.UseLoadedSequences = true
	if err := a.SaveMessageSettings(settings); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 12; i++ {
		responder, mode, err := a.newLiveResponder()
		if err != nil {
			t.Fatal(err)
		}
		if mode != model.ChatModeFolder {
			t.Fatalf("mode = %q", mode)
		}
		start := responder.Start()
		reply, end := responder.Reply("hi")
		if len(start) != 1 || len(reply) != 1 || end {
			t.Fatalf("bad flow: start=%v reply=%v end=%v", start, reply, end)
		}
		if (start[0] == "greet one") != (reply[0] == "reply one") {
			t.Fatalf("mixed sequences: %q then %q", start[0], reply[0])
		}
	}
}

func TestLiveChatEVAResponderStarts(t *testing.T) {
	a := browserTestApp(t)
	settings := model.NewMessageSettings("", "")
	settings.ChatMode = model.ChatModeEVAChat
	if err := a.SaveMessageSettings(settings); err != nil {
		t.Fatal(err)
	}
	responder, mode, err := a.newLiveResponder()
	if err != nil {
		t.Fatalf("EVA responder: %v", err)
	}
	if mode != "EVAChat Flow" {
		t.Fatalf("unexpected mode %q", mode)
	}
	if lines := responder.Start(); len(lines) == 0 {
		t.Fatal("EVA Start should produce a greeting line from the starter pack")
	}
}

func TestShowModeRepeatedTextOwnEchoAndUnknownAuthor(t *testing.T) {
	a := browserTestApp(t)
	if err := a.SaveMessageSettings(model.NewMessageSettings("opener\nreply1\nreply2\nreply3", "")); err != nil {
		t.Fatal(err)
	}
	f := &fakeDOMBackend{controls: browser.DOMControls{HasInput: true, Buttons: []string{"SKIP"}}}
	r := testLiveRunner(a, f)
	ctx := context.Background()
	r.step(ctx)
	f.addRow("partner", "opener") // identical to our greeting, but NOT our echo
	f.addRow("partner", "opener") // identical text, different stable IDs
	f.addRow("", "unclassified")
	r.step(ctx)
	r.step(ctx)
	r.step(ctx)
	if f.sentCount("reply1") != 1 || f.sentCount("reply2") != 1 || f.totalSent() != 3 {
		t.Fatalf("bad queue/echo handling: %v", f.sent)
	}
	// A late author resolution must not lose that unread message.
	f.mu.Lock()
	for i := range f.rows {
		if f.rows[i].Author == "" {
			f.rows[i].Author = "partner"
		}
	}
	f.mu.Unlock()
	r.step(ctx)
	if f.sentCount("reply3") != 1 {
		t.Fatal("late identity message was lost")
	}
}

func TestShowModeUnconfirmedSendPausesWithoutResendOrSkip(t *testing.T) {
	a := browserTestApp(t)
	_ = a.SaveMessageSettings(model.NewMessageSettings("opener\nreply", ""))
	f := &fakeDOMBackend{controls: browser.DOMControls{HasInput: true, Buttons: []string{"SKIP"}}}
	r := testLiveRunner(a, f)
	ctx := context.Background()
	r.step(ctx)
	f.failSend = true
	f.addRow("partner", "hi")
	r.step(ctx)
	for i := 0; i < 4; i++ {
		r.step(ctx)
	}
	if !r.paused || f.sentCount("reply") != 1 || f.clickCount("SKIP") != 0 {
		t.Fatal("uncertain send was retried/skipped")
	}
}

func TestShowModePartnerChangeDuringDelayDiscardsOldQueue(t *testing.T) {
	a := browserTestApp(t)
	_ = a.SaveMessageSettings(model.NewMessageSettings("opener\nreply", ""))
	f := &fakeDOMBackend{controls: browser.DOMControls{HasInput: true, Buttons: []string{"SKIP"}}}
	r := testLiveRunner(a, f)
	r.replyDelay = 40 * time.Millisecond
	ctx := context.Background()
	r.step(ctx)
	f.addRow("partner", "hi")
	done := make(chan struct{})
	go func() {
		defer close(done)
		time.Sleep(5 * time.Millisecond)
		f.mu.Lock()
		f.controls.Partner = "new partner"
		f.rows = nil
		f.mu.Unlock()
	}()
	r.step(ctx)
	<-done
	if f.sentCount("reply") != 0 {
		t.Fatal("old reply leaked to new partner")
	}
	r.step(ctx)
	if f.sentCount("opener") != 2 {
		t.Fatal("new conversation did not open")
	}
}

func TestShowModeUnchangedPageAfterSkipDoesNotRegreet(t *testing.T) {
	a := browserTestApp(t)
	_ = a.SaveMessageSettings(model.NewMessageSettings("opener", ""))
	f := &fakeDOMBackend{controls: browser.DOMControls{HasInput: true, Buttons: []string{"NO ACTION"}}}
	r := testLiveRunner(a, f)
	ctx := context.Background()
	r.step(ctx)
	f.addRow("partner", "next")
	r.step(ctx)
	for i := 0; i < 4; i++ {
		r.step(ctx)
	}
	if f.sentCount("opener") != 1 || !r.rotating {
		t.Fatal("old page was greeted again after failed skip")
	}
}

func TestShowModeResetDuringFinalSendNeverSkipsNewConversation(t *testing.T) {
	a := browserTestApp(t)
	_ = a.SaveMessageSettings(model.NewMessageSettings("opener\nreply", ""))
	f := &fakeDOMBackend{controls: browser.DOMControls{HasInput: true, Buttons: []string{"SKIP"}}}
	r := testLiveRunner(a, f)
	ctx := context.Background()
	r.step(ctx)
	// Custom responder ends with a two-line reply; simulate a rematch after line 1.
	r.responder = endingLiveTestResponder{}
	f.sendHook = func() { f.controls.Partner = "new partner"; f.rows = nil }
	f.addRow("partner", "go")
	r.step(ctx)
	if r.rotating || f.clickCount("SKIP") != 0 || f.sentCount("second") != 0 {
		t.Fatal("stale reply/skip crossed conversation boundary")
	}
}

type endingLiveTestResponder struct{}

func (endingLiveTestResponder) Start() []string { return nil }
func (endingLiveTestResponder) Reply(string) ([]string, bool) {
	return []string{"first", "second"}, true
}

func TestShowModeLiveUsernameRenderingAndNoUsernameGate(t *testing.T) {
	a := browserTestApp(t)
	settings := model.NewMessageSettings("hello\nadd %username%", "test-handle")
	_ = a.SaveMessageSettings(settings)
	responder, _, err := a.newLiveResponder()
	if err != nil {
		t.Fatal(err)
	}
	responder.Start()
	lines, _ := responder.Reply("hi")
	if len(lines) != 1 || lines[0] != "add test-handle" {
		t.Fatalf("unrendered handle: %v", lines)
	}
	settings.Usernames = ""
	_ = a.SaveMessageSettings(settings)
	if _, _, err := a.newLiveResponder(); err == nil {
		t.Fatal("sequence without required username accepted")
	}
	settings.ChatMode = model.ChatModeEVAChat
	_ = a.SaveMessageSettings(settings)
	responder, _, err = a.newLiveResponder()
	if err != nil {
		t.Fatal(err)
	}
	responder.Start()
	lines, end := responder.Reply("whats ur snap")
	if !end || strings.Contains(strings.Join(lines, " "), "%username%") {
		t.Fatal("EVA bypassed no-username gate")
	}
}

// recordingLiveResponder is a test-only responder: it records whether the
// opener was requested and which incoming messages the runner answered, so
// first-message behaviour can be asserted without reading runner internals.
type recordingLiveResponder struct {
	mu       sync.Mutex
	starts   int
	incoming []string
}

func (r *recordingLiveResponder) Start() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.starts++
	return []string{"opener"}
}

func (r *recordingLiveResponder) Reply(text string) ([]string, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.incoming = append(r.incoming, text)
	return []string{"ok reply"}, false
}

func (r *recordingLiveResponder) snapshot() (int, []string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.starts, append([]string(nil), r.incoming...)
}

// recordingLog captures the runner's ACTIVITY lines so tests can wait for the
// conversation to start instead of guessing with sleeps.
type recordingLog struct {
	mu    sync.Mutex
	lines []string
}

func (l *recordingLog) log(_ string, message string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.lines = append(l.lines, message)
}

func (l *recordingLog) contains(text string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, line := range l.lines {
		if strings.Contains(line, text) {
			return true
		}
	}
	return false
}

func (l *recordingLog) count(text string) int {
	l.mu.Lock()
	defer l.mu.Unlock()
	n := 0
	for _, line := range l.lines {
		if strings.Contains(line, text) {
			n++
		}
	}
	return n
}

func TestFirstMessageModeDefaultsAndNormalization(t *testing.T) {
	if got := model.DefaultGeneralSettings().EffectiveFirstMessageMode(); got != model.FirstMessageBot {
		t.Fatalf("default first message mode = %q, want %q", got, model.FirstMessageBot)
	}
	for _, mode := range []string{model.FirstMessageBot, model.FirstMessageUser, model.FirstMessageRandom} {
		if got := (model.GeneralSettings{FirstMessageMode: mode}).EffectiveFirstMessageMode(); got != mode {
			t.Fatalf("mode %q read back as %q", mode, got)
		}
	}
	// Settings written by older versions (no field) and unknown values keep the
	// original bot-writes-first behaviour.
	for _, mode := range []string{"", "USER", "Random ", "garbage"} {
		if got := (model.GeneralSettings{FirstMessageMode: mode}).EffectiveFirstMessageMode(); got != model.FirstMessageBot {
			t.Fatalf("unknown mode %q read back as %q, want %q", mode, got, model.FirstMessageBot)
		}
	}
}

func TestLiveFirstMessageModeReadsSavedSetting(t *testing.T) {
	a := browserTestApp(t)
	if got := a.liveFirstMessageMode(); got != model.FirstMessageBot {
		t.Fatalf("default live mode = %q, want %q", got, model.FirstMessageBot)
	}
	for _, mode := range []string{model.FirstMessageUser, model.FirstMessageRandom, model.FirstMessageBot} {
		if err := a.uiStore.SaveGeneralSettings(model.GeneralSettings{FirstMessageMode: mode}); err != nil {
			t.Fatal(err)
		}
		if got := a.liveFirstMessageMode(); got != mode {
			t.Fatalf("saved %q read back as %q", mode, got)
		}
	}
	if err := a.uiStore.SaveGeneralSettings(model.GeneralSettings{FirstMessageMode: "garbage"}); err != nil {
		t.Fatal(err)
	}
	if got := a.liveFirstMessageMode(); got != model.FirstMessageBot {
		t.Fatalf("unknown saved mode read back as %q", got)
	}
}

func TestShowModeUserFirstWaitsForPartnerAndRepliesOnce(t *testing.T) {
	a := browserTestApp(t)
	rec := &recordingLiveResponder{}
	log := &recordingLog{}
	fake := &fakeDOMBackend{controls: browser.DOMControls{HasInput: true}}
	r := testLiveRunner(a, fake)
	r.logf = log.log
	r.firstMessageMode = func() string { return model.FirstMessageUser }
	r.newResponder = func() (liveResponder, string, error) { return rec, "test", nil }
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go r.run(ctx)

	waitForLive(t, "chat start without opener", func() bool { return log.contains("first=user") })
	if !log.contains("WAITING FIRST SMS") {
		t.Fatal("user-writes-first mode did not report waiting for the first message")
	}
	if starts, incoming := rec.snapshot(); starts != 0 || len(incoming) != 0 {
		t.Fatalf("bot opened the chat: starts=%d incoming=%v", starts, incoming)
	}
	if n := fake.totalSent(); n != 0 {
		t.Fatalf("user-writes-first mode sent %d message(s) before the partner wrote", n)
	}

	fake.addRow("partner", "hi there")
	waitForLive(t, "reply to the partner", func() bool { return fake.sentCount("ok reply") == 1 })
	time.Sleep(200 * time.Millisecond)
	if starts, incoming := rec.snapshot(); starts != 0 || len(incoming) != 1 || incoming[0] != "hi there" {
		t.Fatalf("unexpected reply state: starts=%d incoming=%v", starts, incoming)
	}
	if n := fake.totalSent(); n != 1 {
		t.Fatalf("expected exactly one reply, got %d", n)
	}
}

func TestShowModeUserFirstAnswersOnlyNewestPartnerRow(t *testing.T) {
	a := browserTestApp(t)
	rec := &recordingLiveResponder{}
	fake := &fakeDOMBackend{controls: browser.DOMControls{HasInput: true}}
	fake.addRow("partner", "old message")
	fake.addRow("partner", "newest message")
	r := testLiveRunner(a, fake)
	r.firstMessageMode = func() string { return model.FirstMessageUser }
	r.newResponder = func() (liveResponder, string, error) { return rec, "test", nil }
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go r.run(ctx)

	waitForLive(t, "reply to the newest row", func() bool { return fake.sentCount("ok reply") == 1 })
	time.Sleep(200 * time.Millisecond)
	if _, incoming := rec.snapshot(); len(incoming) != 1 || incoming[0] != "newest message" {
		t.Fatalf("answered %v; want only the newest partner row", incoming)
	}
	if n := fake.totalSent(); n != 1 {
		t.Fatalf("expected exactly one reply, got %d", n)
	}
}

func TestShowModeUserFirstDoesNotRepeatAnAnsweredRow(t *testing.T) {
	a := browserTestApp(t)
	rec := &recordingLiveResponder{}
	log := &recordingLog{}
	fake := &fakeDOMBackend{controls: browser.DOMControls{HasInput: true}}
	fake.addRow("partner", "already handled")
	fake.addRow("me", "my earlier reply")
	r := testLiveRunner(a, fake)
	r.logf = log.log
	r.firstMessageMode = func() string { return model.FirstMessageUser }
	r.newResponder = func() (liveResponder, string, error) { return rec, "test", nil }
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go r.run(ctx)

	waitForLive(t, "conversation with an answered row", func() bool { return log.contains("WAITING NEXT SMS") })
	if log.contains("WAITING FIRST SMS") {
		t.Fatal("log claims the partner never wrote even though an answered partner row exists")
	}
	time.Sleep(150 * time.Millisecond)
	if _, incoming := rec.snapshot(); len(incoming) != 0 {
		t.Fatalf("answered an already handled row: %v", incoming)
	}
	if n := fake.totalSent(); n != 0 {
		t.Fatalf("replied %d time(s) to an already answered conversation", n)
	}
}

func TestShowModeDefaultModeKeepsBotOpener(t *testing.T) {
	a := browserTestApp(t)
	rec := &recordingLiveResponder{}
	log := &recordingLog{}
	fake := &fakeDOMBackend{controls: browser.DOMControls{HasInput: true}}
	r := testLiveRunner(a, fake) // firstMessageMode nil = original behaviour
	r.logf = log.log
	r.newResponder = func() (liveResponder, string, error) { return rec, "test", nil }
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go r.run(ctx)

	waitForLive(t, "opener sent without the setting", func() bool { return fake.sentCount("opener") == 1 })
	if !log.contains("first=bot") {
		t.Fatal("default mode must keep the bot-writes-first behaviour")
	}
	time.Sleep(200 * time.Millisecond)
	if starts, _ := rec.snapshot(); starts != 1 {
		t.Fatalf("opener requested %d time(s), want 1", starts)
	}
	if n := fake.totalSent(); n != 1 {
		t.Fatalf("expected exactly one opener, got %d", n)
	}
}

func TestShowModeUserFirstLogsFirstSMSAnsweredOnce(t *testing.T) {
	a := browserTestApp(t)
	rec := &recordingLiveResponder{}
	log := &recordingLog{}
	fake := &fakeDOMBackend{controls: browser.DOMControls{HasInput: true}}
	r := testLiveRunner(a, fake)
	r.logf = log.log
	r.firstMessageMode = func() string { return model.FirstMessageUser }
	r.newResponder = func() (liveResponder, string, error) { return rec, "test", nil }
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go r.run(ctx)

	waitForLive(t, "waiting for the first partner message", func() bool { return log.contains("WAITING FIRST SMS") })
	fake.addRow("partner", "hello first")
	waitForLive(t, "first reply confirmed", func() bool { return fake.sentCount("ok reply") == 1 })
	waitForLive(t, "first sms log", func() bool { return log.count("FIRST SMS ANSWERED") == 1 })

	// A later partner message is an ordinary reply and must not re-log the first SMS.
	fake.addRow("partner", "second message")
	waitForLive(t, "second reply confirmed", func() bool { return fake.sentCount("ok reply") == 2 })
	time.Sleep(150 * time.Millisecond)
	if n := log.count("FIRST SMS ANSWERED"); n != 1 {
		t.Fatalf("FIRST SMS ANSWERED logged %d time(s), want exactly 1", n)
	}
	if n := fake.totalSent(); n != 2 {
		t.Fatalf("expected exactly two replies, got %d", n)
	}
}

// The in-page helper reports rows in hydration order, which is not guaranteed to
// match DOM order; the row order the page reports must win.
func TestNewestPartnerRowIDPrefersReportedOrder(t *testing.T) {
	c := browser.DOMControls{Partner: "partner", OwnName: "me"}
	newest := []browser.DOMMessage{
		{ID: "row-new", Author: "partner", Text: "new", Order: 5},
		{ID: "row-old", Author: "partner", Text: "old", Order: 2},
	}
	if got := newestPartnerRowID(newest, c); got != "row-new" {
		t.Fatalf("ordered snapshot picked %q, want row-new", got)
	}
	withOwnAfter := append([]browser.DOMMessage(nil), newest...)
	withOwnAfter = append(withOwnAfter, browser.DOMMessage{ID: "own", Author: "me", Text: "mine", Order: 6})
	if got := newestPartnerRowID(withOwnAfter, c); got != "" {
		t.Fatalf("an own row after the newest partner row must not be answered, got %q", got)
	}
	// Unknown/system rows never win, and an ordered snapshot still answers the
	// newest partner row when older own rows exist.
	withOlderOwn := append([]browser.DOMMessage(nil), newest...)
	withOlderOwn = append(withOlderOwn, browser.DOMMessage{ID: "own-old", Author: "me", Text: "mine", Order: 1})
	if got := newestPartnerRowID(withOlderOwn, c); got != "row-new" {
		t.Fatalf("older own row changed the answer: %q", got)
	}
	// Backends without an order keep the original slice-order behaviour.
	plain := []browser.DOMMessage{
		{ID: "p1", Author: "partner", Text: "one"},
		{ID: "p2", Author: "partner", Text: "two"},
	}
	if got := newestPartnerRowID(plain, c); got != "p2" {
		t.Fatalf("orderless snapshot picked %q, want p2", got)
	}
	plainOwnLast := append([]browser.DOMMessage(nil), plain...)
	plainOwnLast = append(plainOwnLast, browser.DOMMessage{ID: "own", Author: "me", Text: "mine"})
	if got := newestPartnerRowID(plainOwnLast, c); got != "" {
		t.Fatalf("orderless snapshot with a newer own row must not answer, got %q", got)
	}
}

func TestShowModeRandomModeDecidesPerConversation(t *testing.T) {
	random := &liveRunner{firstMessageMode: func() string { return model.FirstMessageRandom }, rng: rand.New(rand.NewSource(7))}
	bot, user := 0, 0
	for i := 0; i < 40; i++ {
		if random.botFirstForNewConversation() {
			bot++
		} else {
			user++
		}
	}
	if bot == 0 || user == 0 {
		t.Fatalf("random decisions were one-sided: bot=%d user=%d", bot, user)
	}
	// A missing rng or an unknown saved value never disables the opener.
	noRng := &liveRunner{firstMessageMode: func() string { return model.FirstMessageRandom }}
	if !noRng.botFirstForNewConversation() {
		t.Fatal("random without rng must keep the bot opener")
	}
	for _, mode := range []string{"", "garbage"} {
		unknown := &liveRunner{firstMessageMode: func() string { return mode }}
		if !unknown.botFirstForNewConversation() {
			t.Fatalf("mode %q must keep the bot opener", mode)
		}
	}
	explicitUser := &liveRunner{firstMessageMode: func() string { return model.FirstMessageUser }}
	if explicitUser.botFirstForNewConversation() {
		t.Fatal("user-writes-first mode must not send an opener")
	}
}

func TestSequenceResponderUserFirstStartsFromFirstLine(t *testing.T) {
	responder := &sequenceLiveResponder{sequences: [][]string{{"greet one", "reply one"}}, rng: rand.New(rand.NewSource(1))}
	lines, end := responder.Reply("hi")
	if len(lines) != 1 || lines[0] != "greet one" || end {
		t.Fatalf("first reply without Start() = %v end=%t; want [greet one]", lines, end)
	}
	lines, end = responder.Reply("ok")
	if len(lines) != 1 || lines[0] != "reply one" || end {
		t.Fatalf("second reply = %v end=%t; want [reply one]", lines, end)
	}
	lines, end = responder.Reply("still there?")
	if len(lines) != 0 || !end {
		t.Fatalf("exhausted sequence = %v end=%t; want end", lines, end)
	}
}
