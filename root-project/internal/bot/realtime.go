package bot

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"

	"chitchatbot/internal/model"
	proxypkg "chitchatbot/internal/proxy"
	"github.com/gorilla/websocket"
	utls "github.com/refraction-networking/utls"
	xproxy "golang.org/x/net/proxy"
)

const realtimeSocketURL = "wss://api.chitchat.gg/socket.io/?EIO=4&transport=websocket"

var (
	releaseCacheMu     sync.RWMutex
	cachedRelease      = "bca74a7b1371b56a813e362c289cac9ef974978f"
	lastReleaseFetch   time.Time
	indexScriptRegex   = regexp.MustCompile(`src="(/_app/assets/js-direct-index-[^"]+\.js)"`)
	sentryReleaseRegex = regexp.MustCompile(`SENTRY_RELEASE=\{id:[` + "`" + `"]([a-f0-9]{40})[` + "`" + `"]\}`)
)

func getOrFetchReleaseHash() string {
	releaseCacheMu.RLock()
	if cachedRelease != "" && time.Since(lastReleaseFetch) < 30*time.Minute {
		rel := cachedRelease
		releaseCacheMu.RUnlock()
		return rel
	}
	releaseCacheMu.RUnlock()

	rel := fetchCurrentReleaseFromWeb()
	releaseCacheMu.Lock()
	cachedRelease = rel
	lastReleaseFetch = time.Now()
	releaseCacheMu.Unlock()
	return rel
}

func fetchCurrentReleaseFromWeb() string {
	fallback := "bca74a7b1371b56a813e362c289cac9ef974978f"
	client := &http.Client{Timeout: 5 * time.Second}

	resp, err := client.Get("https://app.chitchat.gg")
	if err != nil {
		return fallback
	}
	defer resp.Body.Close()
	htmlData, err := io.ReadAll(resp.Body)
	if err != nil {
		return fallback
	}

	matches := indexScriptRegex.FindSubmatch(htmlData)
	if len(matches) < 2 {
		return fallback
	}
	scriptPath := string(matches[1])

	respJS, err := client.Get("https://app.chitchat.gg" + scriptPath)
	if err != nil {
		return fallback
	}
	defer respJS.Body.Close()
	jsData, err := io.ReadAll(io.LimitReader(respJS.Body, 4096))
	if err != nil {
		return fallback
	}

	releaseMatches := sentryReleaseRegex.FindSubmatch(jsData)
	if len(releaseMatches) >= 2 {
		return string(releaseMatches[1])
	}

	return fallback
}

type realtimeEvent struct {
	Type           string
	ConversationID string
	UserID         string
	Username       string
	MessageID      string
	Content        string
	CreatedAt      string
	Closed         bool
	ClosedBy       string
	InQueue        bool
	HasQueueState  bool
	Target         model.DMTarget
}

func startRealtimeListener(
	parent context.Context,
	cfg Config,
	login model.LoginInfo,
	currentUserID string,
) (<-chan realtimeEvent, context.CancelFunc) {
	ctx, cancel := context.WithCancel(parent)
	events := make(chan realtimeEvent, 32)

	go func() {
		defer cancel()

		consecutiveBadStatus := 0
		backoff := time.Second
		for {
			select {
			case <-ctx.Done():
				return
			default:
			}

			err := connectRealtimeSocket(ctx, cfg, login, currentUserID, events)
			if err == nil || ctx.Err() != nil {
				return
			}

			if badStatus, ok := err.(*badStatusError); ok {
				if badStatus.status == 401 || badStatus.status == 403 || badStatus.status == 404 {
					consecutiveBadStatus++
					if consecutiveBadStatus >= 10 {
						logf(cfg, "[realtime] Bot account %s marked dead after 10 consecutive %d errors on websocket.\n", currentUserID, badStatus.status)
						_ = trackBotSendStatus(cfg, currentUserID, badStatus.status)
						return
					}
				} else {
					consecutiveBadStatus = 0
				}
			} else {
				consecutiveBadStatus = 0
			}

			logf(cfg, "[realtime] socket disconnected: %v\n", err)

			timer := time.NewTimer(backoff)
			select {
			case <-ctx.Done():
				timer.Stop()
				return
			case <-timer.C:
			}

			if backoff < 15*time.Second {
				backoff *= 2
				if backoff > 15*time.Second {
					backoff = 15 * time.Second
				}
			}
		}
	}()

	return events, cancel
}

