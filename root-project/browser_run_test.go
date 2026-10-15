package main

import (
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"testing"

	"chitchatbot/internal/model"
	"chitchatbot/internal/proxy"
	"chitchatbot/internal/uistore"
)

func browserTestApp(t *testing.T) *App {
	t.Helper()
	dir := t.TempDir()
	s, err := uistore.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return &App{dataDir: dir, uiStore: s}
}

func TestValidateBrowserRun(t *testing.T) {
	for _, key := range []string{"rod", "chromedp"} {
		for _, count := range []int{1, 5} {
			got, err := validateBrowserRun(model.BrowserSettings{Backend: key, Profile: " ../unsafe/name ", Headless: true}, count)
			if err != nil {
				t.Fatal(err)
			}
			if got.Backend != key || !got.Headless || got.Profile != "__unsafe_name" {
				t.Fatalf("unexpected normalization: %#v", got)
			}
		}
	}
	for _, c := range []struct {
		key   string
		count int
	}{{"rod", 0}, {"rod", 6}, {"rod", -1}, {"", 1}, {"invalid", 1}, {"Rod", 1}} {
		if _, err := validateBrowserRun(model.BrowserSettings{Backend: c.key}, c.count); err == nil {
			t.Fatalf("accepted invalid request: %#v", c)
		}
	}
	got, err := validateBrowserRun(model.BrowserSettings{Backend: "rod", Profile: "  "}, 1)
	if err != nil || got.Profile != "default" {
		t.Fatalf("default profile: %#v, %v", got, err)
	}
}

func TestDirectRunUsesRequestNotSavedBackend(t *testing.T) {
	a := browserTestApp(t)
	old := model.BrowserSettings{Backend: "rod", Profile: "old"}
	if err := a.uiStore.SaveBrowserSettings(old); err != nil {
		t.Fatal(err)
	}
	request := model.BrowserSettings{Backend: "chromedp", Profile: "new", Headless: true}
	calls := 0
	err := a.runBrowser(request, 3, func(dir, profile string, got model.BrowserSettings, entry *proxy.Entry, index, total int) {
		calls++
		if got != request || profile != "new" || dir != filepath.Join(a.dataDir, "profiles", "new") || index != calls || total != 3 || entry != nil {
			t.Fatalf("wrong dispatch: %#v, %s, %s, %d/%d", got, dir, profile, index, total)
		}
		// Simulate another request changing defaults between dispatches.
		if err := a.uiStore.SaveBrowserSettings(old); err != nil {
			t.Fatal(err)
		}
	})
	if err != nil || calls != 3 {
		t.Fatalf("dispatch count %d, error %v", calls, err)
	}
}

func TestDirectRunRemembersAcceptedSettings(t *testing.T) {
	a := browserTestApp(t)
	request := model.BrowserSettings{Backend: "chromedp", Profile: " ", Headless: true}
	err := a.runBrowser(request, 1, func(_, _ string, _ model.BrowserSettings, _ *proxy.Entry, _, _ int) {})
	if err != nil {
		t.Fatal(err)
	}
	got, err := a.uiStore.LoadBrowserSettings()
	if err != nil || got.Backend != "chromedp" || got.Profile != "default" || !got.Headless {
		t.Fatalf("saved %#v, %v", got, err)
	}
}

