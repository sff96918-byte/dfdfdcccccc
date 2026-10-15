package applog

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func readEntries(t *testing.T, path string) []Entry {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var entries []Entry
	for _, line := range strings.Split(strings.TrimSuffix(string(data), "\n"), "\n") {
		var e Entry
		if err := json.Unmarshal([]byte(line), &e); err != nil {
			t.Fatalf("invalid log record: %v", err)
		}
		if _, err := time.Parse(time.RFC3339Nano, e.Time); err != nil {
			t.Fatal(err)
		}
		entries = append(entries, e)
	}
	return entries
}

func TestAppendAllLevelsAndPreserveSessions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "logs.txt")
	for _, level := range []string{"activity", "error", "info", "warning", "debug", "trace", ""} {
		if err := Append(path, level, "app", "first session"); err != nil {
			t.Fatal(err)
		}
	}
	// A separate Logger/session must append, never truncate or keep just 200 rows.
	for i := 0; i < 250; i++ {
		Logger{Path: path, Source: "wails"}.Info(fmt.Sprintf("next session %d", i))
	}
	got := readEntries(t, path)
	if len(got) != 257 {
		t.Fatalf("lost logs: got %d", len(got))
	}
	if got[0].Level != "ACTIVITY" || got[1].Level != "ERROR" || got[6].Level != "INFO" || got[7].Source != "wails" {
		t.Fatalf("bad metadata: %+v", got[:8])
	}
}

func TestCompleteMultilineUnicodeAndLongDiagnostics(t *testing.T) {
	path := filepath.Join(t.TempDir(), "logs.txt")
	msg := "browser page unavailable\nstack\tframe\r\nবাংলা " + strings.Repeat("x", 120000)
	if err := Append(path, "error", "app", msg); err != nil {
		t.Fatal(err)
	}
	got := readEntries(t, path)
	if len(got) != 1 || got[0].Message != msg {
		t.Fatal("multiline/Unicode/long diagnostic was changed, truncated or split")
	}
}

func TestConcurrentAppFrameworkAndStandardLogWriters(t *testing.T) {
	path := filepath.Join(t.TempDir(), "logs.txt")
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				if err := Append(path, "activity", fmt.Sprintf("source-%d", i), fmt.Sprintf("row-%d-%d", i, j)); err != nil {
					t.Error(err)
				}
			}
		}(i)
	}
	wg.Wait()
	entries := readEntries(t, path)
	if len(entries) != 800 {
		t.Fatalf("got %d records, want 800", len(entries))
	}
	seen := map[string]bool{}
	var previous time.Time
	for _, e := range entries {
		if seen[e.Message] {
			t.Fatal("duplicate record")
		}
		seen[e.Message] = true
		now, _ := time.Parse(time.RFC3339Nano, e.Time)
		if now.Before(previous) {
			t.Fatal("timestamps out of order")
		}
		previous = now
	}
}

func TestFrameworkAndStandardLibraryAdapters(t *testing.T) {
	path := filepath.Join(t.TempDir(), "logs.txt")
	l := Logger{Path: path, Source: "wails"}
	l.Print("print")
	l.Trace("trace")
	l.Debug("debug")
	l.Info("info")
	l.Warning("warning")
	l.Error("error")
	log.New(Logger{Path: path, Source: "go"}, "", 0).Println("startup failure")
	entries := readEntries(t, path)
	for i, level := range []string{"INFO", "TRACE", "DEBUG", "INFO", "WARNING", "ERROR", "STDLOG"} {
		if entries[i].Level != level {
			t.Fatal(entries[i])
		}
	}
	if entries[6].Source != "go" || entries[6].Message != "startup failure" {
		t.Fatal(entries[6])
	}
}

func TestDiskErrorsAreReturnedWithoutRecursion(t *testing.T) {
	dir := t.TempDir()
	for _, path := range []string{dir, filepath.Join(dir, "missing", "logs.txt")} {
		if err := Append(path, "error", "test", "diagnostic"); err == nil {
			t.Fatal("expected file failure")
		}
		if n, err := (Logger{Path: path, Source: "go"}).Write([]byte("failure")); n != 0 || err == nil {
			t.Fatal("writer hid file failure")
		}
		(Logger{Path: path, Source: "app"}).Error("failure") // must return, not recurse/panic
	}
}

func TestKnownCredentialsRedactedButErrorContextKept(t *testing.T) {
	for _, msg := range []string{
		"Browser failed Authorization: Bearer TOKEN_SECRET\ncontext retained",
		`Browser failed {"access_token":"TOKEN_SECRET","api_key":"API_SECRET","password":"PASS_SECRET"} context retained`,
		"Browser failed Cookie: sid=COOKIE_SECRET\ncontext retained",
		"Browser failed https://u:PASS_SECRET@proxy.test:8080/?api_key=API_SECRET&step=send context retained",
		"Browser failed Bearer TOKEN_SECRET context retained",
		"Browser failed ?token=TOKEN_SECRET&license_key=API_SECRET context retained",
	} {
		path := filepath.Join(t.TempDir(), "logs.txt")
		if err := Append(path, "error", "app", msg); err != nil {
			t.Fatal(err)
		}
		got := readEntries(t, path)[0].Message
		for _, secret := range []string{"TOKEN_SECRET", "API_SECRET", "PASS_SECRET", "COOKIE_SECRET"} {
			if strings.Contains(got, secret) {
				t.Fatalf("leaked known credential form: %s", secret)
			}
		}
		if !strings.Contains(got, "Browser failed") || !strings.Contains(got, "context retained") {
			t.Fatal("lost unrelated error context")
		}
	}
}

func TestPathBesideRuntimeData(t *testing.T) {
	root := t.TempDir()
	if got := Path(filepath.Join(root, "data")); got != filepath.Join(root, "logs.txt") {
		t.Fatal(got)
	}
	if got := Path("data"); got != "logs.txt" {
		t.Fatal(got)
	}
}

func TestFatalPersistsBeforeExit(t *testing.T) {
	if path := os.Getenv("CHITCHAT_SYNTHETIC_FATAL_TEST"); path != "" {
		Logger{Path: path, Source: "wails"}.Fatal("synthetic fatal")
		return
	}
	path := filepath.Join(t.TempDir(), "logs.txt")
	cmd := exec.Command(os.Args[0], "-test.run=^TestFatalPersistsBeforeExit$")
	cmd.Env = append(os.Environ(), "CHITCHAT_SYNTHETIC_FATAL_TEST="+path)
	if err := cmd.Run(); err == nil {
		t.Fatal("Fatal did not exit")
	} else if e, ok := err.(*exec.ExitError); !ok || e.ExitCode() != 1 {
		t.Fatal(err)
	}
	got := readEntries(t, path)
	if len(got) != 1 || got[0].Level != "FATAL" || got[0].Message != "synthetic fatal" {
		t.Fatal(got)
	}
}

// Windows GUI apps may not have a usable console. Console failure must never
// prevent the already-persisted diagnostic from reaching the file.
type unavailableConsole struct{}

func (unavailableConsole) Write([]byte) (int, error) { return 0, os.ErrInvalid }
func TestStandardLogPersistsBeforeUnavailableConsole(t *testing.T) {
	path := filepath.Join(t.TempDir(), "logs.txt")
	logger := log.New(io.MultiWriter(Logger{Path: path, Source: "go"}, unavailableConsole{}), "", 0)
	logger.Print("synthetic GUI startup error")
	entries := readEntries(t, path)
	if len(entries) != 1 || entries[0].Message != "synthetic GUI startup error" {
		t.Fatal(entries)
	}
}
