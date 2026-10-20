package iopar

import (
	"context"
	"sync"
	"sync/atomic"
	"time"
)

// The adjusting: every window the number of operations finished is
// measured, and the number running at once moves the way that made it
// faster — on while it keeps helping, back when it hurt. A drive that slows
// as it goes, its cache full or a scanner busy, gets fewer; one with more to
// give gets more. Once it settles it still looks the other way now and then,
// so a change later in a long run is followed too.

const (
	// maxAtOnce bounds how many run at once.
	maxAtOnce = 64
	// Changes smaller than this are noise, not a reason to move.
	better = 1.05
	worse  = 0.95
	// probeAfter windows without change before looking the other way.
	probeAfter = 4
)

// window is how long each measurement runs; a variable, for tests.
var window = 750 * time.Millisecond

// Stats is how a run went, for the log.
type Stats struct {
	Kind     Kind
	Start    int // operations at once to begin with
	Settled  int // at the end
	Peak     float64
	Duration time.Duration
}

// Run calls work for each of n items, as many at a time as suits the drive
// path is on. progress, if not nil, is told how many are done now and then
// and at the end. The first error stops the run and is returned; work
// already started finishes first.
func Run(ctx context.Context, path string, n int, work func(i int) error, progress func(done, total int)) (Stats, error) {
	return RunKind(ctx, DriveKind(path), n, work, progress)
}

// RunFixed runs work with exactly at operations at once, not adjusting: for
// measuring what a drive does at each number.
func RunFixed(ctx context.Context, at, n int, work func(i int) error, progress func(done, total int)) (Stats, error) {
	return run(ctx, Unknown, at, false, n, work, progress)
}

// RunKind is Run for a drive whose kind is known.
func RunKind(ctx context.Context, kind Kind, n int, work func(i int) error, progress func(done, total int)) (Stats, error) {
	return run(ctx, kind, kind.start(), kind.adapts(), n, work, progress)
}

func run(ctx context.Context, kind Kind, start int, adapts bool, n int, work func(i int) error, progress func(done, total int)) (Stats, error) {
	started := time.Now()
	st := Stats{Kind: kind, Start: start}
	if n <= 0 {
		return st, nil
	}
	p := &pool{limit: min(start, n)}
	p.cond = sync.NewCond(&p.mu)
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	var next, done atomic.Int64
	var firstErr error
	var errOnce sync.Once
	fail := func(err error) {
		errOnce.Do(func() {
			firstErr = err
			cancel()
			p.cond.Broadcast() // wake the ones waiting, to stop
		})
	}

	workers := min(maxAtOnce, n)
	if !adapts {
		workers = p.limit
	}
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				if !p.acquire(ctx) {
					return
				}
				i := int(next.Add(1) - 1)
				if i >= n {
					p.release()
					return
				}
				if err := work(i); err != nil {
					p.release()
					fail(err)
					return
				}
				done.Add(1)
				p.release()
			}
		}()
	}

	// Adjusting, and telling progress, until the workers are done.
	finished := make(chan struct{})
	go func() {
		wg.Wait()
		close(finished)
	}()
	tick := time.NewTicker(window)
	defer tick.Stop()
	c := climber{dir: 1}
	last, lastAt := int64(0), time.Now()
loop:
	for {
		select {
		case <-finished:
			break loop
		case now := <-tick.C:
			d := done.Load()
			rate := float64(d-last) / now.Sub(lastAt).Seconds()
			last, lastAt = d, now
			if rate > st.Peak {
				st.Peak = rate
			}
			if progress != nil {
				progress(int(d), n)
			}
			if adapts {
				p.setLimit(c.next(p.getLimit(), rate))
			}
		}
	}
	if progress != nil {
		progress(int(done.Load()), n)
	}
	st.Settled = p.getLimit()
	st.Duration = time.Since(started)
	if firstErr != nil {
		return st, firstErr
	}
	return st, ctx.Err()
}

// climber moves the number running at once towards where the most finishes.
type climber struct {
	dir      int // +1 more, -1 fewer
	prevRate float64
	still    int // windows without a move
}

func (c *climber) next(limit int, rate float64) int {
	step := max(1, limit/4)
	switch {
	case c.prevRate == 0:
		// The first window: try more.
	case rate >= c.prevRate*better:
		// That helped: go on the same way.
		c.still = 0
	case rate <= c.prevRate*worse:
		// That hurt: back the other way.
		c.dir = -c.dir
		c.still = 0
	default:
		// No difference worth the name: stay, and look the other way after
		// a while.
		c.still++
		if c.still < probeAfter {
			c.prevRate = rate
			return limit
		}
		c.still = 0
		c.dir = -c.dir
	}
	c.prevRate = rate
	limit += c.dir * step
	if limit < 1 {
		limit, c.dir = 1, 1
	}
	if limit > maxAtOnce {
		limit, c.dir = maxAtOnce, -1
	}
	return limit
}

// pool lets at most limit workers work at once; the limit can move while
// they do.
type pool struct {
	mu     sync.Mutex
	cond   *sync.Cond
	active int
	limit  int
}

func (p *pool) acquire(ctx context.Context) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	for p.active >= p.limit {
		if ctx.Err() != nil {
			return false
		}
		p.cond.Wait()
	}
	if ctx.Err() != nil {
		return false
	}
	p.active++
	return true
}

func (p *pool) release() {
	p.mu.Lock()
	p.active--
	p.mu.Unlock()
	p.cond.Broadcast()
}

func (p *pool) setLimit(n int) {
	p.mu.Lock()
	p.limit = n
	p.mu.Unlock()
	p.cond.Broadcast()
}

func (p *pool) getLimit() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.limit
}
