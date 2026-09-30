package snapshot

import (
	"testing"
	"time"
)

// Shutdown waits for the snapshots being written while more may be starting:
// the daemon's Stop does that as newly tracked games take their first
// snapshots. It must be safe. Counted with a sync.WaitGroup it was not — a
// wait begun while one snapshot ran, followed by the next starting from
// nothing, is a race the detector failed CI on, and one a WaitGroup may
// panic over.
func TestWaitingForSnapshotsWhileMoreStart(t *testing.T) {
	env := setup(t)
	writeSave(t, env.saveDir, "slot1.sav", "x")
	stop := make(chan struct{})
	waiting := make(chan struct{})
	go func() {
		defer close(waiting)
		for {
			select {
			case <-stop:
				return
			default:
			}
			env.mgr.WaitForInFlight(10 * time.Second)
		}
	}()
	for i := 0; i < 30; i++ {
		if _, err := env.mgr.Create("game1", "", true); err != nil {
			t.Fatal(err)
		}
	}
	close(stop)
	<-waiting
}
