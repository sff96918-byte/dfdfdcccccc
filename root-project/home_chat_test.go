package main

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"chitchatbot/internal/browser"
)

func transcriptSnapshot(conversation string, rows ...browser.DOMMessage) browser.DOMSnapshot {
	return browser.DOMSnapshot{Controls: browser.DOMControls{Conversation: conversation, Phase: "chatting", OwnName: "self", Partner: "partner"}, Messages: rows}
}
func transcriptRow(id, author, text string, order int) browser.DOMMessage {
	return browser.DOMMessage{ID: id, Author: author, Text: text, Order: order}
}

func TestHomeChatActualRowsFullTextIdentityDedupAndOrder(t *testing.T) {
	f := &homeChatFeed{}
	run := f.start("test")
	long := "বাংলা\n🙂 " + strings.Repeat("x", 20000)
	s := transcriptSnapshot("one", transcriptRow("u2", "PARTNER", "same", 3), transcriptRow("u1", "partner", "same", 1), transcriptRow("own", "SELF", long, 2), transcriptRow("unknown", "", "not guessed", 4))
	f.observe(run, s, false)
	first := f.snapshot("")
	f.observe(run, s, false)
	second := f.snapshot("")
	if len(first.Messages) != 3 || first.Messages[0].ID != "u1" || first.Messages[1].Text != long || first.Messages[1].Role != "bot" || first.Messages[2].Role != "user" {
		t.Fatal("incorrect DOM order/identity/full text")
	}
	if second.Version != first.Version || len(second.Messages) != 3 {
		t.Fatal("rerender duplicated rows")
	}
	s.Messages[3].Author = "partner"
	s.Messages[0].Text = "edited"
	f.observe(run, s, false)
	third := f.snapshot("")
	if len(third.Messages) != 4 || third.Messages[2].Text != "edited" {
		t.Fatal("hydration/edit not updated")
	}
}
func TestHomeChatLateFinalReceiptNeverMixesPartners(t *testing.T) {
	f := &homeChatFeed{}
	run := f.start("test")
	f.observe(run, transcriptSnapshot("one", transcriptRow("old-user", "partner", "old", 1)), false)
	old := f.snapshot("").Selected
	f.observe(run, transcriptSnapshot("two", transcriptRow("new-user", "partner", "new", 1)), false)
	latest := f.snapshot("").Selected
	// The final own DOM row is retained in the old session without switching UI.
	f.observe(run, transcriptSnapshot("one", transcriptRow("old-bot", "self", "last actual reply", 2)), true)
	current := f.snapshot("")
	previous := f.snapshot(old)
	if current.Selected != latest || len(current.Messages) != 1 || current.Messages[0].ID != "new-user" {
		t.Fatal("old receipt switched/mixed session")
	}
	if len(previous.Messages) != 2 || previous.Messages[1].Text != "last actual reply" {
		t.Fatal("last reply lost")
	}
	f.observe(run, transcriptSnapshot("one"), false)
	if f.snapshot("").Selected != latest {
		t.Fatal("stale generation reactivated")
	}
}
func TestHomeChatMultipleBrowsersLobbyCloseAndReplay(t *testing.T) {
	f := &homeChatFeed{}
	a, b := f.start("browser 1"), f.start("browser 2")
	f.observe(a, transcriptSnapshot("same", transcriptRow("id", "partner", "first browser", 1)), false)
	first := f.snapshot("").Selected
	f.observe(b, transcriptSnapshot("same", transcriptRow("id", "partner", "second browser", 1)), false)
	if len(f.snapshot("").Sessions) != 2 || f.snapshot(first).Messages[0].Text != "first browser" {
		t.Fatal("browser IDs mixed")
	}
	f.observe(b, browser.DOMSnapshot{Controls: browser.DOMControls{Phase: "lobby"}}, false)
	f.end(a)
	f.end(b)
	last := f.snapshot("")
	if len(last.Messages) != 1 || last.Messages[0].Text != "second browser" {
		t.Fatal("closed transcript discarded")
	}
	for _, s := range last.Sessions {
		if s.Active {
			t.Fatal("closed session active")
		}
	}
}
func TestHomeChatRejectsMissingOrConflictingIdentityAndEmptyDrafts(t *testing.T) {
	for _, which := range []string{"own", "partner", "same", "conversation"} {
		f := &homeChatFeed{}
		run := f.start("test")
		s := transcriptSnapshot("one", transcriptRow("id", "partner", "actual", 1))
		switch which {
		case "own":
			s.Controls.OwnName = ""
		case "partner":
			s.Controls.Partner = ""
		case "same":
			s.Controls.OwnName = "partner"
		case "conversation":
			s.Controls.Conversation = ""
		}
		f.observe(run, s, false)
		if len(f.snapshot("").Messages) != 0 {
			t.Fatal("guessed identity")
		}
	}
	f := &homeChatFeed{}
	run := f.start("test")
	f.observe(run, transcriptSnapshot("one", transcriptRow("", "partner", "no ID", 1), transcriptRow("empty", "self", "", 2)), false)
	if len(f.snapshot("").Messages) != 0 {
		t.Fatal("draft/invalid ID displayed")
	}
}
func TestHomeChatBoundsHistoryWithoutReplayingDiscardedIDs(t *testing.T) {
	f := &homeChatFeed{}
	run := f.start("test")
	s := transcriptSnapshot("one")
	for i := 1; i <= homeChatRows+30; i++ {
		s.Messages = append(s.Messages, transcriptRow(fmt.Sprint(i), "partner", "full message", i))
	}
	f.observe(run, s, false)
	f.observe(run, s, false)
	got := f.snapshot("")
	if len(got.Messages) != homeChatRows || got.Sessions[0].Dropped != 30 || got.Messages[0].Order != 31 {
		t.Fatal("history retention/replay incorrect")
	}
	for i := 0; i < homeChatSessions+5; i++ {
		f.observe(run, transcriptSnapshot(fmt.Sprint("chat-", i), transcriptRow("id", "partner", "message", 1)), false)
	}
	got = f.snapshot("")
	if len(got.Sessions) != homeChatSessions || got.Evicted == 0 {
		t.Fatal("conversation retention unbounded")
	}
}
func TestHomeChatSnapshotCopiesAndConcurrentObservers(t *testing.T) {
	f := &homeChatFeed{}
	run := f.start("test")
	f.observe(run, transcriptSnapshot("one", transcriptRow("id", "partner", "unchanged", 1)), false)
	snap := f.snapshot("")
	snap.Messages[0].Text = "changed by caller"
	snap.Sessions[0].Label = "changed"
	if f.snapshot("").Messages[0].Text != "unchanged" {
		t.Fatal("snapshot aliases internal state")
	}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 30; j++ {
				f.observe(run, transcriptSnapshot("one", transcriptRow("id", "partner", "unchanged", 1)), false)
				f.snapshot("")
			}
		}()
	}
	wg.Wait()
	if len(f.snapshot("").Messages) != 1 {
		t.Fatal("concurrent duplicates")
	}
}

