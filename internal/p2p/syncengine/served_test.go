package syncengine

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/opensave/opensave/internal/delta"
)

// halfWrittenPull sets up the case served.go exists for: both devices agreed
// on v1; the game here wrote half of v2 and the peer pulled that half-written
// state; then the game finished. noteServed says whether this device
// remembered serving the half state.
func halfWrittenPull(t *testing.T, noteServed bool) *engineEnv {
	t.Helper()
	env := setupEngine(t)
	files := []string{"a.sav", "b.sav", "c.sav", "d.sav"}
	hourAgo := time.Now().Add(-time.Hour)
	for _, dir := range []string{env.localDir, env.remoteDir} {
		for _, f := range files {
			write(t, dir, f, "v1 of "+f)
			_ = os.Chtimes(filepath.Join(dir, f), hourAgo, hourAgo)
		}
	}
	v1 := mustManifest(t, env.localDir)
	if err := env.store.SetSyncState("game1", env.peer.ID, files, nil); err != nil {
		t.Fatal(err)
	}
	_ = env.store.SetAgreedHash("game1", env.peer.ID, v1.ManifestHash())

	// The game writes half its save, and the peer asks for it then.
	for _, f := range files[:2] {
		write(t, env.localDir, f, "v2 of "+f)
	}
	half := mustManifest(t, env.localDir)
	if noteServed {
		env.engine.NoteServed("game1", env.peer.ID, half)
	}
	for _, f := range files[:2] { // the peer now holds exactly that
		write(t, env.remoteDir, f, "v2 of "+f)
	}
	// And the game finishes here.
	for _, f := range files[2:] {
		write(t, env.localDir, f, "v2 of "+f)
	}
	return env
}

func TestServed_APeerHoldingWhatWasServedHasNotDiverged(t *testing.T) {
	env := halfWrittenPull(t, true)
	res, err := env.engine.SyncWithPeer(context.Background(), "game1", env.peer)
	if err != nil {
		t.Fatal(err)
	}
	if res.Status == "conflict" {
		t.Fatal("the peer holds a state this device served it, yet a conflict was raised")
	}
	if res.Status != "triggered_peer_pull" {
		t.Errorf("status = %q, want the finished save pushed", res.Status)
	}
}

// The control: without the record the same situation is a conflict — which is
// what it was before served.go, and what makes the test above mean something.
func TestServed_WithoutTheRecordItIsAConflict(t *testing.T) {
	env := halfWrittenPull(t, false)
	res, err := env.engine.SyncWithPeer(context.Background(), "game1", env.peer)
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != "conflict" {
		t.Fatalf("status = %q: the control no longer reproduces the conflict, so the test above proves nothing", res.Status)
	}
}

// A record from before a later agreement names a state older than it. Banking
// it would move the base backwards, so it must not count.
func TestServed_ARecordFromBeforeALaterAgreementIsIgnored(t *testing.T) {
	env := halfWrittenPull(t, true)
	// Some later agreement, on a state neither side holds now.
	_ = env.store.SetAgreedHash("game1", env.peer.ID, "a-later-agreement")

	if env.engine.servedUnderBase("game1", env.peer.ID, mustManifest(t, env.remoteDir).ManifestHash(), "a-later-agreement") {
		t.Fatal("a state served under an earlier base was accepted under a later one")
	}
	res, err := env.engine.SyncWithPeer(context.Background(), "game1", env.peer)
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != "conflict" {
		t.Errorf("status = %q: a stale record moved the base backwards", res.Status)
	}
}

func TestServed_RecordsArePerPeerAndBounded(t *testing.T) {
	env := setupEngine(t)
	m := func(content string) delta.Manifest {
		return delta.Manifest{Files: map[string]delta.FileEntry{"a": {Hash: content}}}
	}
	env.engine.NoteServed("game1", "", m("nobody")) // an unidentified asker records nothing
	if env.engine.servedUnderBase("game1", "", m("nobody").ManifestHash(), "") {
		t.Error("a state served to nobody in particular was recorded")
	}
	env.engine.NoteServed("game1", "peer-a", m("x"))
	if env.engine.servedUnderBase("game1", "peer-b", m("x").ManifestHash(), "") {
		t.Error("a state served to one peer counted for another")
	}
	for i := 0; i < servedKeep+5; i++ {
		env.engine.NoteServed("game1", "peer-a", m(string(rune('A'+i))))
	}
	if env.engine.servedUnderBase("game1", "peer-a", m("x").ManifestHash(), "") {
		t.Error("more states were kept than servedKeep")
	}
	if !env.engine.servedUnderBase("game1", "peer-a", m(string(rune('A'+servedKeep+4))).ManifestHash(), "") {
		t.Error("the latest state served was not kept")
	}
}
