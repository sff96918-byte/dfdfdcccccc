package main

// Show Mode live chat, DOM-only edition. After the user logs in manually in
// the visible Rod browser, chatting continues inside that same browser using
// only what a human sees: message rows are read from the DOM, replies are
// typed into the message box, and Skip/START are real button clicks. No
// WebSocket frames and no API calls are used (explicit user requirement).
// Normal Rod/ChromeDP login capture and the Start-Bot worker are untouched;
// this runs only when BrowserSettings.LiveChat is set (Show Mode).

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"strings"
	"time"

	"chitchatbot/internal/bot"
	"chitchatbot/internal/browser"
	"chitchatbot/internal/model"
)

// ---------------------------------------------------------------------------
// Reply responders (unchanged semantics). EVA mode reuses the exported
// EVAChat engine; Folder mode picks ONE sequence per conversation, like the
// worker bot; Single mode walks its lines.
// ---------------------------------------------------------------------------

type liveResponder interface {
	Start() []string
	Reply(incoming string) (lines []string, end bool)
}

type detectOnlyResponder struct{}

func (detectOnlyResponder) Start() []string { return nil }
func (detectOnlyResponder) Reply(string) ([]string, bool) {
	return nil, false
}

type sequenceLiveResponder struct {
	sequences [][]string
	lines     []string
	next      int
	rng       *rand.Rand
}

func (r *sequenceLiveResponder) Start() []string {
	usable := r.sequences[:0]
	for _, seq := range r.sequences {
		if len(seq) > 0 {
			usable = append(usable, seq)
		}
	}
	if len(usable) == 0 {
		return nil
	}
	pick := 0
	if len(usable) > 1 && r.rng != nil {
		pick = r.rng.Intn(len(usable))
	}
	r.lines = usable[pick]
	r.next = 1
	return []string{r.lines[0]}
}

func (r *sequenceLiveResponder) Reply(string) ([]string, bool) {
	if r.next < len(r.lines) {
		line := r.lines[r.next]
		r.next++
		return []string{line}, false
	}
	return nil, true
}

type evaLiveResponder struct {
	engine   *bot.EVAChatEngine
	state    *model.EVAChatSession
	rng      *rand.Rand
	username string
}

func evaLines(result bot.EVAReplyResult) []string {
	lines := []string{}
	if strings.TrimSpace(result.Text) != "" {
		lines = append(lines, result.Text)
	}
	for _, extra := range result.Extra {
		if strings.TrimSpace(extra) != "" {
			lines = append(lines, extra)
		}
	}
	return lines
}

func (r *evaLiveResponder) Start() []string {
	return r.render(r.engine.Start(r.state, r.rng))
}

func (r *evaLiveResponder) Reply(incoming string) ([]string, bool) {
	result := r.engine.ReplyWithOptions(incoming, r.state, r.rng, bot.EVAReplyOptions{HasUsernames: r.username != ""})
	return r.render(result), result.Ended
}

func (r *evaLiveResponder) render(result bot.EVAReplyResult) []string {
	lines := evaLines(result)
	for i, line := range lines {
		lines[i] = strings.NewReplacer("%username%", r.username, "{snap}", r.username).Replace(line)
	}
	return lines
}

func liveSequences(settings model.MessageSettings) [][]string {
	if settings.EffectiveChatMode() == model.ChatModeFolder && len(settings.LoadedSequences) > 0 {
		sequences := make([][]string, 0, len(settings.LoadedSequences))
		for _, seq := range settings.LoadedSequences {
			sequences = append(sequences, model.MessageSequenceLines(seq))
		}
		return sequences
	}
	return [][]string{settings.SequenceLines()}
}

func (a *App) newLiveResponder() (liveResponder, string, error) {
	settings, err := a.uiStore.LoadMessageSettings()
	if err != nil {
		return nil, "", err
	}
	mode := settings.EffectiveChatMode()
	rng := rand.New(rand.NewSource(time.Now().UnixNano()))
	username := ""
	if handles := settings.UsernameLines(); len(handles) > 0 {
		username = handles[rng.Intn(len(handles))]
	}
	if mode == model.ChatModeEVAChat {
		dataRoot, err := bot.EnsureEVAChatData(a.dataDir)
		if err != nil {
			return nil, "", err
		}
		engine, err := bot.LoadEVAChatEngine(dataRoot)
		if err != nil {
			return nil, "", err
		}
		session := bot.NewEVAChatSession()
		return &evaLiveResponder{
			engine:   engine,
			state:    &session,
			rng:      rng,
			username: username,
		}, "EVAChat Flow", nil
	}
	sequences := liveSequences(settings)
	for i, seq := range sequences {
		for j, line := range seq {
			if username == "" && (strings.Contains(line, "%username%") || strings.Contains(line, "{snap}")) {
				return nil, "", fmt.Errorf("Show Mode sequence needs a Message → Usernames entry")
			}
			sequences[i][j] = strings.NewReplacer("%username%", username, "{snap}", username).Replace(line)
		}
	}
	return &sequenceLiveResponder{sequences: sequences, rng: rng}, mode, nil
}

