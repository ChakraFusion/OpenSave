package main

import (
	"strings"
	"testing"
	"time"
)

// The window asks for the service's address as soon as it loads, which is
// usually before startup has finished: with many games to watch, starting
// takes several seconds on a cold disk. Answering straight away handed it an
// empty address — "can't reach OpenSave's background service at http://",
// on the first launch after boot, and Retry worked (GitHub #17). The answer
// waits for startup instead.
func TestDaemonAddrWaitsForStartupToFinish(t *testing.T) {
	a := NewApp()
	answer := make(chan map[string]string, 1)
	go func() { answer <- a.DaemonAddr() }()

	select {
	case got := <-answer:
		t.Fatalf("DaemonAddr answered %v while startup was still running", got)
	case <-time.After(150 * time.Millisecond):
	}

	a.addr = "127.0.0.1:8383"
	close(a.ready)
	select {
	case got := <-answer:
		if got["addr"] != "127.0.0.1:8383" || got["error"] != "" {
			t.Errorf("DaemonAddr = %v, want the address startup found", got)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("DaemonAddr did not answer once startup finished")
	}
}

// A startup that never finishes ends in a reason, not a wait forever.
func TestDaemonAddrGivesUpOnAStartupThatNeverFinishes(t *testing.T) {
	was := startupWait
	startupWait = 50 * time.Millisecond
	defer func() { startupWait = was }()

	got := NewApp().DaemonAddr()
	if got["addr"] != "" || !strings.Contains(got["error"], "still starting") {
		t.Errorf("DaemonAddr = %v, want an error saying the service is still starting", got)
	}
}
