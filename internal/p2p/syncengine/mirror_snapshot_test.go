package syncengine

import (
	"context"
	"testing"
)

// The snapshot recorded under the peer's id after a pull records what it
// holds, like any snapshot here: the next snapshot reuses it rather than
// compressing the whole save again.
func TestSync_TheSnapshotAfterAPullRecordsItsFiles(t *testing.T) {
	env := setupEngine(t)
	write(t, env.remoteDir, "save.dat", "remote progress")
	write(t, env.remoteDir, "profile.dat", "remote profile")
	env.transport.latestSnap = &SnapshotInfo{ID: "snap_777", Timestamp: "2026-06-01T00:00:00.000Z", Comment: "peer snap"}

	if _, err := env.engine.SyncWithPeer(context.Background(), "game1", env.peer); err != nil {
		t.Fatal(err)
	}
	if _, err := env.store.GetSnapshot("snap_777"); err != nil {
		t.Fatalf("mirror snapshot not recorded: %v", err)
	}
	files, err := env.store.SnapshotFiles("snap_777")
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 2 {
		t.Errorf("the snapshot after the pull records %d file(s), want 2: %+v", len(files), files)
	}
}
