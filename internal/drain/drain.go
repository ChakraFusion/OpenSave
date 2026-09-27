// Package drain counts work in progress so that shutdown can wait for it.
package drain

import (
	"sync"
	"time"
)

// Group is a sync.WaitGroup for shutdown, without the one rule of
// WaitGroup's that shutdown cannot keep.
//
// A WaitGroup requires every Add that starts from zero to happen before Wait
// is called. Shutdown cannot promise that: a snapshot, an upload or a request
// begun a moment earlier can start its work just as Stop starts waiting. With
// a WaitGroup that is a race the detector reports, and one that can panic
// ("WaitGroup is reused before previous Wait has returned"). A Group allows
// it: work may start at any time, and Wait returns once nothing is in
// progress.
//
// The zero value is ready to use.
type Group struct {
	mu   sync.Mutex
	n    int
	idle chan struct{} // closed when n returns to zero; nil while it is zero
}

// Add records one piece of work starting. Every Add is ended by one Done.
func (g *Group) Add() {
	g.mu.Lock()
	if g.n == 0 {
		g.idle = make(chan struct{})
	}
	g.n++
	g.mu.Unlock()
}

// Done records one piece of work finishing.
func (g *Group) Done() {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.n == 0 {
		panic("drain: Done without a matching Add")
	}
	g.n--
	if g.n == 0 {
		close(g.idle)
		g.idle = nil
	}
}

// Go runs fn in a goroutine, counted from before this call returns.
func (g *Group) Go(fn func()) {
	g.Add()
	go func() {
		defer g.Done()
		fn()
	}()
}

// Wait blocks until no work is in progress, and reports true, or until
// timeout has passed, and reports false. A timeout of zero or less waits for
// as long as it takes.
//
// Work that starts while Wait is waiting is waited for too, as long as it
// starts before everything else has finished. Callers that need nothing new
// to start at all stop its sources first.
func (g *Group) Wait(timeout time.Duration) bool {
	var deadline <-chan time.Time
	if timeout > 0 {
		t := time.NewTimer(timeout)
		defer t.Stop()
		deadline = t.C
	}
	for {
		g.mu.Lock()
		if g.n == 0 {
			g.mu.Unlock()
			return true
		}
		idle := g.idle
		g.mu.Unlock()
		select {
		case <-idle:
			// Something may have started since; look again.
		case <-deadline:
			return false
		}
	}
}
