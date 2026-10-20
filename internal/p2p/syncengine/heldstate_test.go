package syncengine

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/opensave/opensave/internal/delta"
)

func stampFile(t *testing.T, dir, rel string, at time.Time) {
	t.Helper()
	if err := os.Chtimes(filepath.Join(dir, filepath.FromSlash(rel)), at, at); err != nil {
		t.Fatal(err)
	}
}

func readSaveFile(t *testing.T, dir, rel string) string {
	t.Helper()
	b, _ := os.ReadFile(filepath.Join(dir, filepath.FromSlash(rel)))
	return string(b)
}

// The peer joins with an old copy of this device's save, stamped later than
// this device's current one, and nothing agreed between them yet. This device
// has the old copy in its history: the peer is behind, not diverged.
func TestHeldState_APeerHoldingAStateFromThisDevicesHistoryIsBehind(t *testing.T) {
	env := setupEngine(t)
	write(t, env.localDir, "slot1.sav", "v1")
	if _, err := env.engine.Snapshots.Create("game1", "", true); err != nil {
		t.Fatal(err)
	}
	write(t, env.localDir, "slot1.sav", "v2")
	write(t, env.remoteDir, "slot1.sav", "v1")
	stampFile(t, env.remoteDir, "slot1.sav", time.Now().Add(time.Hour))

	res, err := env.engine.SyncWithPeer(context.Background(), "game1", env.peer)
	if err != nil {
		t.Fatal(err)
	}
	if res.Status == "conflict" {
		t.Fatal("asked which to keep, though the peer holds a save this device has held")
	}
	if env.transport.pullTriggers == 0 {
		t.Errorf("the peer was not asked to take this device's save: %+v", res)
	}
	if got := readSaveFile(t, env.localDir, "slot1.sav"); got != "v2" {
		t.Errorf("this device's save became %q", got)
	}
	remote, _ := delta.BuildManifest(env.remoteDir)
	if got := env.store.GetAgreedHash("game1", env.peer.ID); got != remote.ManifestHash() {
		t.Errorf("agreed base = %q, want the state both have held", got)
	}
}

// The other side of it: this device holds the old copy, and the peer has held
// it. Asked, the peer says so, and this device takes the peer's save.
func TestHeldState_ThisDeviceHoldingAStateThePeerHasHeldIsBehind(t *testing.T) {
	env := setupEngine(t)
	write(t, env.localDir, "slot1.sav", "v1")
	stampFile(t, env.localDir, "slot1.sav", time.Now().Add(time.Hour))
	write(t, env.remoteDir, "slot1.sav", "v2")
	old, _ := delta.BuildManifest(env.localDir)
	var asked []string
	env.transport.heldByPeer = func(h string) bool {
		asked = append(asked, h)
		return h == manifestHeldKey(old)
	}

	res, err := env.engine.SyncWithPeer(context.Background(), "game1", env.peer)
	if err != nil {
		t.Fatal(err)
	}
	if res.Status == "conflict" {
		t.Fatal("asked which to keep, though the peer has held this device's save")
	}
	if len(asked) != 1 {
		t.Errorf("the peer was asked %d times, want once", len(asked))
	}
	if got := readSaveFile(t, env.localDir, "slot1.sav"); got != "v2" {
		t.Errorf("this device kept %q, want the peer's newer save", got)
	}
}

// Neither has held the other's save: two unrelated saves, asked about as
// before. So with a peer that predates the question.
func TestHeldState_UnrelatedSavesAreStillAsked(t *testing.T) {
	env := setupEngine(t)
	write(t, env.localDir, "slot1.sav", "a-run")
	if _, err := env.engine.Snapshots.Create("game1", "", true); err != nil {
		t.Fatal(err)
	}
	write(t, env.remoteDir, "slot1.sav", "b-run")
	env.transport.heldByPeer = func(string) bool { return false }

	res, err := env.engine.SyncWithPeer(context.Background(), "game1", env.peer)
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != "conflict" {
		t.Errorf("two unrelated saves were settled without asking: %+v", res)
	}
	if got := env.store.GetAgreedHash("game1", env.peer.ID); got != "" {
		t.Errorf("an agreed base %q was recorded for saves neither has held", got)
	}
}

// Asked only with nothing agreed: once there is a base, the question is not
// sent again.
func TestHeldState_NotAskedOnceThereIsABase(t *testing.T) {
	env := setupEngine(t)
	write(t, env.localDir, "slot1.sav", "v1")
	write(t, env.remoteDir, "slot1.sav", "v1")
	local, _ := delta.BuildManifest(env.localDir)
	if err := env.store.SetAgreedHash("game1", env.peer.ID, local.ManifestHash()); err != nil {
		t.Fatal(err)
	}
	write(t, env.localDir, "slot1.sav", "v2")
	asked := 0
	env.transport.heldByPeer = func(string) bool { asked++; return false }

	if _, err := env.engine.SyncWithPeer(context.Background(), "game1", env.peer); err != nil {
		t.Fatal(err)
	}
	if asked != 0 {
		t.Errorf("the peer was asked %d times with a base already agreed", asked)
	}
}

// Snapshots are listed with their file counts, newest first, and any holding
// fewer files than the save being matched are left out unread; a save is
// found in whichever snapshot holds it.
func TestHeldState_ReadsOnlySnapshotsLargeEnoughToMatch(t *testing.T) {
	env := setupEngine(t)
	var ids []string
	for _, f := range []string{"a.sav", "b.sav", "c.sav"} {
		write(t, env.localDir, f, f)
		snap, err := env.engine.Snapshots.Create("game1", "", true)
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, snap.ID)
	}

	counts, err := env.store.SnapshotFileCounts("game1", delta.PrimaryRoot, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(counts) != 2 || counts[0].SnapshotID != ids[2] || counts[0].Files != 3 ||
		counts[1].SnapshotID != ids[1] || counts[1].Files != 2 {
		t.Errorf("snapshots with at least 2 files = %+v, want %s (3) then %s (2)", counts, ids[2], ids[1])
	}

	hashOf := func(content string) string {
		dir := t.TempDir()
		write(t, dir, "x", content)
		m, _ := delta.BuildManifest(dir)
		return m.Files["x"].Hash
	}
	for _, c := range []struct {
		files map[string]string
		held  bool
	}{
		{map[string]string{"a.sav": hashOf("a.sav")}, true},
		{map[string]string{"a.sav": hashOf("a.sav"), "b.sav": hashOf("b.sav")}, true},
		{map[string]string{"a.sav": hashOf("a.sav"), "b.sav": hashOf("other")}, false},
		{map[string]string{"a.sav": "", "b.sav": "", "c.sav": "", "d.sav": ""}, false},
	} {
		if got := env.engine.HeldState("game1", heldKey(c.files)); got != c.held {
			t.Errorf("HeldState(%v) = %v, want %v", c.files, got, c.held)
		}
	}
	if env.engine.HeldState("game1", "not-a-key") {
		t.Error("a malformed key matched")
	}
}
