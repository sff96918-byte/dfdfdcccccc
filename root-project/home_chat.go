package main

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"chitchatbot/internal/browser"
)

const homeChatRows = 500
const homeChatSessions = 12

type homeChatMessage struct {
	ID    string `json:"id"`
	Role  string `json:"role"`
	Text  string `json:"text"`
	Order int    `json:"order"`
}
type homeChatSessionInfo struct {
	ID      string `json:"id"`
	Label   string `json:"label"`
	Active  bool   `json:"active"`
	Dropped int    `json:"dropped"`
}
type homeChatSnapshot struct {
	Version  uint64                `json:"version"`
	Selected string                `json:"selected"`
	Sessions []homeChatSessionInfo `json:"sessions"`
	Messages []homeChatMessage     `json:"messages"`
	Evicted  int                   `json:"evicted"`
}
type homeChatSession struct {
	info homeChatSessionInfo
	run  uint64
	rows map[string]homeChatMessage
	// Never replay discarded older IDs when the DOM observer rescans its history.
	floor     int
	nextOrder int
}
type homeChatRun struct {
	label, current string
	suppressed     string
	chats          int
	ended          bool
}
type homeChatFeed struct {
	mu       sync.Mutex
	version  uint64
	nextRun  uint64
	runs     map[uint64]*homeChatRun
	sessions map[string]*homeChatSession
	order    []string
	evicted  int
}

func (a *App) homeChatFeed() *homeChatFeed {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.chatFeed == nil {
		a.chatFeed = &homeChatFeed{}
	}
	return a.chatFeed
}
func (f *homeChatFeed) start(label string) uint64 {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.runs == nil {
		f.runs = map[uint64]*homeChatRun{}
		f.sessions = map[string]*homeChatSession{}
	}
	f.nextRun++
	f.runs[f.nextRun] = &homeChatRun{label: label}
	return f.nextRun
}
func (f *homeChatFeed) end(run uint64) {
	f.mu.Lock()
	defer f.mu.Unlock()
	r := f.runs[run]
	if r == nil {
		return
	}
	r.ended = true
	for _, s := range f.sessions {
		if s.run == run && s.info.Active {
			s.info.Active = false
			f.version++
		}
	}
	f.trim()
}
func (f *homeChatFeed) versionValue() uint64 { f.mu.Lock(); defer f.mu.Unlock(); return f.version }
func (f *homeChatFeed) observe(run uint64, snapshot browser.DOMSnapshot, receipt bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	r := f.runs[run]
	if r == nil || r.ended {
		return
	}
	c := snapshot.Controls
	if c.Phase != "chatting" {
		if !receipt {
			if s := f.sessions[r.current]; s != nil && s.info.Active {
				s.info.Active = false
				f.version++
			}
		}
		return // Do not replace the last real transcript with an empty lobby snapshot.
	}
	if c.Conversation == "" || strings.TrimSpace(c.OwnName) == "" || strings.TrimSpace(c.Partner) == "" || strings.EqualFold(c.OwnName, c.Partner) {
		return
	}
	key := fmt.Sprintf("%d/%s", run, c.Conversation)
	if key == r.suppressed {
		return
	}
	s := f.sessions[key]
	if s == nil {
		// A late receipt can finish an old conversation but never switch the view.
		// Normally that session was already seeded by a prior actual read.
		if receipt && r.current != "" {
			return
		}
		if old := f.sessions[r.current]; old != nil {
			old.info.Active = false
		}
		r.chats++
		s = &homeChatSession{run: run, info: homeChatSessionInfo{ID: key, Label: fmt.Sprintf("%s · Chat %d", r.label, r.chats), Active: true}, rows: map[string]homeChatMessage{}}
		f.sessions[key] = s
		f.order = append(f.order, key)
		r.current = key
		f.version++
	}
	// Never reactivate a previous generation on a delayed read/receipt.
	if key == r.current && !receipt && !s.info.Active {
		s.info.Active = true
		f.version++
	}
	for _, row := range snapshot.Messages {
		if row.ID == "" || strings.TrimSpace(row.Text) == "" {
			continue
		}
		role := ""
		switch {
		case strings.EqualFold(row.Author, c.OwnName):
			role = "bot"
		case strings.EqualFold(row.Author, c.Partner):
			role = "user"
		default:
			continue
		}
		order := row.Order
		if order <= 0 {
			if old, ok := s.rows[row.ID]; ok {
				order = old.Order
			} else {
				s.nextOrder++
				order = s.nextOrder
			}
		}
		if order > s.nextOrder {
			s.nextOrder = order
		}
		if order <= s.floor {
			continue
		}
		message := homeChatMessage{ID: row.ID, Role: role, Text: row.Text, Order: order}
		if old, ok := s.rows[row.ID]; !ok || old != message {
			s.rows[row.ID] = message
			f.version++
		}
	}
	if len(s.rows) > homeChatRows {
		rows := orderedHomeChatRows(s)
		for _, row := range rows[:len(rows)-homeChatRows] {
			delete(s.rows, row.ID)
			if row.Order > s.floor {
				s.floor = row.Order
			}
			s.info.Dropped++
			f.version++
		}
	}
	f.trim()
}
func orderedHomeChatRows(s *homeChatSession) []homeChatMessage {
	rows := make([]homeChatMessage, 0, len(s.rows))
	for _, row := range s.rows {
		rows = append(rows, row)
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].Order == rows[j].Order {
			return rows[i].ID < rows[j].ID
		}
		return rows[i].Order < rows[j].Order
	})
	return rows
}
func (f *homeChatFeed) trim() {
	for len(f.order) > homeChatSessions {
		index := -1
		for i, id := range f.order {
			if !f.sessions[id].info.Active {
				index = i
				break
			}
		}
		if index < 0 {
			index = 0
		} // Display-only bound, even across multiple launch batches.
		id := f.order[index]
		session := f.sessions[id]
		if r := f.runs[session.run]; r != nil && r.current == id {
			r.suppressed = id
		}
		delete(f.sessions, id)
		f.order = append(f.order[:index], f.order[index+1:]...)
		f.evicted++
		f.version++
	}
	// Do not retain metadata for ended runs after their last session is evicted.
	for id, r := range f.runs {
		if !r.ended {
			continue
		}
		retained := false
		for _, s := range f.sessions {
			if s.run == id {
				retained = true
				break
			}
		}
		if !retained {
			delete(f.runs, id)
		}
	}
}
func (f *homeChatFeed) snapshot(selected string) homeChatSnapshot {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := homeChatSnapshot{Version: f.version, Sessions: []homeChatSessionInfo{}, Messages: []homeChatMessage{}, Evicted: f.evicted}
	for _, id := range f.order {
		out.Sessions = append(out.Sessions, f.sessions[id].info)
	}
	if f.sessions[selected] == nil {
		selected = ""
		for i := len(f.order) - 1; i >= 0; i-- {
			id := f.order[i]
			if selected == "" {
				selected = id
			}
			if f.sessions[id].info.Active {
				selected = id
				break
			}
		}
	}
	out.Selected = selected
	if s := f.sessions[selected]; s != nil {
		out.Messages = orderedHomeChatRows(s)
	}
	return out
}

