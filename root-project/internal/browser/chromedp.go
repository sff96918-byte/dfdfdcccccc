package browser

import (
	"context"
	"encoding/json"
	"fmt"

	"chitchatbot/internal/model"
	"chitchatbot/internal/proxy"
	"chitchatbot/internal/runtimeassets"
	cdpfetch "github.com/chromedp/cdproto/fetch"

	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/cdproto/runtime"
	"github.com/chromedp/chromedp"
)

type ChromeDPBackend struct {
	ctx         context.Context
	allocCancel context.CancelFunc
	cancel      context.CancelFunc
	proxyRelay  *proxy.Relay
}

func (b *ChromeDPBackend) Name() string {
	return "chromedp"
}

func (b *ChromeDPBackend) Start(parent context.Context, options Options) error {
	var relay *proxy.Relay
	success := false
	if options.Proxy != nil {
		var err error
		relay, err = proxy.StartRelay(parent, *options.Proxy)
		if err != nil {
			return err
		}
		options.Proxy = &relay.Entry
		defer func() {
			if !success {
				relay.Close()
			}
		}()
	}

	execOpts := append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.UserDataDir(options.UserDataDir),
		chromedp.Flag("headless", options.Headless),
		chromedp.Flag("no-first-run", true),
		chromedp.Flag("disable-popup-blocking", false),
		chromedp.Flag("disable-extensions", true),
	)
	if options.Proxy != nil {
		execOpts = append(execOpts, chromedp.ProxyServer(options.Proxy.BrowserURL()), chromedp.Flag("proxy-bypass-list", "<-loopback>"))
	}
	if bin, reason := runtimeassets.BundledChromiumPathForProfile(options.UserDataDir); bin != "" {
		fmt.Printf("chromedp: using bundled browser: %s\n", bin)
		execOpts = append(execOpts, chromedp.ExecPath(bin))
	} else if reason != "" {
		fmt.Printf("chromedp: skipping bundled browser: %s\n", reason)
	}

	allocCtx, allocCancel := chromedp.NewExecAllocator(parent, execOpts...)
	ctx, cancel := chromedp.NewContext(allocCtx)

	if options.Proxy != nil && options.Proxy.HasAuth() {
		chromedp.ListenTarget(ctx, func(ev any) {
			authEvent, ok := ev.(*cdpfetch.EventAuthRequired)
			if !ok || authEvent.AuthChallenge == nil {
				return
			}
			if authEvent.AuthChallenge.Source != cdpfetch.AuthChallengeSourceProxy {
				return
			}

			go func() {
				_ = cdpfetch.ContinueWithAuth(authEvent.RequestID, &cdpfetch.AuthChallengeResponse{
					Response: cdpfetch.AuthChallengeResponseResponseProvideCredentials,
					Username: options.Proxy.Username,
					Password: options.Proxy.Password,
				}).Do(ctx)
			}()
		})
	}

	actions := []chromedp.Action{
		network.Enable(),
	}
	if options.Proxy != nil && options.Proxy.HasAuth() {
		actions = append(actions, cdpfetch.Enable().WithHandleAuthRequests(true))
	}
	actions = append(actions,
		chromedp.Navigate(options.StartURL),
	)

	if err := chromedp.Run(ctx, actions...); err != nil {
		cancel()
		allocCancel()
		return err
	}

	success = true
	b.proxyRelay = relay
	b.ctx = ctx
	b.cancel = cancel
	b.allocCancel = allocCancel
	return nil
}

func (b *ChromeDPBackend) Close() error {
	if b.proxyRelay != nil {
		b.proxyRelay.Close()
	}
	if b.cancel != nil {
		b.cancel()
	}
	if b.allocCancel != nil {
		b.allocCancel()
	}
	return nil
}

func (b *ChromeDPBackend) CaptureSession(ctx context.Context) (model.LoginInfo, error) {
	if b.ctx == nil {
		return model.LoginInfo{}, ErrPageUnavailable
	}

	var cookies []*network.Cookie
	if err := chromedp.Run(b.ctx, chromedp.ActionFunc(func(execCtx context.Context) error {
		var err error
		cookies, err = network.GetCookies().
			WithURLs([]string{
				"https://app.chitchat.gg/",
				"https://api.chitchat.gg/",
			}).
			Do(execCtx)
		return err
	})); err != nil {
		return model.LoginInfo{}, err
	}

	var storage struct {
		LocalStorage   map[string]string `json:"local_storage"`
		SessionStorage map[string]string `json:"session_storage"`
	}
	if err := b.eval(ctx, "("+storageFunction+")()", &storage); err != nil {
		return model.LoginInfo{}, err
	}

	var fingerprint model.TransportFingerprint
	if err := b.eval(ctx, "("+fingerprintFunction+")()", &fingerprint); err != nil {
		return model.LoginInfo{}, err
	}

	login := model.LoginInfo{
		Backend:              b.Name(),
		Cookies:              make([]model.Cookie, 0, len(cookies)),
		UserAgent:            fingerprint.UserAgent,
		LocalStorage:         storage.LocalStorage,
		SessionStorage:       storage.SessionStorage,
		TransportFingerprint: fingerprint,
	}

	for _, cookie := range cookies {
		login.Cookies = append(login.Cookies, model.Cookie{
			Name:     cookie.Name,
			Value:    cookie.Value,
			Domain:   cookie.Domain,
			Path:     cookie.Path,
			Expires:  cookie.Expires,
			Secure:   cookie.Secure,
			HTTPOnly: cookie.HTTPOnly,
		})
	}

	return login, nil
}

func (b *ChromeDPBackend) Fetch(ctx context.Context, req FetchRequest) (FetchResponse, error) {
	if b.ctx == nil {
		return FetchResponse{}, ErrPageUnavailable
	}

	payload := map[string]any{
		"url":     req.URL,
		"method":  req.Method,
		"headers": req.Headers,
	}
	if req.Body != nil {
		payload["body"] = *req.Body
	}

	payloadJSON, err := json.Marshal(payload)
	if err != nil {
		return FetchResponse{}, err
	}

	script := fmt.Sprintf("(%s)(%s)", fetchFunction, string(payloadJSON))

	var raw []byte
	if err := chromedp.Run(b.ctx, chromedp.ActionFunc(func(execCtx context.Context) error {
		value, exception, err := runtime.Evaluate(script).
			WithAwaitPromise(true).
			WithReturnByValue(true).
			Do(execCtx)
		if err != nil {
			return err
		}
		if exception != nil {
			return exception
		}
		raw = value.Value
		return nil
	})); err != nil {
		return FetchResponse{}, err
	}

	var response FetchResponse
	if err := json.Unmarshal(raw, &response); err != nil {
		return FetchResponse{}, err
	}

	return response, nil
}

func (b *ChromeDPBackend) eval(ctx context.Context, expression string, dest any) error {
	var raw []byte
	if err := chromedp.Run(b.ctx, chromedp.Evaluate(expression, &raw)); err != nil {
		return err
	}
	return json.Unmarshal(raw, dest)
}
