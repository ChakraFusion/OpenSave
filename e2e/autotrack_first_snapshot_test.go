package e2e

import (
	"net/http"
	"path/filepath"
	"testing"
	"time"

	"github.com/opensave/opensave/testutil"
)

// A game tracked here because another device asked about it gets what
// tracking it by hand gives: a first snapshot of what is already in its
// folder, and a watch.
//
// Reported in GitHub #16: auto-tracking created the game and nothing else.
// When the save was already the same on both devices — Steam Cloud keeps many
// of them so — every sync after that found nothing to do, so no snapshot was
// ever taken there, and the game had no history on that device at all. It
// was not watched either until the periodic check came round, so a change
// made there in the meantime was not sent on.
func TestAutoTrack_TakesAFirstSnapshotAndWatches(t *testing.T) {
	a := testutil.NewTestDaemon(t, "AutoSnap-A")
	b := testutil.NewTestDaemon(t, "AutoSnap-B")
	a.PairWith(b)
	rootA, rootB := t.TempDir(), t.TempDir()
	saveA, saveB := filepath.Join(rootA, "Detroit"), filepath.Join(rootB, "Detroit")
	writeAt(t, filepath.Join(saveA, "slot1.sav"), "chapter 12")
	writeAt(t, filepath.Join(saveB, "slot1.sav"), "chapter 12")
	b.API(http.MethodPost, "/api/settings", map[string]any{
		"pathTranslations": []map[string]string{{"fromPattern": rootA, "toPattern": rootB}},
	}, nil)

	var tracked gameRow
	a.API(http.MethodPost, "/api/games", map[string]string{"name": "Detroit", "savePath": saveA}, &tracked)
	a.API(http.MethodPost, "/api/games/"+tracked.ID+"/sync", nil, nil)

	if !testutil.WaitFor(30*time.Second, func() bool { return gamesOf(b)[tracked.ID].SavePath == saveB }) {
		t.Fatalf("B never auto-tracked the game at its own folder; B has %+v", gamesOf(b))
	}
	if !testutil.WaitFor(30*time.Second, func() bool {
		snaps, _ := b.Daemon.Store.ListSnapshots(tracked.ID, "main")
		return len(snaps) > 0
	}) {
		t.Fatal("the auto-tracked game has no snapshot on B, though its folder held the save")
	}
	if !testutil.WaitFor(30*time.Second, func() bool {
		_, watching := b.Daemon.Watcher.Watching(tracked.ID)
		return watching
	}) {
		t.Error("the auto-tracked game is not watched on B")
	}
	if got := readAt(filepath.Join(saveB, "slot1.sav")); got != "chapter 12" {
		t.Errorf("B's save changed to %q", got)
	}
}
