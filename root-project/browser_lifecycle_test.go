package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"chitchatbot/internal/browser"
	"chitchatbot/internal/model"
	"chitchatbot/internal/proxy"
)

// Synthetic data only. No real browser, HTTP request, account or token is used.
type fakeCaptureBrowser struct {
	options                          browser.Options
	login                            model.LoginInfo
	startErr, captureErr, fetchErr   error
	status, starts, captures, closes int
	cancel                           context.CancelFunc
	closeSawFiles                    bool
}

func (f *fakeCaptureBrowser) Name() string { return "fake" }
func (f *fakeCaptureBrowser) Start(_ context.Context, o browser.Options) error {
	f.starts++
	f.options = o
	return f.startErr
}
func (f *fakeCaptureBrowser) CaptureSession(context.Context) (model.LoginInfo, error) {
	f.captures++
	if f.cancel != nil {
		f.cancel()
	}
	return f.login, f.captureErr
}
func (f *fakeCaptureBrowser) Fetch(_ context.Context, r browser.FetchRequest) (browser.FetchResponse, error) {
	if r.Method != "GET" || !strings.HasSuffix(r.URL, "/users/me/relationships") {
		return browser.FetchResponse{}, errors.New("unexpected authentication request")
	}
	return browser.FetchResponse{Status: f.status}, f.fetchErr
}
func (f *fakeCaptureBrowser) Close() error {
	f.closes++
	_, err := os.Stat(f.options.UserDataDir)
	f.closeSawFiles = err == nil
	return nil
}
func syntheticLogin() model.LoginInfo {
	return model.LoginInfo{UserID: "test-user", Cookies: []model.Cookie{{Name: "token", Value: "synthetic-not-a-real-token"}}}
}

func TestBrowserLifecycle(t *testing.T) {
	for _, scenario := range []string{"success", "start-failure", "capture-failure", "closed", "missing-token", "unauthorized", "auth-error", "missing-user", "save-failure"} {
		t.Run(scenario, func(t *testing.T) {
			a := browserTestApp(t)
			dir := filepath.Join(a.dataDir, "captured")
			if err := os.MkdirAll(dir, 0700); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			f := &fakeCaptureBrowser{login: syntheticLogin(), status: 200}
			switch scenario {
			case "start-failure":
				f.startErr = errors.New("synthetic start failure")
			case "capture-failure":
				f.captureErr = errors.New("synthetic capture failure")
			case "closed":
				f.captureErr = browser.ErrPageUnavailable
			case "missing-token":
				f.login = model.LoginInfo{}
				f.cancel = cancel
			case "unauthorized":
				f.status = 401
				f.cancel = cancel
			case "auth-error":
				f.fetchErr = errors.New("synthetic transport failure")
				f.cancel = cancel
			case "missing-user":
				f.login.UserID = ""
			case "save-failure":
				// A file where a directory is required gives a deterministic cross-platform failure.
				dir = filepath.Join(a.dataDir, "not-a-directory")
				if err := os.WriteFile(dir, []byte("test"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			settings := model.BrowserSettings{Backend: "chromedp", Profile: "capture-test", Headless: true}
			entry := &proxy.Entry{Scheme: "http", Host: "example.invalid", Port: "8080"}
			a.runFreshBrowserWithBackend(ctx, dir, settings.Profile, settings, entry, 1, 1, func(key string) (browser.Backend, error) {
				if key != "chromedp" {
					t.Fatalf("wrong backend: %s", key)
				}
				return f, nil
			})
			if f.starts != 1 || f.closes != 1 || !f.closeSawFiles {
				t.Fatalf("start/close/ordering: %#v", f)
			}
			if f.options.Proxy != entry || !f.options.Headless || !f.options.DisablePageRecovery {
				t.Fatalf("wrong options: %#v", f.options)
			}
			if _, err := os.Stat(filepath.Dir(f.options.UserDataDir)); !os.IsNotExist(err) {
				t.Fatalf("temporary directory not removed: %v", err)
			}
			files, err := filepath.Glob(filepath.Join(dir, "login_info_*.json"))
			if err != nil {
				t.Fatal(err)
			}
			logs, err := a.uiStore.ListLogs(100)
			if err != nil {
				t.Fatal(err)
			}
			savedMessage := false
			for _, l := range logs {
				if strings.Contains(l.Message, "Login saved to") {
					savedMessage = true
				}
			}
			if scenario == "success" {
				if len(files) != 1 || !savedMessage {
					t.Fatalf("missing saved login: %v", files)
				}
				b, err := os.ReadFile(files[0])
				if err != nil {
					t.Fatal(err)
				}
				var got model.LoginInfo
				if err := json.Unmarshal(b, &got); err != nil {
					t.Fatal(err)
				}
				if got.Backend != settings.Backend || got.Profile != settings.Profile || got.UserID != "test-user" || got.SavedAt.IsZero() {
					t.Fatalf("wrong saved metadata: %#v", got)
				}
			} else if len(files) != 0 || savedMessage {
				t.Fatalf("false success for %s", scenario)
			}
		})
	}
}

func TestBrowserFactoryFailureCleansTemp(t *testing.T) {
	// Each test owns its temp root. Do not use t.Parallel with t.Setenv.
	temp := t.TempDir()
	t.Setenv("TMPDIR", temp)
	t.Setenv("TMP", temp)
	t.Setenv("TEMP", temp)
	a := browserTestApp(t)
	a.runFreshBrowserWithBackend(context.Background(), a.dataDir, "factory-test", model.BrowserSettings{Backend: "rod"}, nil, 1, 1, func(string) (browser.Backend, error) { return nil, errors.New("factory failed") })
	matches, err := filepath.Glob(filepath.Join(temp, "chitchat-fresh-browser-*"))
	if err != nil || len(matches) != 0 {
		t.Fatalf("leaked temporary data: %v, %v", matches, err)
	}
}
