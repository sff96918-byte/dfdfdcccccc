package applog

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// Install only in a disposable subprocess, just as production installs once
// before any desktop workers. Do not mutate the test runner's global handles.
func TestProcessOutputCapture(t *testing.T) {
	if path := os.Getenv("CHITCHAT_OUTPUT_CAPTURE_TEST"); path != "" {
		c, err := CaptureOutput(path)
		if err != nil {
			t.Fatal(err)
		}
		fmt.Printf("rod: failed to continue paused proxy request: synthetic\n")
		fmt.Fprint(os.Stderr, "chromedp: synthetic startup warning\n")
		fmt.Print(strings.Repeat("x", 100000) + "\n")
		var wg sync.WaitGroup
		for i := 0; i < 8; i++ {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				for j := 0; j < 20; j++ {
					fmt.Printf("worker %d row %d\n", i, j)
				}
			}(i)
		}
		wg.Wait()
		fmt.Print("token=TOKEN_SECRET\n")
		fmt.Print("last partial line")
		if err := c.Close(); err != nil {
			t.Fatal(err)
		}
		if err := c.Close(); err != nil {
			t.Fatal(err)
		}
		return
	}
	path := filepath.Join(t.TempDir(), "logs.txt")
	cmd := exec.Command(os.Args[0], "-test.run=^TestProcessOutputCapture$")
	cmd.Env = append(os.Environ(), "CHITCHAT_OUTPUT_CAPTURE_TEST="+path)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("capture subprocess failed: %v %s", err, out)
	}
	entries := readEntries(t, path)
	if len(entries) != 165 {
		t.Fatalf("lost process output: %d records", len(entries))
	}
	var stdout, stderr, longLine, partial, redacted bool
	for _, e := range entries {
		if strings.Contains(e.Message, "TOKEN_SECRET") {
			t.Fatal("stdout credential leaked")
		}
		stdout = stdout || e.Level == "STDOUT" && strings.Contains(e.Message, "rod: failed")
		stderr = stderr || e.Level == "STDERR" && strings.Contains(e.Message, "chromedp:")
		longLine = longLine || len(e.Message) == 100001
		partial = partial || e.Message == "last partial line"
		redacted = redacted || strings.Contains(e.Message, "token=[REDACTED]")
	}
	if !stdout || !stderr || !longLine || !partial || !redacted {
		t.Fatal("capture omitted diagnostics or changed long/partial line")
	}
}

func TestGoroutineCrashAppendsDirectRuntimeDump(t *testing.T) {
	if path := os.Getenv("CHITCHAT_CRASH_CAPTURE_TEST"); path != "" {
		_, err := CaptureOutput(path)
		if err != nil {
			t.Fatal(err)
		}
		if err := Append(path, "info", "app", "before synthetic crash"); err != nil {
			t.Fatal(err)
		}
		go func() { panic("synthetic goroutine crash") }()
		select {}
	}
	path := filepath.Join(t.TempDir(), "logs.txt")
	cmd := exec.Command(os.Args[0], "-test.run=^TestGoroutineCrashAppendsDirectRuntimeDump$")
	cmd.Env = append(os.Environ(), "CHITCHAT_CRASH_CAPTURE_TEST="+path)
	if err := cmd.Run(); err == nil {
		t.Fatal("expected subprocess crash")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, text := range []string{"before synthetic crash", "panic: synthetic goroutine crash", "capture_test.go", "goroutine"} {
		if !strings.Contains(string(data), text) {
			t.Fatalf("fatal dump omitted %q", text)
		}
	}
}

func TestCaptureOutputRejectsUnavailableFileWithoutChangingConsole(t *testing.T) {
	out, errOut := os.Stdout, os.Stderr
	if c, err := CaptureOutput(t.TempDir()); err == nil || c != nil {
		t.Fatal("expected setup failure")
	}
	if os.Stdout != out || os.Stderr != errOut {
		t.Fatal("failed setup changed console handles")
	}
}

func TestDiskFailureWithCapturedStderrDoesNotRecurse(t *testing.T) {
	if path := os.Getenv("CHITCHAT_CAPTURE_DISK_FAILURE_TEST"); path != "" {
		c, err := CaptureOutput(path)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.Remove(path); err != nil {
			t.Fatal(err)
		}
		if err := os.Mkdir(path, 0700); err != nil {
			t.Fatal(err)
		}
		Logger{Path: path, Source: "app"}.Error("synthetic failed disk")
		fmt.Fprintln(os.Stderr, "synthetic captured error")
		if err := c.Close(); err != nil {
			t.Fatal(err)
		}
		return
	}
	path := filepath.Join(t.TempDir(), "logs.txt")
	cmd := exec.Command(os.Args[0], "-test.run=^TestDiskFailureWithCapturedStderrDoesNotRecurse$")
	cmd.Env = append(os.Environ(), "CHITCHAT_CAPTURE_DISK_FAILURE_TEST="+path)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("fallback process failed: %v %s", err, out)
	}
	if n := strings.Count(string(out), "logs.txt write failed:"); n != 2 {
		t.Fatalf("fallback missing or recursed: %d occurrences", n)
	}
}
