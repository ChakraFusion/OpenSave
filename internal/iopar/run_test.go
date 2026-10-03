package iopar

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

// A drive that finishes the most with best operations at once: each
// operation takes longer the more run beside it, slowly up to best and
// steeply beyond.
func simulated(best int) func(i int) error {
	var active atomic.Int64
	return func(int) error {
		a := float64(active.Add(1))
		defer active.Add(-1)
		over := a / float64(best)
		lat := 200 * time.Microsecond
		if over > 1 {
			lat = time.Duration(float64(lat) * over * over * over)
		}
		time.Sleep(lat)
		return nil
	}
}

func TestRun_SettlesNearWhatTheDriveDoesBest(t *testing.T) {
	defer func(w time.Duration) { window = w }(window)
	window = 60 * time.Millisecond
	for _, kind := range []Kind{NVMe, SATA} {
		st, err := RunKind(context.Background(), kind, 40000, simulated(12), nil)
		if err != nil {
			t.Fatal(err)
		}
		t.Logf("%s: started at %d, settled at %d, peak %.0f/s in %v", kind, st.Start, st.Settled, st.Peak, st.Duration)
		if st.Settled < 5 || st.Settled > 28 {
			t.Errorf("%s: settled at %d, far from the 12 the drive does best", kind, st.Settled)
		}
	}
}

func TestRun_HardDiskOneAtATime(t *testing.T) {
	var active, most atomic.Int64
	st, err := RunKind(context.Background(), HDD, 300, func(int) error {
		a := active.Add(1)
		defer active.Add(-1)
		for {
			m := most.Load()
			if a <= m || most.CompareAndSwap(m, a) {
				break
			}
		}
		time.Sleep(100 * time.Microsecond)
		return nil
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if most.Load() != 1 || st.Settled != 1 {
		t.Errorf("a hard disk ran %d at once (settled %d), want 1", most.Load(), st.Settled)
	}
}

func TestRun_EveryItemOnceAndProgress(t *testing.T) {
	const n = 5000
	var seen [n]atomic.Int32
	var lastDone int
	_, err := RunKind(context.Background(), NVMe, n, func(i int) error {
		seen[i].Add(1)
		return nil
	}, func(done, total int) {
		if total != n || done < lastDone {
			t.Errorf("progress %d/%d after %d", done, total, lastDone)
		}
		lastDone = done
	})
	if err != nil {
		t.Fatal(err)
	}
	for i := range seen {
		if seen[i].Load() != 1 {
			t.Fatalf("item %d done %d times", i, seen[i].Load())
		}
	}
	if lastDone != n {
		t.Errorf("progress ended at %d of %d", lastDone, n)
	}
}

func TestRun_StopsAtTheFirstError(t *testing.T) {
	boom := errors.New("disk full")
	var calls atomic.Int64
	_, err := RunKind(context.Background(), SATA, 100000, func(i int) error {
		calls.Add(1)
		if i == 50 {
			return boom
		}
		time.Sleep(50 * time.Microsecond)
		return nil
	}, nil)
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v, want the work's error", err)
	}
	if calls.Load() > 5000 {
		t.Errorf("went on for %d items after the error", calls.Load())
	}
}