func connectRealtimeSocket(
	ctx context.Context,
	cfg Config,
	login model.LoginInfo,
	currentUserID string,
	events chan<- realtimeEvent,
) error {
	token := strings.TrimSpace(login.Token())
	if token == "" {
		return fmt.Errorf("missing token for realtime socket")
	}

	dialer, err := realtimeDialer(cfg.ProxyRotator)
	if err != nil {
		return err
	}

	headers := realtimeHeaders(login, token)
	conn, resp, err := dialer.DialContext(ctx, realtimeSocketURL, headers)
	if err != nil {
		if resp != nil {
			return &badStatusError{status: resp.StatusCode, err: err}
		}
		return err
	}
	defer conn.Close()
	go func() {
		<-ctx.Done()
		_ = conn.Close()
	}()

	logf(cfg, "[realtime] socket connected.\n")

	if err := conn.SetReadDeadline(time.Now().Add(20 * time.Second)); err != nil {
		return err
	}
	_, firstMessage, err := conn.ReadMessage()
	if err != nil {
		return err
	}
	if !strings.HasPrefix(string(firstMessage), "0") {
		return fmt.Errorf("unexpected realtime open packet: %s", firstMessage)
	}

	if err := conn.SetReadDeadline(time.Time{}); err != nil {
		return err
	}

	releaseHash := getOrFetchReleaseHash()
	connectPacket := fmt.Sprintf(`40{"release":%q}`, releaseHash)
	if err := conn.WriteMessage(websocket.TextMessage, []byte(connectPacket)); err != nil {
		return err
	}

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		if err := conn.SetReadDeadline(time.Now().Add(65 * time.Second)); err != nil {
			return err
		}
		_, payload, err := conn.ReadMessage()
		if err != nil {
			return err
		}

		packet := string(payload)
		switch {
		case packet == "2":
			if err := conn.WriteMessage(websocket.TextMessage, []byte("3")); err != nil {
				return err
			}
		case strings.HasPrefix(packet, "40"):
			logf(cfg, "[realtime] socket handshake accepted: %s\n", packet)
		case strings.HasPrefix(packet, "42"):
			event, ok, err := parseRealtimeEvent(packet, currentUserID)
			if err != nil {
				logf(cfg, "[realtime] failed to parse packet: %v\n", err)
				continue
			}
			if !ok {
				continue
			}
			logRealtimeEvent(cfg, currentUserID, event)
			select {
			case events <- event:
			default:
			}
		}
	}
}

func realtimeDialer(rotator *proxypkg.Rotator) (*websocket.Dialer, error) {
	var proxyEntry *proxypkg.Entry
	if rotator != nil && rotator.Len() > 0 {
		if entry, ok := rotator.Next(); ok {
			proxyEntry = &entry
		}
	}

	dialer := &websocket.Dialer{
		HandshakeTimeout:  30 * time.Second,
		EnableCompression: true,
		NetDialTLSContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			return dialWithChromeUTLS(ctx, network, addr, proxyEntry)
		},
	}

	return dialer, nil
}

