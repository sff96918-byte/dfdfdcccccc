package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"

	"chitchatbot/internal/applog"
	"chitchatbot/internal/model"
	"chitchatbot/internal/store"
	"chitchatbot/internal/uistore"
)

// Avoid NewApp: tests must never contact licensing or launch the desktop/browser.
func diagnosticTestApp(t *testing.T) *App {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "data")
	s, err := uistore.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return &App{dataDir: dir, uiStore: s}
}
func diagnosticEntries(t *testing.T, a *App) []applog.Entry {
	t.Helper()
	data, err := os.ReadFile(applog.Path(a.dataDir))
	if err != nil {
		t.Fatal(err)
	}
	var entries []applog.Entry
	for _, line := range strings.Split(strings.TrimSuffix(string(data), "\n"), "\n") {
		var entry applog.Entry
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			t.Fatal(err)
		}
		entries = append(entries, entry)
	}
	return entries
}
func TestAppDiagnosticsPersistAllLevelsAndSurviveClearLogs(t *testing.T) {
	a := diagnosticTestApp(t)
	for _, level := range []string{"activity", "error", "info", "warning", "debug"} {
		a.log(level, "synthetic diagnostic "+level)
	}
	if err := a.ClearLogs(); err != nil {
		t.Fatal(err)
	}
	entries := diagnosticEntries(t, a)
	if len(entries) != 6 {
		t.Fatalf("ClearLogs lost diagnostic history: %d entries", len(entries))
	}
	for i, level := range []string{"ACTIVITY", "ERROR", "INFO", "WARNING", "DEBUG"} {
		if entries[i].Level != level || entries[i].Source != "app" {
			t.Fatal(entries[i])
		}
	}
}
func TestAppDiagnosticsPersistWhenUIDatabaseFails(t *testing.T) {
	a := diagnosticTestApp(t)
	if err := a.uiStore.Close(); err != nil {
		t.Fatal(err)
	}
	a.log("error", "synthetic browser failure")
	entries := diagnosticEntries(t, a)
	if len(entries) != 2 || entries[0].Message != "synthetic browser failure" || !strings.Contains(entries[1].Message, "UI log persistence failed") {
		t.Fatal(entries)
	}
}

// The saved-settings log must name the effective Show Mode first-message mode,
// so a reader of logs.txt can tell why the bot did (not) open a chat.
func TestSaveGeneralSettingsLogsEffectiveFirstMessageMode(t *testing.T) {
	a := diagnosticTestApp(t)
	if err := a.SaveGeneralSettings(model.GeneralSettings{FirstMessageMode: "garbage"}); err != nil {
		t.Fatal(err)
	}
	entries := diagnosticEntries(t, a)
	last := entries[len(entries)-1]
	if last.Level != "INFO" || !strings.Contains(last.Message, "First=bot") {
		t.Fatalf("unknown saved mode must log the effective bot mode: %#v", last)
	}

	if err := a.SaveGeneralSettings(model.GeneralSettings{FirstMessageMode: model.FirstMessageUser}); err != nil {
		t.Fatal(err)
	}
	entries = diagnosticEntries(t, a)
	last = entries[len(entries)-1]
	if !strings.Contains(last.Message, "First=user") {
		t.Fatalf("user-writes-first save not logged: %#v", last)
	}
	if !strings.Contains(last.Message, "Poll=") || !strings.Contains(last.Message, "Action=") {
		t.Fatalf("timing values disappeared from the saved-settings log: %#v", last)
	}
}

// A reader must never observe a half-written JSON file: the payload is written
// to a sibling temp file and renamed into place, and nothing is left behind.
func TestSaveJSONIsAtomicAndLeavesNoTempFiles(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "state.json")
	payload := map[string]string{"blob": strings.Repeat("x", 200000)}

	done := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		defer close(done)
		for i := 0; i < 40; i++ {
			payload["n"] = strconv.Itoa(i)
			if err := store.SaveJSON(target, payload); err != nil {
				t.Errorf("save failed: %v", err)
				return
			}
		}
	}()
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-done:
				return
			default:
			}
			data, err := os.ReadFile(target)
			if err != nil {
				continue // not created yet, or the rename is in flight
			}
			var probe map[string]string
			if err := json.Unmarshal(data, &probe); err != nil {
				t.Errorf("reader saw a partial JSON file (%d bytes): %v", len(data), err)
				return
			}
		}
	}()
	wg.Wait()

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.Name() != "state.json" {
			t.Fatalf("temporary file left behind: %s", entry.Name())
		}
	}
	data, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	var final map[string]string
	if err := json.Unmarshal(data, &final); err != nil || final["n"] != "39" {
		t.Fatalf("final content wrong (bytes=%d): %v", len(data), err)
	}
}

// One unreadable state file must not fail dashboard stats (it refreshes every
// 3s), and the warning is logged once per path instead of on every refresh.
func TestCountSentMessagesSkipsCorruptStateOnce(t *testing.T) {
	a := diagnosticTestApp(t)
	good := filepath.Join(a.dataDir, "profiles", "good", "bot_state.json")
	if err := store.SaveJSON(good, model.BotState{
		"acct": model.AccountState{SentMessageCounts: map[string]int{"u1": 3, "u2": 2}},
	}); err != nil {
		t.Fatal(err)
	}
	broken := filepath.Join(a.dataDir, "profiles", "broken", "bot_state.json")
	if err := os.MkdirAll(filepath.Dir(broken), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(broken, []byte("{ not json"), 0o644); err != nil {
		t.Fatal(err)
	}

	for run := 0; run < 2; run++ {
		sent, users, snapTg, skipped, err := a.countSentMessages()
		if err != nil {
			t.Fatalf("run %d: an unreadable state file must not fail the stats: %v", run, err)
		}
		if sent != 5 || users != 2 || snapTg != 0 || skipped != 0 {
			t.Fatalf("run %d: readable profiles lost: sent=%d users=%d snap=%d skipped=%d", run, sent, users, snapTg, skipped)
		}
	}

	warnings := 0
	for _, entry := range diagnosticEntries(t, a) {
		if strings.Contains(entry.Message, "Skipped unreadable bot state") {
			warnings++
			if entry.Level != "WARN" {
				t.Fatalf("unexpected level for the skip warning: %#v", entry)
			}
		}
	}
	if warnings != 1 {
		t.Fatalf("skip warning logged %d time(s); want exactly 1", warnings)
	}
}