// DOM runner: single-consumer queue over the observer's retained rows. Unknown
// authors are not guessed; uncertain sends pause until a different conversation.
type liveRunner struct {
	dom           browser.DOMChatBackend
	prefix        string
	logf          func(string, string)
	newResponder  func() (liveResponder, string, error)
	replyDelay    time.Duration
	pollInterval  time.Duration
	conversation  string
	controls      browser.DOMControls
	responder     liveResponder
	seenIDs       map[string]bool
	queuedIDs     map[string]bool
	queue         []browser.DOMMessage
	paused        bool
	rotating      bool
	lastStartAt   time.Time
	lastRotateAt  time.Time
	lastStepError string
	probeLogged   bool
}

func (a *App) runLiveChat(ctx context.Context, backend browser.Backend, _ model.LoginInfo, profileName string, index, total int) {
	dom, ok := backend.(browser.DOMChatBackend)
	if !ok {
		a.log("activity", "SHOW MODE ERROR | backend does not support DOM chat")
		return
	}
	observed, stopTranscript := a.startHomeChatObserver(ctx, dom, profileName, index, total)
	defer stopTranscript()
	r := &liveRunner{dom: observed, prefix: fmt.Sprintf("[%s show-live %d/%d]", profileName, index, total),
		logf: a.log, newResponder: a.newLiveResponder, replyDelay: 900 * time.Millisecond, pollInterval: 350 * time.Millisecond}
	a.log("activity", r.prefix+" DOM READY | browser-only chat watcher started; close its browser window to stop")
	r.run(ctx)
	a.log("activity", r.prefix+" LIVE CHAT stopped")
}

