package cliapp

// HUNT-ONLY diagnostics for the Windows "daemon never became ready" flake.
// Enabled only when OPENSAVE_HUNT names a directory (the e2e harness sets it
// to the test's home). Not for upstream.

import (
	"fmt"
	"github.com/opensave/opensave/internal/huntmark"
	"os"
	"path/filepath"
	"runtime"
	"time"
)

var huntStart = time.Now()

// huntMark writes a timestamped phase marker to stderr, which the harness
// captures, so a slow or stuck phase is visible even when the log is empty.
func huntMark(phase string) { huntmark.Mark(phase) }

// HuntWatchDump writes every goroutine's stack to <dir>/stacks-<pid>.txt each
// time <dir>/dump.req appears (it is removed after), so the harness can see
// where a daemon that never became ready is stuck. Windows has no SIGQUIT.
func HuntWatchDump() {
	dir := os.Getenv("OPENSAVE_HUNT")
	if dir == "" {
		return
	}
	req := filepath.Join(dir, "dump.req")
	go func() {
		for {
			time.Sleep(100 * time.Millisecond)
			if _, err := os.Stat(req); err != nil {
				continue
			}
			buf := make([]byte, 8<<20)
			n := runtime.Stack(buf, true)
			out := filepath.Join(dir, fmt.Sprintf("stacks-%d.txt", os.Getpid()))
			_ = os.WriteFile(out, append([]byte(fmt.Sprintf("dumped at +%dms\n", time.Since(huntStart).Milliseconds())), buf[:n]...), 0o666)
			_ = os.Remove(req)
		}
	}()
}

// HuntMark is huntMark for package main.
func HuntMark(phase string) { huntMark(phase) }
