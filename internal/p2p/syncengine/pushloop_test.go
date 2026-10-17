package syncengine

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/opensave/opensave/internal/delta"
)

// aba sets up this device's side of #71's reproduction: the two devices once
// held the same save, this device now holds "a-turn", the peer holds "b-turn",
// and the agreed base is the peer's state — bytes the peer held before and
// wrote again, which #42's rule reads as "the peer has not changed", so this
// device decides to push.
func aba(t *testing.T) *engineEnv {
	t.Helper()
	env := setupEngine(t)
	write(t, env.localDir, "slot1.sav", "start")
	write(t, env.remoteDir, "slot1.sav", "start")
	if res, err := env.engine.SyncWithPeer(context.Background(), "game1", env.peer); err != nil || res.Status != "in_sync" {
		t.Fatalf("setup: %+v %v", res, err)
	}
	write(t, env.localDir, "slot1.sav", "a-turn")
	write(t, env.remoteDir, "slot1.sav", "b-turn")
	remote, err := delta.BuildManifest(env.remoteDir)
	if err != nil {
		t.Fatal(err)
	}
	if err := env.store.SetAgreedHash("game1", env.peer.ID, remote.ManifestHash()); err != nil {
		t.Fatal(err)
	}
	return env
}

// The peer answers every push by asking this device to pull, neither side
// changing: the loop in #71. It used to go on forever; now the second answered
// push is a conflict, with both saves as they were and no further trigger.
func TestPushLoop_TwoDevicesPushingToEachOtherIsAConflict(t *testing.T) {
	env := aba(t)
	ctx := context.Background()

	res, err := env.engine.SyncWithPeer(ctx, "game1", env.peer)
	if err != nil || res.Status != "triggered_peer_pull" {
		t.Fatalf("this device's own push: %+v %v", res, err)
	}
	for round := 1; round <= pushLoopRounds; round++ {
		// The peer pushed back instead of pulling.
		env.engine.NoteTriggered("game1", env.peer.ID)
		res, err = env.engine.SyncWithPeer(ctx, "game1", env.peer)
		if err != nil {
			t.Fatal(err)
		}
		if round < pushLoopRounds && res.Status != "triggered_peer_pull" {
			t.Fatalf("answered push %d: %+v, want another push before it counts as a loop", round, res)
		}
	}
	if res.Status != "conflict" {
		t.Fatalf("after %d answered pushes: %+v, want a conflict", pushLoopRounds, res)
	}
	if got := env.transport.pullTriggers; got != pushLoopRounds {
		t.Errorf("the peer was asked to pull %d times, want %d: the last answered push must not trigger again", got, pushLoopRounds)
	}
	if _, open := env.engine.ActiveConflicts()["game1"]; !open {
		t.Error("no conflict recorded for the user to decide")
	}
	if got := readFile(t, env.localDir, "slot1.sav"); got != "a-turn" {
		t.Errorf("this device's save became %q", got)
	}
	if got := readFile(t, env.remoteDir, "slot1.sav"); got != "b-turn" {
		t.Errorf("the peer's save became %q", got)
	}
}

// The same push again, but not as the peer's answer — this device syncing on
// its own while the peer has not pulled yet — is a retry, not a loop.
func TestPushLoop_RetryingOnItsOwnIsNoLoop(t *testing.T) {
	env := aba(t)
	for i := 0; i < pushLoopRounds+2; i++ {
		res, err := env.engine.SyncWithPeer(context.Background(), "game1", env.peer)
		if err != nil || res.Status != "triggered_peer_pull" {
			t.Fatalf("own sync %d: %+v %v, want a plain push", i, res, err)
		}
	}
}

// A peer that changed its save since the last push did not refuse it: the
// watch starts over.
func TestPushLoop_APeerThatChangedStartsOver(t *testing.T) {
	env := aba(t)
	ctx := context.Background()
	if res, _ := env.engine.SyncWithPeer(ctx, "game1", env.peer); res.Status != "triggered_peer_pull" {
		t.Fatalf("setup: %+v", res)
	}
	env.engine.NoteTriggered("game1", env.peer.ID)
	if res, _ := env.engine.SyncWithPeer(ctx, "game1", env.peer); res.Status != "triggered_peer_pull" {
		t.Fatalf("first answered push: %+v", res)
	}
	// The peer saves again: an older file with new content, so this device
	// still pushes, but about a different pair of states.
	write(t, env.remoteDir, "extra.sav", "new on the peer")
	remote, err := delta.BuildManifest(env.remoteDir)
	if err != nil {
		t.Fatal(err)
	}
	if err := env.store.SetAgreedHash("game1", env.peer.ID, remote.ManifestHash()); err != nil {
		t.Fatal(err)
	}
	env.engine.NoteTriggered("game1", env.peer.ID)
	res, err := env.engine.SyncWithPeer(ctx, "game1", env.peer)
	if err != nil {
		t.Fatal(err)
	}
	if res.Status == "conflict" {
		t.Errorf("a peer whose save changed was taken for one pushing back unchanged: %+v", res)
	}
}

func readFile(t *testing.T, dir, rel string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(rel)))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