func dialWithChromeUTLS(ctx context.Context, network, addr string, entry *proxypkg.Entry) (net.Conn, error) {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		host = addr
	}

	var rawConn net.Conn
	baseDialer := &net.Dialer{Timeout: 30 * time.Second}

	if entry != nil && entry.Address() != "" {
		switch entry.SchemeOrDefault() {
		case "http", "https":
			proxyConn, err := baseDialer.DialContext(ctx, "tcp", entry.Address())
			if err != nil {
				return nil, fmt.Errorf("failed to dial HTTP proxy %s: %w", entry.Address(), err)
			}
			req := fmt.Sprintf("CONNECT %s HTTP/1.1\r\nHost: %s\r\n", addr, host)
			if entry.HasAuth() {
				auth := base64.StdEncoding.EncodeToString([]byte(entry.Username + ":" + entry.Password))
				req += fmt.Sprintf("Proxy-Authorization: Basic %s\r\n", auth)
			}
			req += "\r\n"
			if _, err := proxyConn.Write([]byte(req)); err != nil {
				proxyConn.Close()
				return nil, fmt.Errorf("failed to send CONNECT to proxy: %w", err)
			}
			br := bufio.NewReader(proxyConn)
			resp, err := http.ReadResponse(br, &http.Request{Method: "CONNECT"})
			if err != nil {
				proxyConn.Close()
				return nil, fmt.Errorf("failed to read proxy CONNECT response: %w", err)
			}
			if resp.StatusCode != 200 {
				proxyConn.Close()
				return nil, fmt.Errorf("proxy CONNECT failed with status %d", resp.StatusCode)
			}
			rawConn = proxyConn
		case "socks5":
			auth := &xproxy.Auth{}
			if entry.HasAuth() {
				auth.User = entry.Username
				auth.Password = entry.Password
			}
			socksDialer, err := xproxy.SOCKS5("tcp", entry.Address(), auth, baseDialer)
			if err != nil {
				return nil, err
			}
			type dialRes struct {
				conn net.Conn
				err  error
			}
			resCh := make(chan dialRes, 1)
			go func() {
				c, e := socksDialer.Dial(network, addr)
				resCh <- dialRes{conn: c, err: e}
			}()
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case r := <-resCh:
				if r.err != nil {
					return nil, r.err
				}
				rawConn = r.conn
			}
		default:
			c, err := baseDialer.DialContext(ctx, network, addr)
			if err != nil {
				return nil, err
			}
			rawConn = c
		}
	} else {
		c, err := baseDialer.DialContext(ctx, network, addr)
		if err != nil {
			return nil, err
		}
		rawConn = c
	}

	uConn := utls.UClient(rawConn, &utls.Config{
		ServerName: host,
		NextProtos: []string{"http/1.1"},
	}, utls.HelloChrome_120)

	// Ensure ALPN is strictly http/1.1 so Cloudflare doesn't negotiate HTTP/2 on websocket
	if err := uConn.BuildHandshakeState(); err == nil {
		for _, ext := range uConn.Extensions {
			if alpnExt, ok := ext.(*utls.ALPNExtension); ok {
				alpnExt.AlpnProtocols = []string{"http/1.1"}
			}
		}
	}

	if err := uConn.HandshakeContext(ctx); err != nil {
		rawConn.Close()
		return nil, fmt.Errorf("uTLS handshake failed: %w", err)
	}

	return uConn, nil
}

func realtimeHeaders(login model.LoginInfo, token string) http.Header {
	userAgent := strings.TrimSpace(login.UserAgent)
	if userAgent == "" {
		userAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/150.0.0.0 Safari/537.36"
	}

	headers := http.Header{}
	headers.Set("Origin", "https://app.chitchat.gg")
	headers.Set("User-Agent", userAgent)
	headers.Set("Accept-Language", "en-US,en;q=0.9")
	cookieHeader := login.CookieHeader()
	if cookieHeader == "" {
		cookieHeader = "token=" + token
	}
	headers.Set("Cookie", cookieHeader)
	headers.Set("Cache-Control", "no-cache")
	headers.Set("Pragma", "no-cache")
	return headers
}

type badStatusError struct {
	status int
	err    error
}

func (e *badStatusError) Error() string { return e.err.Error() }

func parseRealtimeEvent(packet string, currentUserID string) (realtimeEvent, bool, error) {
	var parts []json.RawMessage
	if err := json.Unmarshal([]byte(strings.TrimPrefix(packet, "42")), &parts); err != nil {
		return realtimeEvent{}, false, err
	}
	if len(parts) == 0 {
		return realtimeEvent{}, false, nil
	}

	var eventName string
	if err := json.Unmarshal(parts[0], &eventName); err != nil {
		return realtimeEvent{}, false, err
	}
	if len(parts) < 2 {
		return realtimeEvent{Type: eventName}, true, nil
	}

	switch eventName {
	case "typing":
		var payload struct {
			UserID         string `json:"userId"`
			ConversationID string `json:"conversationId"`
		}
		if err := json.Unmarshal(parts[1], &payload); err != nil {
			return realtimeEvent{}, false, err
		}
		return realtimeEvent{
			Type:           eventName,
			UserID:         payload.UserID,
			ConversationID: payload.ConversationID,
		}, true, nil
	case "chatMessage":
		var payload struct {
			Message struct {
				ID             string `json:"id"`
				ConversationID string `json:"conversationId"`
				Content        string `json:"content"`
				CreatedAt      string `json:"createdAt"`
				Author         struct {
					ID       string `json:"id"`
					Username string `json:"username"`
				} `json:"author"`
			} `json:"message"`
		}
		if err := json.Unmarshal(parts[1], &payload); err != nil {
			return realtimeEvent{}, false, err
		}
		return realtimeEvent{
			Type:           eventName,
			ConversationID: payload.Message.ConversationID,
			UserID:         payload.Message.Author.ID,
			Username:       payload.Message.Author.Username,
			MessageID:      payload.Message.ID,
			Content:        payload.Message.Content,
			CreatedAt:      payload.Message.CreatedAt,
			Target: model.DMTarget{
				ConversationID: payload.Message.ConversationID,
				UserID:         nonCurrentUserID(currentUserID, payload.Message.Author.ID),
				Username:       nonCurrentUsername(currentUserID, payload.Message.Author.ID, payload.Message.Author.Username),
			},
		}, true, nil
	case "matchUpdate":
		var payload struct {
			InQueue *bool `json:"inQueue"`
			Match   struct {
				Conversation struct {
					ID           string `json:"id"`
					Participants []struct {
						Profile struct {
							ID       string `json:"id"`
							Username string `json:"username"`
						} `json:"profile"`
					} `json:"participants"`
				} `json:"conversation"`
				Closure struct {
					Closed   bool   `json:"closed"`
					ClosedBy string `json:"closedBy"`
				} `json:"closure"`
			} `json:"match"`
		}
		if err := json.Unmarshal(parts[1], &payload); err != nil {
			return realtimeEvent{}, false, err
		}
		target := matchTargetFromParticipants(currentUserID, payload.Match.Conversation.ID, payload.Match.Conversation.Participants)
		hasQueueState := payload.InQueue != nil
		inQueue := false
		if payload.InQueue != nil {
			inQueue = *payload.InQueue
		}
		return realtimeEvent{
			Type:           eventName,
			ConversationID: payload.Match.Conversation.ID,
			Closed:         payload.Match.Closure.Closed,
			ClosedBy:       payload.Match.Closure.ClosedBy,
			InQueue:        inQueue,
			HasQueueState:  hasQueueState,
			Target:         target,
		}, true, nil
	default:
		return realtimeEvent{}, false, nil
	}
}

