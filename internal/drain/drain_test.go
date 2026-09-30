package drain

import (
	"sync/atomic"
	"testing"
	"time"
)

func TestWaitWithNothingInProgressReturnsAtOnce(t *testing.T) {
	var g Group
	if !g.Wait(time.Second) {
		t.Fatal("Wait timed out with nothing in progress")
	}
}

func TestWaitWaitsForWorkInProgress(t *testing.T) {
	var g Group
	var finished atomic.Bool
	g.Go(func() {
		time.Sleep(50 * time.Millisecond)
		finished.Store(true)
	})
	if !g.Wait(5 * time.Second) {
		t.Fatal("Wait timed out")
	}
	if !finished.Load() {
		t.Fatal("Wait returned before the work finished")
	}
}

func TestWaitGivesUpAtTheTimeout(t *testing.T) {
	var g Group
	g.Add()
	defer g.Done()
	start := time.Now()
	if g.Wait(30 * time.Millisecond) {
		t.Fatal("Wait reported everything finished while work was in progress")
	}
	if time.Since(start) > 2*time.Second {
		t.Fatal("Wait overran its timeout")
	}
}

// What a WaitGroup forbids and shutdown does: work starting from nothing
// while Wait is already waiting. Under the race detector a WaitGroup fails
// this; a Group must not, however often it runs.
func TestWorkMayStartWhileWaitIsWaiting(t *testing.T) {
	for i := 0; i < 200; i++ {
		var g Group
		started := make(chan struct{})
		waited := make(chan bool)
		go func() {
			close(started)
			waited <- g.Wait(5 * time.Second)
		}()
		<-started
		g.Go(func() {})
		if !<-waited {
			t.Fatal("Wait timed out")
		}
	}
}

// Work that starts while other work is still running is waited for as well.
func TestWaitCoversWorkStartedBeforeTheLastFinished(t *testing.T) {
	var g Group
	var second atomic.Bool
	release := make(chan struct{})
	g.Go(func() { <-release })
	done := make(chan bool)
	go func() { done <- g.Wait(5 * time.Second) }()
	time.Sleep(10 * time.Millisecond)
	g.Go(func() {
		time.Sleep(40 * time.Millisecond)
		second.Store(true)
	})
	close(release)
	if !<-done {
		t.Fatal("Wait timed out")
	}
	if !second.Load() {
		t.Fatal("Wait returned while the second piece of work was running")
	}
}

func TestDoneWithoutAddPanics(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("Done without Add did not panic")
		}
	}()
	var g Group
	g.Done()
}