type transcriptDOMFake struct {
	mu                   sync.Mutex
	snapshot             browser.DOMSnapshot
	reads, sends, clicks int
	receipt              bool
	fail                 bool
}

func (d *transcriptDOMFake) ReadDOMSnapshot(context.Context) (browser.DOMSnapshot, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.reads++
	return d.snapshot, nil
}
func (d *transcriptDOMFake) SendDOMMessage(context.Context, string) (bool, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.sends++
	return !d.fail, nil
}
func (d *transcriptDOMFake) ClickDOMButton(context.Context, []string) (string, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.clicks++
	return "", nil
}
func (d *transcriptDOMFake) DOMProbe(context.Context) (string, error) { return "", nil }
func (d *transcriptDOMFake) SendDOMMessageObserved(ctx context.Context, text string, observe func(browser.DOMSnapshot)) (bool, error) {
	sent, err := d.SendDOMMessage(ctx, text)
	if sent && d.receipt {
		observe(transcriptSnapshot("one", transcriptRow("user", "partner", "incoming", 1), transcriptRow("bot", "self", text, 2)))
	}
	return sent, err
}
func TestHomeChatSendHookShowsActualRowsOnlyBeforeSkip(t *testing.T) {
	f := &homeChatFeed{}
	run := f.start("test")
	raw := &transcriptDOMFake{snapshot: transcriptSnapshot("one"), receipt: true}
	d := &homeObservedDOM{DOMChatBackend: raw, feed: f, run: run}
	d.ReadDOMSnapshot(context.Background())
	d.SendDOMMessage(context.Background(), "confirmed")
	if len(f.snapshot("").Messages) != 2 {
		t.Fatal("receipt snapshot lost before final skip")
	}
	raw.fail = true
	d.SendDOMMessage(context.Background(), "not sent")
	for _, row := range f.snapshot("").Messages {
		if row.Text == "not sent" {
			t.Fatal("outgoing intent displayed")
		}
	}
}
func TestHomeChatObserverRunsWithoutReplyAndStopsCleanly(t *testing.T) {
	a := &App{}
	raw := &transcriptDOMFake{snapshot: transcriptSnapshot("one", transcriptRow("id", "partner", "while automation paused", 1))}
	_, stop := a.startHomeChatObserver(context.Background(), raw, "test", 1, 1)
	deadline := time.Now().Add(2 * time.Second)
	for len(a.GetLiveChatSnapshot("").Messages) == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	stop()
	stop()
	got := a.GetLiveChatSnapshot("")
	if len(got.Messages) != 1 {
		t.Fatal("passive observer did not publish paused chat")
	}
	raw.mu.Lock()
	reads := raw.reads
	if raw.sends != 0 || raw.clicks != 0 {
		t.Fatal("observer changed chat")
	}
	raw.mu.Unlock()
	time.Sleep(300 * time.Millisecond)
	raw.mu.Lock()
	defer raw.mu.Unlock()
	if raw.reads != reads {
		t.Fatal("observer leaked after stop")
	}
}