func logRealtimeEvent(cfg Config, currentUserID string, event realtimeEvent) {
	switch event.Type {
	case "typing":
		if event.UserID != "" && event.UserID != currentUserID {
			logf(cfg, "[realtime] typing user=%s conversation=%s\n", event.UserID, event.ConversationID)
		}
	case "chatMessage":
		if event.UserID == currentUserID {
			logf(cfg, "[realtime] outgoing chat conversation=%s message=%s\n", event.ConversationID, event.MessageID)
			return
		}
		logf(cfg, "[realtime] incoming chat user=%s conversation=%s message=%s content=%q\n", event.UserID, event.ConversationID, event.MessageID, event.Content)
	case "matchUpdate":
		if event.HasQueueState && event.ConversationID == "" {
			logf(cfg, "[realtime] match queue in_queue=%t\n", event.InQueue)
			return
		}
		if event.Closed {
			logf(cfg, "[realtime] conversation closed id=%s closed_by=%s\n", event.ConversationID, event.ClosedBy)
			return
		}
		if event.Target.UserID != "" {
			logf(cfg, "[realtime] matched conversation id=%s user=%s (%s) in_queue=%t\n", event.ConversationID, event.Target.Username, event.Target.UserID, event.InQueue)
			return
		}
		logf(cfg, "[realtime] conversation update id=%s in_queue=%t\n", event.ConversationID, event.InQueue)
	}
}

func shouldWakeForRealtimeEvent(currentUserID string, event realtimeEvent) bool {
	switch event.Type {
	case "chatMessage":
		return event.UserID != "" && event.UserID != currentUserID
	case "matchUpdate":
		return event.ConversationID != ""
	default:
		return false
	}
}

func matchTargetFromParticipants(
	currentUserID string,
	conversationID string,
	participants []struct {
		Profile struct {
			ID       string `json:"id"`
			Username string `json:"username"`
		} `json:"profile"`
	},
) model.DMTarget {
	target := model.DMTarget{ConversationID: conversationID}
	for _, participant := range participants {
		userID := strings.TrimSpace(participant.Profile.ID)
		username := strings.TrimSpace(participant.Profile.Username)
		if userID == "" {
			continue
		}
		if userID == currentUserID {
			target.BotUsername = username
			continue
		}
		if target.UserID == "" {
			target.UserID = userID
			target.Username = username
		}
	}
	return target
}

func nonCurrentUserID(currentUserID string, userID string) string {
	if strings.TrimSpace(userID) == "" || userID == currentUserID {
		return ""
	}
	return userID
}

func nonCurrentUsername(currentUserID string, userID string, username string) string {
	if nonCurrentUserID(currentUserID, userID) == "" {
		return ""
	}
	return strings.TrimSpace(username)
}
