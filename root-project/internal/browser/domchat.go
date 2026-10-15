package browser

// Show Mode only: visible page DOM + native browser input. The normal Backend,
// Rod/ChromeDP login capture, proxy setup and worker transports are unchanged.
import (
	"context"
	_ "embed"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/cdp"
	"github.com/go-rod/rod/lib/input"
)

type DOMMessage struct {
	ID     string `json:"id"`
	Order  int    `json:"order"`
	Author string `json:"author"`
	Text   string `json:"text"`
}

type DOMControls struct {
	HasInput      bool     `json:"hasInput"`
	InputDesc     string   `json:"inputDesc"`
	Buttons       []string `json:"buttons"`
	ChatEnded     bool     `json:"chatEnded"`
	Partner       string   `json:"partner"`
	OwnName       string   `json:"ownName"`
	Conversation  string   `json:"conversation"`
	Phase         string   `json:"phase"`
	CanSend       bool     `json:"canSend"`
	ObserverReady bool     `json:"observerReady"`
	Overflow      bool     `json:"overflow"`
}

type DOMSnapshot struct {
	Controls DOMControls  `json:"controls"`
	Messages []DOMMessage `json:"messages"`
	Revision int          `json:"revision"`
}

// Optional Show Mode surface only; the shared login Backend is not modified.
type DOMChatBackend interface {
	ReadDOMSnapshot(context.Context) (DOMSnapshot, error)
	SendDOMMessage(context.Context, string) (bool, error)
	ClickDOMButton(context.Context, []string) (string, error)
	DOMProbe(context.Context) (string, error)
}

// Embedded verbatim so browser-free tests execute the actual production JS.
//
//go:embed showmode_dom.js
var domLibJS string

func (b *RodBackend) evalDOM(ctx context.Context, expression string, args []any, out any) error {
	return b.withPageRetry(ctx, func(page *rod.Page) error {
		res, err := page.Eval(expression, args...)
		// A reload can destroy only the JS context while the same page is alive.
		// Retry this read-only evaluation on that page, never relaunch a browser.
		for attempt := 0; attempt < 2 && (errors.Is(err, cdp.ErrCtxDestroyed) || errors.Is(err, cdp.ErrCtxNotFound)); attempt++ {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(150 * time.Millisecond):
			}
			res, err = page.Eval(expression, args...)
		}
		if err != nil {
			return err
		}
		if out == nil {
			return nil
		}
		return res.Value.Unmarshal(out)
	})
}

func (b *RodBackend) ReadDOMSnapshot(ctx context.Context) (DOMSnapshot, error) {
	var snapshot DOMSnapshot
	err := b.evalDOM(ctx, "() => { "+domLibJS+" return __sm.snapshot(); }", nil, &snapshot)
	return snapshot, err
}

func (b *RodBackend) DOMProbe(ctx context.Context) (string, error) {
	var summary string
	err := b.evalDOM(ctx, "() => { "+domLibJS+" return __sm.probe(); }", nil, &summary)
	return summary, err
}

func (b *RodBackend) ClickDOMButton(ctx context.Context, labels []string) (string, error) {
	// Do not retry side effects on a recovered page: a click may already have run.
	page, err := b.currentPage(ctx)
	if err != nil {
		return "", err
	}
	res, err := page.Eval("(labels) => { "+domLibJS+" return __sm.click(labels); }", labels)
	if err != nil {
		return "", err
	}
	var clicked string
	err = res.Value.Unmarshal(&clicked)
	return clicked, err
}

// SendDOMMessage uses CDP browser input/Enter, not page fetch, WebSocket,
// form.submit or synthetic KeyboardEvents. Success requires a NEW own row ID.
// An uncertain delivery is never automatically retried (avoids duplicate sends).
func (b *RodBackend) SendDOMMessage(ctx context.Context, text string) (bool, error) {
	return b.SendDOMMessageObserved(ctx, text, nil)
}

// Optional Show Mode display hook. Every actual read is exposed, including the
// final confirmed row before an immediate Skip. No proposed/draft text is emitted.
func (b *RodBackend) SendDOMMessageObserved(ctx context.Context, text string, observe func(DOMSnapshot)) (bool, error) {
	before, err := b.ReadDOMSnapshot(ctx)
	if err != nil {
		return false, err
	}
	if observe != nil {
		observe(before)
	}
	if !before.Controls.CanSend || before.Controls.Overflow || before.Controls.OwnName == "" {
		return false, fmt.Errorf("Show Mode composer/own identity unavailable; no send attempted")
	}
	page, err := b.currentPage(ctx)
	if err != nil {
		return false, err
	}
	// Bound element waits; user edits are never overwritten.
	actionCtx, cancel := context.WithTimeout(ctx, 4*time.Second)
	defer cancel()
	page = page.Context(actionCtx)
	el, err := page.Element(`[data-chat-composer="true"] textarea[aria-label="Send a message"]`)
	if err != nil {
		return false, err
	}
	value, err := el.Property("value")
	if err != nil {
		return false, err
	}
	if value.Str() != "" {
		return false, fmt.Errorf("Show Mode composer has a draft; refusing to overwrite it")
	}
	if err = el.Input(strings.TrimSpace(text)); err != nil {
		return false, err
	}
	check, err := b.ReadDOMSnapshot(actionCtx)
	if err != nil {
		return false, err
	}
	if observe != nil {
		observe(check)
	}
	if !sameDOMConversation(before.Controls, check.Controls) || !check.Controls.CanSend {
		// A navigation/rematch occurred while typing. Never press Enter here.
		return false, fmt.Errorf("Show Mode conversation changed while typing; inspect the draft")
	}
	if err = el.Type(input.Enter); err != nil {
		return false, err
	}
	deadline := time.NewTimer(3 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(150 * time.Millisecond)
	defer ticker.Stop()
	for {
		after, err := b.ReadDOMSnapshot(ctx)
		if err != nil {
			return false, err
		}
		if observe != nil {
			observe(after)
		}
		if !sameDOMConversation(before.Controls, after.Controls) {
			return false, fmt.Errorf("Show Mode conversation changed before send confirmation")
		}
		if confirmedDOMSend(before, after, strings.TrimSpace(text)) {
			return true, nil
		}
		select {
		case <-ctx.Done():
			return false, ctx.Err()
		case <-deadline.C:
			return false, nil
		case <-ticker.C:
		}
	}
}

func sameDOMConversation(before, after DOMControls) bool {
	return before.Conversation != "" && before.Conversation == after.Conversation &&
		after.Phase == "chatting" && strings.EqualFold(before.Partner, after.Partner)
}

func confirmedDOMSend(before, after DOMSnapshot, text string) bool {
	if !sameDOMConversation(before.Controls, after.Controls) || before.Controls.OwnName == "" {
		return false
	}
	ids := make(map[string]bool, len(before.Messages))
	for _, m := range before.Messages {
		ids[m.ID] = true
	}
	for _, m := range after.Messages {
		if m.ID != "" && !ids[m.ID] && m.Text == text && strings.EqualFold(m.Author, before.Controls.OwnName) {
			return true
		}
	}
	return false
}
