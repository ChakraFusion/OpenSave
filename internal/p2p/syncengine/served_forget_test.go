package syncengine

import (
	"testing"

	"github.com/opensave/opensave/internal/delta"
)

// Untracking a game forgets what this device served for it, so a re-track
// cannot bank a state served before the untrack; other games keep theirs.
func TestForgetServed_ForgetsOnlyThatGame(t *testing.T) {
	env := setupEngine(t)
	write(t, env.localDir, "save.dat", "v1")
	m, err := delta.BuildManifest(env.localDir)
	if err != nil {
		t.Fatal(err)
	}
	e := env.engine
	e.NoteServed("game1", env.peer.ID, m)
	e.NoteServed("game10", env.peer.ID, m)
	base := e.Store.GetAgreedHash("game1", env.peer.ID)
	if !e.servedUnderBase("game1", env.peer.ID, m.ManifestHash(), base) {
		t.Fatal("setup: the served state was not recorded")
	}

	e.ForgetServed("game1")

	if e.servedUnderBase("game1", env.peer.ID, m.ManifestHash(), base) {
		t.Error("a state served before the untrack is still remembered")
	}
	if !e.servedUnderBase("game10", env.peer.ID, m.ManifestHash(), e.Store.GetAgreedHash("game10", env.peer.ID)) {
		t.Error("forgetting game1 also forgot game10")
	}
}
