package applog

import (
	"bufio"
	"fmt"
	"os"
	"runtime/debug"
	"sync"
)

// Console writes in the shared browser drivers use fmt.Printf, not log.Printf.
// Capture redirects only this desktop process's Go stdout/stderr handles; no
// browser/worker/chat implementation or subprocess transport is changed.
type Capture struct {
	oldOut, oldErr *os.File
	out, err       *os.File
	wg             sync.WaitGroup
	once           sync.Once
	closeErr       error
}

var fallbackMu sync.RWMutex
var fallback = os.Stderr

func reportWriteFailure(err error) {
	fallbackMu.RLock()
	defer fallbackMu.RUnlock()
	fmt.Fprintf(fallback, "logs.txt write failed: %v\n", err)
}

// CaptureOutput must be installed once during desktop startup, before workers.
// Ordinary records are JSON lines. Go's fatal crash dump is appended directly
// by the runtime in plain text, so it survives even a panic in another goroutine
// (a background pipe reader cannot reliably flush after a fatal runtime exit).
func CaptureOutput(path string) (*Capture, error) {
	outRead, outWrite, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	errRead, errWrite, err := os.Pipe()
	if err != nil {
		outRead.Close()
		outWrite.Close()
		return nil, err
	}
	cleanup := func() { outRead.Close(); outWrite.Close(); errRead.Close(); errWrite.Close() }
	crash, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		cleanup()
		return nil, err
	}
	// SetCrashOutput duplicates the handle; closing our copy is documented safe.
	err = debug.SetCrashOutput(crash, debug.CrashOptions{})
	closeErr := crash.Close()
	if err != nil {
		cleanup()
		return nil, err
	}
	if closeErr != nil {
		debug.SetCrashOutput(nil, debug.CrashOptions{})
		cleanup()
		return nil, closeErr
	}
	c := &Capture{oldOut: os.Stdout, oldErr: os.Stderr, out: outWrite, err: errWrite}
	fallbackMu.Lock()
	fallback = c.oldErr
	fallbackMu.Unlock()
	c.wg.Add(2)
	go c.consume(path, "STDOUT", outRead)
	go c.consume(path, "STDERR", errRead)
	os.Stdout = outWrite
	os.Stderr = errWrite
	return c, nil
}

func (c *Capture) consume(path, level string, r *os.File) {
	defer c.wg.Done()
	defer r.Close()
	// ReadString has no Scanner's 64 KiB truncation limit. Preserve partial final
	// lines on shutdown and keep streams separate for error source attribution.
	reader := bufio.NewReader(r)
	for {
		text, err := reader.ReadString('\n')
		if len(text) > 0 {
			if writeErr := Append(path, level, "process", text); writeErr != nil {
				reportWriteFailure(writeErr)
			}
		}
		if err != nil {
			return
		}
	}
}

// Close drains both streams on final process shutdown. It does
// not truncate the log, alter chat state, or shut down a browser/worker itself.
func (c *Capture) Close() error {
	c.once.Do(func() {
		// Do not reassign globals while a cancelled worker might still be exiting.
		if err := c.out.Close(); err != nil {
			c.closeErr = err
		}
		if err := c.err.Close(); err != nil && c.closeErr == nil {
			c.closeErr = err
		}
		c.wg.Wait()
		if err := debug.SetCrashOutput(nil, debug.CrashOptions{}); err != nil && c.closeErr == nil {
			c.closeErr = err
		}
	})
	return c.closeErr
}
