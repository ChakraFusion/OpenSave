package snapshot

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Shutdown waits for a restore or a branch switch under way, as for a
// snapshot being written: one cut off part-way leaves the save folder half
// restored.
func TestShutdownWaitsForARestoreUnderWay(t *testing.T) {
	m := &Manager{}
	done := m.writing("game")
	if m.WaitForInFlight(50 * time.Millisecond) {
		t.Fatal("shutdown did not wait for the restore under way")
	}
	done()
	if !m.WaitForInFlight(time.Second) {
		t.Fatal("still waiting after the restore finished")
	}

	// And with a write gate, released along with it.
	released := false
	m.WriteGate = func(string) func() { return func() { released = true } }
	done = m.writing("game")
	if m.WaitForInFlight(50 * time.Millisecond) {
		t.Fatal("shutdown did not wait for the restore under way (gated)")
	}
	done()
	if !released || !m.WaitForInFlight(time.Second) {
		t.Fatalf("released=%v; the gate or the count was not let go", released)
	}
}

// Archives left under a staging name by a process that ended part-way are
// removed at the next start, once they have sat unchanged long enough not to
// be one still being written.
func TestStaleStagingArchivesAreRemoved(t *testing.T) {
	env := setup(t)
	m := env.mgr
	dir := filepath.Join(env.backups, "game1", "main")
	if err := os.MkdirAll(dir, 0o777); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-2 * staleStagingAge)
	write := func(name string, at time.Time) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte("partial"), 0o666); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(p, at, at); err != nil {
			t.Fatal(err)
		}
		return p
	}
	staleSnap := write(".staging-123-1.zip", old)
	staleImport := write(".import-1700000000000-0.tmp", old)
	writing := write(".staging-123-2.zip", time.Now())
	real := write("snap_1700000000000.zip", old)

	if n := m.RemoveStaleStaging(); n != 2 {
		t.Errorf("removed %d, want 2", n)
	}
	for _, p := range []string{staleSnap, staleImport} {
		if _, err := os.Stat(p); err == nil {
			t.Errorf("%s left behind", filepath.Base(p))
		}
	}
	for _, p := range []string{writing, real} {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("%s removed: %v", filepath.Base(p), err)
		}
	}
}
