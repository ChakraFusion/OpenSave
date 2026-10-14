package watcher

import (
	"testing"
	"time"
)

// Stop waits for a catch-up already taking a snapshot, so whatever stopping
// is for — closing the database, quitting, removing the folder — does not
// happen under it. It returned at once, and the snapshot went on writing into
// a folder being removed (TestAddingAnOrdinarySaveLocationStillWorks on Linux
// CI: "directory not empty").
func TestStopWaitsForACatchUpUnderWay(t *testing.T) {
	saveDir := t.TempDir()
	col := newCollector()
	col.manifestHashes["game1"] = "a save from before" // so the catch-up snapshots
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
	if err := eng.Watch("game1", saveDir); err != nil {
		t.Fatal(err)
	}
	select {
	case <-inSnapshot:
	case <-time.After(15 * time.Second):
		t.Fatal("the catch-up never took its snapshot")
	}

	stopped := make(chan struct{})
	go func() {
		eng.Stop()
		close(stopped)
	}()
	select {
	case <-stopped:
		t.Fatal("Stop returned while the catch-up was still taking its snapshot")
	case <-time.After(500 * time.Millisecond):
	}
	close(release)
	select {
	case <-stopped:
	case <-time.After(10 * time.Second):
		t.Fatal("Stop did not return once the catch-up had finished")
	}
}
