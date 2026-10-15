package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"chitchatbot/internal/applog"
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
