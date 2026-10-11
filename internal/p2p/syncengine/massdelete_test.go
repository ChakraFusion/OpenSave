package syncengine

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// Both sides converge on n files, then the peer loses some of them.
func convergedThenPeerLoses(t *testing.T, n, lost int) *engineEnv {
	t.Helper()
	env := setupEngine(t)
	writeSmallFiles(t, env.localDir, n)
	writeSmallFiles(t, env.remoteDir, n)
	if res, err := env.engine.SyncWithPeer(context.Background(), "game1", env.peer); err != nil || res.Status != "in_sync" {
		t.Fatalf("setup sync = %+v, %v", res, err)
	}
	for i := 0; i < lost; i++ {
		if err := os.Remove(filepath.Join(env.remoteDir, "world", fmt.Sprintf("map_%04d.bin", i))); err != nil {
			t.Fatal(err)
		}
	}
	return env
}

func TestMassDeletion_IsHeldForADecision(t *testing.T) {
	env := convergedThenPeerLoses(t, 1000, 500)
	res, err := env.engine.SyncWithPeer(context.Background(), "game1", env.peer)
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != "conflict" {
		t.Fatalf("result = %+v, want the sync held as a conflict", res)
	}
	if n := countFiles(t, env.localDir); n != 1000 {
		t.Fatalf("this side has %d files; the 500 deletions were applied instead of held", n)
	}
	if _, ok := env.engine.ActiveConflicts()["game1"]; !ok {
		t.Error("no conflict registered to ask about")
	}

	// "Keep mine": the peer's missing files go back to it, nothing is deleted here.
	if _, err := env.engine.ResolveConflict(context.Background(), "game1", env.peer.ID, "keep-local"); err != nil {
		t.Fatal(err)
	}
	if _, err := env.engine.SyncWithPeer(context.Background(), "game1", env.peer); err != nil {
		t.Fatal(err)
	}
	if n := countFiles(t, env.localDir); n != 1000 {
		t.Fatalf("after keep-mine this side has %d files, want 1000", n)
	}
}

// Ordinary play deletes a few files; that still just syncs.
func TestMassDeletion_SmallDeletionsStillSync(t *testing.T) {
	env := convergedThenPeerLoses(t, 1000, 20)
	res, err := env.engine.SyncWithPeer(context.Background(), "game1", env.peer)
	if err != nil {
		t.Fatal(err)
	}
	if res.Status == "conflict" {
		t.Fatalf("20 of 1000 deletions were held: %+v", res)
	}
	if n := countFiles(t, env.localDir); n != 980 {
		t.Fatalf("this side has %d files, want the peer's 20 deletions applied (980)", n)
	}
}