// Optional additive native-send hook; the existing DOMChatBackend contract and
// boolean send semantics are unchanged for old callers and fake backends.
type homeObservedSender interface {
	SendDOMMessageObserved(context.Context, string, func(browser.DOMSnapshot)) (bool, error)
}
type homeObservedDOM struct {
	browser.DOMChatBackend
	feed   *homeChatFeed
	run    uint64
	readMu sync.Mutex
}

func (d *homeObservedDOM) ReadDOMSnapshot(ctx context.Context) (browser.DOMSnapshot, error) {
	d.readMu.Lock()
	defer d.readMu.Unlock()
	snapshot, err := d.DOMChatBackend.ReadDOMSnapshot(ctx)
	if err == nil {
		d.feed.observe(d.run, snapshot, false)
	}
	return snapshot, err
}
func (d *homeObservedDOM) SendDOMMessage(ctx context.Context, text string) (bool, error) {
	if sender, ok := d.DOMChatBackend.(homeObservedSender); ok {
		return sender.SendDOMMessageObserved(ctx, text, func(snapshot browser.DOMSnapshot) { d.feed.observe(d.run, snapshot, true) })
	}
	// Do not invent a message from outgoing intent on an older backend. The
	// passive reader can still show actual own rows observed in that backend.
	return d.DOMChatBackend.SendDOMMessage(ctx, text)
}
func (a *App) startHomeChatObserver(ctx context.Context, dom browser.DOMChatBackend, profile string, index, total int) (browser.DOMChatBackend, func()) {
	feed := a.homeChatFeed()
	run := feed.start(fmt.Sprintf("%s · Browser %d/%d", profile, index, total))
	observed := &homeObservedDOM{DOMChatBackend: dom, feed: feed, run: run}
	watchCtx, cancel := context.WithCancel(ctx)
	stopApp := func() bool { return false }
	if a.ctx != nil {
		stopApp = context.AfterFunc(a.ctx, cancel)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(250 * time.Millisecond)
		defer ticker.Stop()
		last := uint64(0)
		for {
			select {
			case <-watchCtx.Done():
				return
			case <-ticker.C:
			}
			readCtx, stop := context.WithTimeout(watchCtx, 750*time.Millisecond)
			_, _ = observed.ReadDOMSnapshot(readCtx)
			stop()
			version := feed.versionValue()
			if version != last {
				last = version
				a.emit("ui:chat_changed", map[string]uint64{"version": version})
			}
		}
	}()
	var once sync.Once
	return observed, func() {
		once.Do(func() {
			cancel()
			stopApp()
			feed.end(run)
			select {
			case <-done:
			case <-time.After(time.Second):
			}
		})
	}
}
