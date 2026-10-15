// Package applog stores desktop diagnostics independently of the UI database.
// Each append is opened, written and closed; no buffered history is lost on exit.
package applog

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

// All app, standard-library and Wails writers share this lock, even if their
// Logger values differ. O_APPEND also avoids truncating earlier sessions.
var appendMu sync.Mutex

// Path returns logs.txt beside the existing runtime data directory. The normal
// desktop launcher uses <project>/data, so the file is <project>/logs.txt.
func Path(dataDir string) string { return filepath.Join(filepath.Dir(dataDir), "logs.txt") }

type Entry struct {
	Time    string `json:"time"`
	Level   string `json:"level"`
	Source  string `json:"source"`
	Message string `json:"message"`
}

var (
	headerSecret = regexp.MustCompile(`(?im)\b(authorization|proxy-authorization|cookie|set-cookie)\s*[:=]\s*[^\r\n]+`)
	fieldSecret  = regexp.MustCompile(`(?i)(["']?\b(?:access[_-]?token|refresh[_-]?token|id[_-]?token|token|ai[_-]?api[_-]?key|api[_-]?key|license[_-]?key|password|passwd|secret|session[_-]?id|authorization|proxy-authorization|cookie|set-cookie)\b["']?\s*[:=]\s*)("[^"\r\n]*"|'[^'\r\n]*'|[^\s,;&]+)`)
	bearerSecret = regexp.MustCompile(`(?i)\bBearer\s+[a-z0-9._~+/-]+=*`)
	proxySecret  = regexp.MustCompile(`(?i)\b(https?|socks5h?|socks4a?)://[^\s/@]+@`)
)

// Redact known credential forms before writing diagnostics. This is a safeguard,
// not a promise to identify arbitrary private chat text or unlabeled secrets.
func Redact(message string) string {
	message = headerSecret.ReplaceAllString(message, "$1: [REDACTED]")
	message = fieldSecret.ReplaceAllString(message, "${1}[REDACTED]")
	message = bearerSecret.ReplaceAllString(message, "Bearer [REDACTED]")
	return proxySecret.ReplaceAllString(message, "${1}://[REDACTED]@")
}

func Append(path, level, source, message string) error {
	appendMu.Lock()
	defer appendMu.Unlock()
	level = strings.ToUpper(strings.TrimSpace(level))
	if level == "" {
		level = "INFO"
	}
	// JSON lines keep multiline errors/stacks complete without permitting fake
	// timestamp/level entries through embedded newline or control characters.
	data, err := json.Marshal(Entry{
		Time: time.Now().UTC().Format(time.RFC3339Nano), Level: level,
		Source: source, Message: Redact(message),
	})
	if err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	data = append(data, '\n')
	n, writeErr := f.Write(data)
	closeErr := f.Close()
	if writeErr != nil {
		return writeErr
	}
	if n != len(data) {
		return io.ErrShortWrite
	}
	return closeErr
}

// Logger satisfies both io.Writer and Wails' Logger interface without adding a
// desktop/framework dependency to this package. Failed disk writes are reported
// to stderr, never recursively fed back into the failing file or UI store.
type Logger struct {
	Path   string
	Source string
}

func (l Logger) Log(level, message string) {
	if err := Append(l.Path, level, l.Source, message); err != nil {
		reportWriteFailure(err)
	}
}
func (l Logger) Write(p []byte) (int, error) {
	if err := Append(l.Path, "STDLOG", l.Source, strings.TrimRight(string(p), "\r\n")); err != nil {
		reportWriteFailure(err)
		return 0, err
	}
	return len(p), nil
}
func (l Logger) Print(message string)   { l.Log("INFO", message) }
func (l Logger) Trace(message string)   { l.Log("TRACE", message) }
func (l Logger) Debug(message string)   { l.Log("DEBUG", message) }
func (l Logger) Info(message string)    { l.Log("INFO", message) }
func (l Logger) Warning(message string) { l.Log("WARNING", message) }
func (l Logger) Error(message string)   { l.Log("ERROR", message) }
func (l Logger) Fatal(message string)   { l.Log("FATAL", message); os.Exit(1) }
