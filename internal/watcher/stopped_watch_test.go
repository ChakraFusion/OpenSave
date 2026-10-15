package watcher

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// A watch stopped while its snapshot is being taken keeps the snapshot and
// announces nothing.
//
// A snapshot can take longer than stopping waits for the watch, so it finished
// after the watch had been given up, and announced the change: the daemon then
// synced a game whose auto-sync had just been switched off. On Windows CI that
// was the sync nobody expected in
// TestMultiRootScenario_ALateReportDoesNotHideADivergence ("the watcher ...
// did not stop within 5s and was abandoned", then "auto-snapshot created" and
// a sync).
func TestAWatchStoppedDuringItsSnapshotAnnouncesNothing(t *testing.T) {
	saveDir := t.TempDir()
	col := newCollector()
	cb := col.callbacks()
	inSnapshot := make(chan struct{}, 1)
	release := make(chan struct{})
	take := cb.CreateSnapshot
	cb.CreateSnapshot = func(gameID string) error {
		select {
		case inSnapshot <- struct{}{}:
		default:
		}
		<-release
		return take(gameID)
	}
	eng := New(cb)
	defer eng.Stop()
	if err := eng.Watch("game1", saveDir); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(saveDir, "slot1.sav"), []byte("played"), 0o666); err != nil {
		t.Fatal(err)
	}
	select {
	case <-inSnapshot:
	case <-time.After(15 * time.Second):
		t.Fatal("the change was never snapshotted")
	}

	stopped := make(chan struct{})
	go func() {
		eng.Unwatch("game1")
		close(stopped)
	}()
	time.Sleep(300 * time.Millisecond) // the watch is cancelled; its snapshot still runs
	close(release)
	<-stopped
	time.Sleep(300 * time.Millisecond)

	if n := col.snapshotCount(); n != 1 {
		t.Errorf("%d snapshots, want the one being taken kept", n)
	}
	if n := col.changedCount(); n != 0 {
		t.Errorf("the change was announced %d time(s) after its watch was stopped", n)
	}
}
