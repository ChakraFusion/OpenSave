package e2e

import (
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/opensave/opensave/testutil"
)

// A device joins holding an old copy of the save — from a backup, an earlier
// install, a folder copied across — stamped later than the other device's
// current save, with nothing agreed between them yet. The other device has the
// old copy in its history, so nobody needs to be asked: the new device takes
// the newer save. It syncs first, as tracking the game makes it do, so it is
// the one that has to ask whether its save was held (syncengine/heldstate.go).
func heldOldCopyJoins(t *testing.T, name string, pair func(a, b *testutil.TestDaemon)) {
	a := testutil.NewTestDaemon(t, name+"-A")
	b := testutil.NewTestDaemon(t, name+"-B")
	pair(a, b)
	restore := suppressSyncOnTrack(a, b)

	a.WriteSave("slot1.sav", "v1")
	gameID := a.TrackGame(name)
	waitTracked(t, a, gameID)
	time.Sleep(syncSettleWindow)
	a.WriteSave("slot1.sav", "v2")
	if !testutil.WaitFor(30*time.Second, func() bool { return len(snapshotsOn(a, gameID, "main")) >= 2 }) {
		t.Fatalf("setup: A never snapshotted v2 (%d snapshots)", len(snapshotsOn(a, gameID, "main")))
	}

	b.WriteSave("slot1.sav", "v1")
	later := time.Now().Add(time.Hour)
	if err := os.Chtimes(filepath.Join(b.SaveDir, "slot1.sav"), later, later); err != nil {
		t.Fatal(err)
	}
	b.API(http.MethodPost, "/api/games", map[string]string{"name": name, "savePath": b.SaveDir}, nil)
	restore()

	syncTo(b, gameID, a.NodeID())
	if !testutil.WaitFor(60*time.Second, func() bool {
		return b.ReadSave("slot1.sav") == "v2" || hasConflict(a, gameID) || hasConflict(b, gameID)
	}) {
		t.Fatalf("B never took A's save: B=%q", b.ReadSave("slot1.sav"))
	}
	time.Sleep(syncSettleWindow)
	settledOnBoth(t, gameID, a, b)
	if hasConflict(a, gameID) || hasConflict(b, gameID) {
		t.Fatalf("asked which save to keep, though A has held B's: A=%q B=%q", a.ReadSave("slot1.sav"), b.ReadSave("slot1.sav"))
	}
	if a.ReadSave("slot1.sav") != "v2" || b.ReadSave("slot1.sav") != "v2" {
		t.Errorf("want v2 on both: A=%q B=%q", a.ReadSave("slot1.sav"), b.ReadSave("slot1.sav"))
	}
}

func TestHeldState_AnOldCopyJoiningOverLANTakesTheNewerSave(t *testing.T) {
	heldOldCopyJoins(t, "HeldLAN", func(a, b *testutil.TestDaemon) { a.PairWith(b) })
}

func TestHeldState_AnOldCopyJoiningOverTheRelayTakesTheNewerSave(t *testing.T) {
	relayURL := startRelay(t)
	heldOldCopyJoins(t, "HeldWAN", func(a, b *testutil.TestDaemon) { pairOverRelay(t, a, b, relayURL, "held-room") })
}