func TestHomeChatDoesNotInventSuccessfulSendWithoutDOMReceipt(t *testing.T) {
	f := &homeChatFeed{}
	run := f.start("test")
	raw := &transcriptDOMFake{snapshot: transcriptSnapshot("one")}
	d := &homeObservedDOM{DOMChatBackend: raw, feed: f, run: run}
	d.ReadDOMSnapshot(context.Background())
	if sent, _ := d.SendDOMMessage(context.Background(), "proposed text"); !sent {
		t.Fatal("fake send result changed")
	}
	if len(f.snapshot("").Messages) != 0 {
		t.Fatal("successful result invented outgoing transcript")
	}
	raw.mu.Lock()
	raw.snapshot = transcriptSnapshot("one", transcriptRow("actual", "self", "actual browser text", 1))
	raw.mu.Unlock()
	d.ReadDOMSnapshot(context.Background())
	if got := f.snapshot("").Messages; len(got) != 1 || got[0].Text != "actual browser text" {
		t.Fatal("actual own row not observed")
	}
}

func TestHomeChatBoundsAcrossMultipleActiveBrowserBatches(t *testing.T) {
	f := &homeChatFeed{}
	var first uint64
	for i := 0; i < homeChatSessions+4; i++ {
		run := f.start(fmt.Sprint(i))
		if i == 0 {
			first = run
		}
		f.observe(run, transcriptSnapshot("one", transcriptRow("id", "partner", "actual", 1)), false)
	}
	got := f.snapshot("")
	if len(got.Sessions) != homeChatSessions || got.Evicted != 4 {
		t.Fatal("active display retention unbounded")
	}
	for i := 0; i < 10; i++ {
		f.observe(first, transcriptSnapshot("one", transcriptRow("id", "partner", "actual", 1)), false)
	}
	after := f.snapshot("")
	if after.Evicted != got.Evicted || after.Version != got.Version {
		t.Fatal("evicted active chat replayed endlessly")
	}
	f.observe(first, transcriptSnapshot("new", transcriptRow("id", "partner", "new conversation", 1)), false)
	if f.snapshot("").Messages[0].Text != "new conversation" {
		t.Fatal("new generation remains suppressed")
	}
}