func TestDirectRunPreflightFailureDoesNotSaveOrLaunch(t *testing.T) {
	for _, scenario := range []string{"backend", "count", "proxy", "directory", "closed-store"} {
		t.Run(scenario, func(t *testing.T) {
			a := browserTestApp(t)
			old := model.BrowserSettings{Backend: "rod", Profile: "original"}
			if err := a.uiStore.SaveBrowserSettings(old); err != nil {
				t.Fatal(err)
			}
			request := model.BrowserSettings{Backend: "chromedp", Profile: "new"}
			count := 1
			switch scenario {
			case "backend":
				request.Backend = "invalid"
			case "count":
				count = 6
			case "proxy":
				if err := a.uiStore.SaveProxySettings(model.ProxySettings{Enabled: true, Entries: ""}); err != nil {
					t.Fatal(err)
				}
			case "directory":
				if err := os.WriteFile(filepath.Join(a.dataDir, "profiles"), []byte("blocked"), 0600); err != nil {
					t.Fatal(err)
				}
			case "closed-store":
				_ = a.uiStore.Close()
			}
			calls := 0
			err := a.runBrowser(request, count, func(_, _ string, _ model.BrowserSettings, _ *proxy.Entry, _, _ int) { calls++ })
			if err == nil || calls != 0 {
				t.Fatalf("error %v, launches %d", err, calls)
			}
			if scenario != "closed-store" {
				got, err := a.uiStore.LoadBrowserSettings()
				if err != nil || !reflect.DeepEqual(got, old) {
					t.Fatalf("defaults modified: %#v, %v", got, err)
				}
			}
		})
	}
}

// Overlapping requests must never reload another request's saved defaults.
func TestConcurrentDirectRunIsolation(t *testing.T) {
	a := browserTestApp(t)
	requests := []model.BrowserSettings{
		{Backend: "rod", Profile: "first", Headless: false},
		{Backend: "chromedp", Profile: "second", Headless: true},
	}
	var wg sync.WaitGroup
	results := make(chan model.BrowserSettings, 2)
	errs := make(chan error, 2)
	for _, request := range requests {
		wg.Add(1)
		go func(request model.BrowserSettings) {
			defer wg.Done()
			errs <- a.runBrowser(request, 1, func(_, _ string, got model.BrowserSettings, _ *proxy.Entry, _, _ int) { results <- got })
		}(request)
	}
	wg.Wait()
	close(results)
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	got := map[string]model.BrowserSettings{}
	for result := range results {
		got[result.Profile] = result
	}
	for _, want := range requests {
		if got[want.Profile] != want {
			t.Fatalf("request mixed: got %#v, want %#v", got[want.Profile], want)
		}
	}
}

func TestSafeProfileNameWindows(t *testing.T) {
	for input, want := range map[string]string{
		".": "default", " ": "default", "normal": "normal",
		"CON": "_CON", "con.txt": "_con.txt", "LPT1": "_LPT1",
		"COM9.json": "_COM9.json", "COM10": "COM10", "account. ": "account",
		"a\x00b": "a_b", "../unsafe/name": "__unsafe_name",
	} {
		if got := safeProfileName(input); got != want {
			t.Errorf("safeProfileName(%q) = %q; want %q", input, got, want)
		}
	}
}

func TestShowModeNormalizesToVisibleRod(t *testing.T) {
	for _, headless := range []bool{false, true} {
		got, err := validateBrowserRun(model.BrowserSettings{Backend: "show_mode", Profile: "visible", Headless: headless}, 1)
		if err != nil || got.Backend != "rod" || got.Headless || !got.LiveChat || got.Profile != "visible" {
			t.Fatalf("unexpected Show Mode settings: %#v, %v", got, err)
		}
	}
}

func TestShowModeDispatchAndSavedDefaults(t *testing.T) {
	a := browserTestApp(t)
	calls := 0
	err := a.runBrowser(model.BrowserSettings{Backend: "show_mode", Profile: "visible", Headless: true}, 2,
		func(_, profile string, got model.BrowserSettings, _ *proxy.Entry, index, total int) {
			calls++
			if got.Backend != "rod" || got.Headless || !got.LiveChat || profile != "visible" || index != calls || total != 2 {
				t.Fatalf("incorrect Show Mode dispatch: %#v", got)
			}
		})
	if err != nil || calls != 2 {
		t.Fatalf("Show Mode launch count=%d err=%v", calls, err)
	}
	saved, err := a.uiStore.LoadBrowserSettings()
	if err != nil || saved.Backend != "rod" || saved.Headless || !saved.LiveChat || saved.Profile != "visible" {
		t.Fatalf("incorrect persisted Show Mode defaults: %#v, %v", saved, err)
	}
}