func (r *liveRunner) run(ctx context.Context) {
	ticker := time.NewTicker(r.pollInterval)
	defer ticker.Stop()
	for {
		if ctx.Err() != nil || r.step(ctx) {
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (r *liveRunner) reset(c browser.DOMControls) {
	r.conversation = c.Conversation
	r.controls = c
	r.responder = nil
	r.seenIDs = make(map[string]bool)
	r.queuedIDs = make(map[string]bool)
	r.queue = nil
	r.paused = false
	r.rotating = false
	r.probeLogged = false
	r.lastStepError = ""
	r.lastRotateAt = time.Time{}
	r.lastStartAt = time.Time{}
}

func (r *liveRunner) step(ctx context.Context) bool {
	snapshot, err := r.dom.ReadDOMSnapshot(ctx)
	if err != nil {
		if r.pageGone(err) {
			return true
		}
		r.logStepError("DOM READ FAILED | " + err.Error())
		return false
	}
	c := snapshot.Controls
	if c.Conversation == "" {
		r.logStepError("DOM NOT READY | missing conversation identity")
		return false
	}
	if c.Conversation != r.conversation {
		r.reset(c)
	}
	r.controls = c
	if c.Overflow {
		r.paused = true
		r.logStepError("QUEUE FULL | paused safely; start a new chat")
		return false
	}
	if c.Phase != "chatting" {
		r.responder = nil
		r.queue = nil
		if hasDOMButton(c.Buttons, "START") && time.Since(r.lastStartAt) > 6*time.Second {
			r.lastStartAt = time.Now()
			clicked, err := r.dom.ClickDOMButton(ctx, []string{"START"})
			if err != nil {
				if r.pageGone(err) {
					return true
				}
				r.logStepError("START FAILED | " + err.Error())
			} else if clicked != "" {
				r.logf("activity", r.prefix+" START clicked | waiting for a new conversation")
			}
		} else if !r.probeLogged {
			r.probeLogged = true
			if probe, err := r.dom.DOMProbe(ctx); err == nil {
				r.logf("activity", r.prefix+" PAGE PROBE | "+probe)
			}
		}
		return false
	}
	if r.rotating {
		return r.rotate(ctx)
	}
	if r.paused {
		return false
	}
	if !c.CanSend || c.Partner == "" || c.OwnName == "" || strings.EqualFold(c.Partner, c.OwnName) {
		r.logStepError("DOM WAIT | composer or own/partner identity not ready; no guessed replies")
		return false
	}
	if r.responder == nil {
		// Only the first snapshot is history. Rows arriving during opener/send remain
		// in the browser queue and are processed on the next cycle.
		for _, row := range snapshot.Messages {
			if row.ID != "" {
				r.seenIDs[row.ID] = true
			}
		}
		responder, mode, err := r.newResponder()
		if err != nil {
			r.paused = true
			r.logStepError("REPLY ENGINE FAILED | " + err.Error())
			return false
		}
		r.responder = responder
		r.logf("activity", fmt.Sprintf("%s CHAT STARTED | mode=%s history=%d observer=%t", r.prefix, mode, len(r.seenIDs), c.ObserverReady))
		return r.sendLines(ctx, responder.Start())
	}
	for _, row := range snapshot.Messages {
		if row.ID == "" || r.seenIDs[row.ID] || r.queuedIDs[row.ID] || strings.TrimSpace(row.Text) == "" {
			continue
		}
		if strings.EqualFold(row.Author, c.OwnName) {
			r.seenIDs[row.ID] = true
			continue
		}
		if !strings.EqualFold(row.Author, c.Partner) {
			r.logStepError("AUTHOR UNKNOWN | unread row retained; no text-only echo guessing")
			continue
		}
		r.queue = append(r.queue, row)
		r.queuedIDs[row.ID] = true
	}
	if len(r.queue) == 0 {
		return false
	}
	m := r.queue[0]
	r.logf("activity", fmt.Sprintf("%s INCOMING | partner row accepted; pending=%d", r.prefix, len(r.queue)))
	if sleepLive(ctx, r.replyDelay) {
		return true
	}
	// Do not let a slow reply or an old batch leak into the next partner's chat.
	if !r.stillCurrent(ctx) {
		return ctx.Err() != nil
	}
	generation := r.conversation
	lines, end := r.responder.Reply(m.Text)
	r.seenIDs[m.ID] = true
	delete(r.queuedIDs, m.ID)
	r.queue = r.queue[1:]
	if r.sendLines(ctx, lines) {
		return true
	}
	if r.conversation != generation || r.responder == nil {
		return false
	}
	if end && !r.paused {
		r.queue = nil
		r.rotating = true
		return r.rotate(ctx)
	}
	return false
}

func (r *liveRunner) stillCurrent(ctx context.Context) bool {
	s, err := r.dom.ReadDOMSnapshot(ctx)
	if err != nil {
		r.paused = true
		r.logStepError("DOM RECHECK FAILED | " + err.Error())
		return false
	}
	if s.Controls.Conversation != r.conversation || s.Controls.Phase != "chatting" {
		r.reset(s.Controls)
		return false
	}
	if !s.Controls.CanSend || s.Controls.Overflow {
		r.paused = true
		r.logStepError("SEND PAUSED | composer is not ready; inspect the page")
		return false
	}
	return true
}

func (r *liveRunner) sendLines(ctx context.Context, lines []string) bool {
	for i, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if i > 0 && sleepLive(ctx, 400*time.Millisecond) {
			return true
		}
		if !r.stillCurrent(ctx) {
			return ctx.Err() != nil
		}
		sent, err := r.dom.SendDOMMessage(ctx, line)
		if err != nil && r.pageGone(err) {
			return true
		}
		if err != nil || !sent {
			r.paused = true
			r.logStepError("SEND UNCONFIRMED | automation paused; inspect the page before starting a new chat (no automatic resend)")
			return false
		}
		r.logf("activity", r.prefix+" SEND CONFIRMED | new own message row observed")
	}
	return false
}

func (r *liveRunner) rotate(ctx context.Context) bool {
	if time.Since(r.lastRotateAt) < 2*time.Second {
		return false
	}
	r.lastRotateAt = time.Now()
	s, err := r.dom.ReadDOMSnapshot(ctx)
	if err != nil {
		if r.pageGone(err) {
			return true
		}
		r.logStepError("SKIP CHECK FAILED | " + err.Error())
		return false
	}
	if s.Controls.Conversation != r.conversation {
		r.reset(s.Controls)
		return false
	}
	// Exact active aria-label only; hidden START/STOP labels never participate.
	clicked, err := r.dom.ClickDOMButton(ctx, []string{"SKIP", "CONFIRM?", "START"})
	if err != nil {
		if r.pageGone(err) {
			return true
		}
		r.logStepError("SKIP FAILED | " + err.Error())
	} else if clicked == "" {
		r.logStepError("SKIP WAIT | active cycle button unavailable; use the page manually")
	} else {
		r.logf("activity", r.prefix+" SKIP | clicked active "+clicked+"; awaiting conversation change")
		r.lastStartAt = time.Time{}
	}
	// Do not greet the old page again just because click() returned successfully.
	return false
}

func (r *liveRunner) pageGone(err error) bool {
	return errors.Is(err, browser.ErrPageUnavailable) || errors.Is(err, context.Canceled)
}
func (r *liveRunner) logStepError(message string) {
	if message == r.lastStepError {
		return
	}
	r.lastStepError = message
	r.logf("activity", r.prefix+" "+message)
}
func hasDOMButton(buttons []string, label string) bool {
	for _, b := range buttons {
		if strings.EqualFold(strings.TrimSpace(b), label) {
			return true
		}
	}
	return false
}
func sleepLive(ctx context.Context, d time.Duration) bool {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return true
	case <-timer.C:
		return false
	}
}
