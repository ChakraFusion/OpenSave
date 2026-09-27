package syncengine

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/opensave/opensave/internal/delta"
)

// A file taken from the peer and deleted here straight away must count as
// shared, so the deletion is passed on. The record of what the two share used
// to come from a read of the folder at the start of the sync and another at
// the end; a file that arrived and was deleted in between was in neither, and
// the next sync, finding it only on the peer, fetched it back.
//
// "sync-complete" is reported once the pull's files are written and before
// the sync that pulled them ends: the window the deletion lands in.
func TestSync_AFileDeletedAsSoonAsItArrivesIsDeletedOnThePeer(t *testing.T) {
	env := setupEngine(t)
	ctx := context.Background()
	write(t, env.localDir, "base.sav", "shared")
	write(t, env.remoteDir, "base.sav", "shared")
	if res, err := env.engine.SyncWithPeer(ctx, "game1", env.peer); err != nil || res.Status != "in_sync" {
		t.Fatalf("setting up: %+v, %v", res, err)
	}

	write(t, env.remoteDir, "kill-me.sav", "arrives, and is deleted at once")
	env.transport.onSyncEvent = func(event string) {
		if event != "sync-complete" {
			return
		}
		if err := os.Remove(filepath.Join(env.localDir, "kill-me.sav")); err != nil {
			t.Errorf("the pull had not brought kill-me.sav: %v", err)
		}
	}
	if res, err := env.engine.SyncWithPeer(ctx, "game1", env.peer); err != nil || res.Direction != "pull" {
		t.Fatalf("the pull: %+v, %v", res, err)
	}
	env.transport.onSyncEvent = nil

	res, err := env.engine.SyncWithPeer(ctx, "game1", env.peer)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(env.localDir, "kill-me.sav")); err == nil {
		t.Errorf("the deleted file was fetched back from the peer (sync: %+v)", res)
	}
	if !slices.Contains(env.transport.deletedOnPeer, "kill-me.sav") {
		t.Errorf("the deletion never reached the peer (sync: %+v)", res)
	}
	if _, err := os.Stat(filepath.Join(env.remoteDir, "base.sav")); err != nil {
		t.Error("a file nobody deleted went missing on the peer")
	}
}

// What withPulled adds, and what it must not: a name this system cannot
// store is skipped by the pull, so it was never here — counted as shared, its
// absence would read as a deletion and be passed on to the peer.
func TestWithPulledLeavesOutWhatThePullSkipped(t *testing.T) {
	remote := delta.Manifest{Files: map[string]delta.FileEntry{
		"took.sav":  {Hash: "a"},
		"what?.sav": {Hash: "b"},
	}}
	got := withPulled(delta.Manifest{Files: map[string]delta.FileEntry{"had.sav": {}}}, remote,
		[]string{"took.sav", "what?.sav", "not-offered.sav"}, []string{"slots"})
	for _, p := range []string{"had.sav", "took.sav"} {
		if _, ok := got.Files[p]; !ok {
			t.Errorf("%s missing from %v", p, got.Files)
		}
	}
	if _, ok := got.Files["not-offered.sav"]; ok {
		t.Error("a path the peer does not have was counted as pulled")
	}
	if !slices.Contains(got.Dirs, "slots") {
		t.Errorf("a pulled folder is missing: %v", got.Dirs)
	}
	_, counted := got.Files["what?.sav"]
	if unstorable := delta.UnrepresentableName("what?.sav") != ""; counted == unstorable {
		t.Errorf("what?.sav counted as shared = %v on a system where it is unstorable = %v", counted, unstorable)
	}
}
