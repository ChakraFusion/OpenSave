package e2e

import (
	"fmt"
	"net/http"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/opensave/opensave/internal/p2p/syncengine"
	"github.com/opensave/opensave/testutil"
)

// A device part-way through a sync holds a save nobody has: some files taken,
// some not. Asked for its files in that moment by the other device — whose own
// sync started a little later, from its watcher, its reconcile, a click — it
// used to describe exactly that, and the other device raised a conflict over a
// save neither had diverged on. See syncengine/settle.go.
//
// These start one device's sync, then the other's a moment later, at several
// offsets either way round, and require that no conflict appears and the
// files arrive.

// crossingSyncs starts a sync of the game on first, then on second after gap.
func crossingSyncs(first, second *testutil.TestDaemon, gameID string, gap time.Duration) {
	var wg sync.WaitGroup
	for i, d := range []*testutil.TestDaemon{first, second} {
		wg.Add(1)
		go func(i int, d *testutil.TestDaemon) {
			defer wg.Done()
			if i == 1 {
				time.Sleep(gap)
			}
			d.API(http.MethodPost, "/api/games/"+gameID+"/sync", nil, nil)
		}(i, d)
	}
	wg.Wait()
}

func manySaveFiles(n int, version string) map[string]string {
	files := map[string]string{}
	for i := 0; i < n; i++ {
		files[fmt.Sprintf("saves/slot%03d/data.sav", i)] = fmt.Sprintf("%s-payload-%d-%s", version, i, longFiller(i))
	}
	return files
}

// settledOnBoth waits for the game's syncs, follow-ups included, to finish on
// both devices.
func settledOnBoth(t *testing.T, gameID string, ds ...*testutil.TestDaemon) {
	t.Helper()
	if !testutil.WaitFor(60*time.Second, func() bool {
		for _, d := range ds {
			if d.Daemon.P2P.Sync.SyncBusy(gameID) {
				return false
			}
		}
		return true
	}) {
		t.Fatal("the syncs never finished")
	}
}

func requireSameFilesNoConflict(t *testing.T, a, b *testutil.TestDaemon, gameID string, files map[string]string) {
	t.Helper()
	if !testutil.WaitFor(45*time.Second, func() bool {
		for rel, c := range files {
			if b.ReadSave(rel) != c || a.ReadSave(rel) != c {
				return false
			}
		}
		return true
	}) {
		t.Fatalf("the two devices never ended with the same save (conflict: A=%v B=%v)%s", hasConflict(a, gameID), hasConflict(b, gameID), whereFilesAre(a, b, files))
	}
	settledOnBoth(t, gameID, a, b)
	if hasConflict(a, gameID) || hasConflict(b, gameID) {
		t.Fatalf("a conflict was raised though nothing diverged:%s%s", describeConflict(a, gameID), describeConflict(b, gameID))
	}
	// And the next sync from either side agrees there is nothing to do.
	for _, pair := range [][2]*testutil.TestDaemon{{a, b}, {b, a}} {
		if status, _ := syncTo(pair[0], gameID, pair[1].NodeID()); status == "conflict" {
			t.Fatalf("a sync after both settled reported a conflict")
		}
	}
}

// whereFilesAre says, for a failure message, how many of the wanted files
// each device holds, and what the first few it lacks hold instead.
func whereFilesAre(a, b *testutil.TestDaemon, want map[string]string) string {
	out := ""
	for _, d := range []*testutil.TestDaemon{a, b} {
		have, wrong := 0, []string{}
		for rel, c := range want {
			got := d.ReadSave(rel)
			if got == c {
				have++
				continue
			}
			if len(wrong) < 3 {
				if len(got) > 12 {
					got = got[:12]
				}
				wrong = append(wrong, fmt.Sprintf("%s=%q", rel, got))
			}
		}
		out += fmt.Sprintf("\n  %s holds %d of %d wanted; e.g. %v", d.Name(), have, len(want), wrong)
	}
	return out
}

// describeConflict says what a device's conflict on the game is about, for a
// failure message: which side holds what, and the first few paths that differ.
func describeConflict(d *testutil.TestDaemon, gameID string) string {
	c, ok := d.Daemon.P2P.Sync.ActiveConflicts()[gameID]
	if !ok {
		return fmt.Sprintf("\n  %s: none", d.Name())
	}
	diffs := c.DiffFiles
	if len(diffs) > 4 {
		diffs = diffs[:4]
	}
	return fmt.Sprintf("\n  %s: with %s; here %d files, there %d; %d differ, e.g. %+v",
		d.Name(), c.Peer.Name, c.LocalStats.Files, c.RemoteStats.Files, c.DiffTotal, diffs)
}

// Negative gaps start the device with the empty folder first — the one that
// pulls — which is the order that raised the conflict.
var crossingGaps = []time.Duration{
	-400 * time.Millisecond, -150 * time.Millisecond, -50 * time.Millisecond,
	50 * time.Millisecond, 150 * time.Millisecond, 400 * time.Millisecond,
}

func TestCrossingSyncs_AFirstPullIsNotTakenForADifferentSave(t *testing.T) {
	for _, gap := range crossingGaps {
		t.Run(fmt.Sprintf("gap=%v", gap), func(t *testing.T) {
			a := testutil.NewTestDaemon(t, "Crossing-A")
			b := testutil.NewTestDaemon(t, "Crossing-B")
			logOnFailure(t, a, b)
			a.PairWith(b)
			files := manySaveFiles(121, "v1")
			for rel, c := range files {
				a.WriteSave(rel, c)
			}
			// Hand-driven: the two syncs below are the only ones.
			for _, d := range []*testutil.TestDaemon{a, b} {
				d.API(http.MethodPost, "/api/settings", map[string]any{"autoSyncOnTrack": false}, nil)
			}
			gameID := a.TrackGame("Crossing")
			b.API(http.MethodPost, "/api/games", map[string]string{"name": "Crossing", "savePath": b.SaveDir}, nil)
			a.Daemon.WaitForTracking()
			b.Daemon.WaitForTracking()

			first, second, lag := a, b, gap
			if gap < 0 {
				first, second, lag = b, a, -gap
			}
			crossingSyncs(first, second, gameID, lag)
			requireSameFilesNoConflict(t, a, b, gameID, files)
		})
	}
}

// With history the rule that a device merely behind is not diverged does not
// help: an update changes files, so a device part-way through taking one holds
// some old and some new, which differs from the other side. Only not being
// read mid-write keeps this from being a conflict.
func TestCrossingSyncs_AnUpdateTakenPartWayIsNotAConflict(t *testing.T) {
	for _, gap := range crossingGaps {
		t.Run(fmt.Sprintf("gap=%v", gap), func(t *testing.T) {
			a := testutil.NewTestDaemon(t, "Update-A")
			b := testutil.NewTestDaemon(t, "Update-B")
			logOnFailure(t, a, b)
			a.PairWith(b)
			v1 := manySaveFiles(121, "v1")
			for rel, c := range v1 {
				a.WriteSave(rel, c)
			}
			for _, d := range []*testutil.TestDaemon{a, b} {
				d.API(http.MethodPost, "/api/settings", map[string]any{"autoSyncOnTrack": false}, nil)
			}
			gameID := a.TrackGame("Update")
			b.API(http.MethodPost, "/api/games", map[string]string{"name": "Update", "savePath": b.SaveDir}, nil)
			a.Daemon.WaitForTracking()
			b.Daemon.WaitForTracking()
			a.API(http.MethodPost, "/api/games/"+gameID+"/sync", nil, nil)
			requireSameFilesNoConflict(t, a, b, gameID, v1)

			// A plays: every file changes.
			time.Sleep(1100 * time.Millisecond) // a later mtime second
			v2 := manySaveFiles(121, "v2")
			for rel, c := range v2 {
				a.WriteSave(rel, c)
			}

			first, second, lag := a, b, gap
			if gap < 0 {
				first, second, lag = b, a, -gap
			}
			crossingSyncs(first, second, gameID, lag)
			requireSameFilesNoConflict(t, a, b, gameID, v2)
		})
	}
}

// A device still writing when the wait runs out answers "busy". That is not a
// failure and not a divergence: the asking device reports it, asks again by
// itself, and the change lands once the other has finished.
func TestCrossingSyncs_ADeviceStillWritingIsAskedAgain(t *testing.T) {
	orig := syncengine.ServeSettleWait
	syncengine.ServeSettleWait = 200 * time.Millisecond
	t.Cleanup(func() { syncengine.ServeSettleWait = orig })

	a := testutil.NewTestDaemon(t, "Busy-A")
	b := testutil.NewTestDaemon(t, "Busy-B")
	a.PairWith(b)
	a.WriteSave("slot1.sav", "from A")
	restore := suppressSyncOnTrack(a, b)
	gameID := a.TrackGame("Busy Game")
	b.API(http.MethodPost, "/api/games", map[string]string{"name": "Busy Game", "savePath": b.SaveDir}, nil)
	restore()

	// B is in the middle of writing this game, for as long as it takes to see
	// A's sync turned away more than once.
	release := b.Daemon.P2P.Sync.Writing(gameID)
	released := false
	defer func() {
		if !released {
			release()
		}
	}()

	if status, _ := syncTo(a, gameID, b.NodeID()); status != "peer_busy" {
		t.Fatalf("a sync with a device still writing reported %q, want peer_busy", status)
	}
	time.Sleep(800 * time.Millisecond) // several follow-ups turned away
	if got := b.ReadSave("slot1.sav"); got != "" {
		t.Fatalf("the save reached B while B was still writing: %q", got)
	}
	if hasConflict(a, gameID) || hasConflict(b, gameID) {
		t.Fatal("being turned away raised a conflict")
	}

	release()
	released = true
	if !testutil.WaitFor(30*time.Second, func() bool { return b.ReadSave("slot1.sav") == "from A" }) {
		t.Fatalf("once B finished, A's follow-up never delivered the save (B has %q)", b.ReadSave("slot1.sav"))
	}
}

// A game writes its save over a moment, and the other device can ask for it
// in the middle. It pulls the half-written state and holds it; the game
// finishes. That half state is one this device really had, so the other device
// holding it has not diverged — but nothing recorded that, this device judged
// it against their older agreement, and raised a conflict over a save only it
// had touched. The game's writes cannot be held back the way a sync's are, so
// this is answered by remembering what was served (syncengine/served.go).
func TestCrossingSyncs_APullOfAHalfWrittenSaveIsNotAConflict(t *testing.T) {
	a := testutil.NewTestDaemon(t, "Half-A")
	b := testutil.NewTestDaemon(t, "Half-B")
	logOnFailure(t, a, b)
	a.PairWith(b)
	v1 := manySaveFiles(40, "v1")
	for rel, c := range v1 {
		a.WriteSave(rel, c)
	}
	restore := suppressSyncOnTrack(a, b)
	gameID := a.TrackGame("Half")
	b.API(http.MethodPost, "/api/games", map[string]string{"name": "Half", "savePath": b.SaveDir}, nil)
	restore()
	a.API(http.MethodPost, "/api/games/"+gameID+"/sync", nil, nil)
	requireSameFilesNoConflict(t, a, b, gameID, v1)

	time.Sleep(1100 * time.Millisecond)
	v2 := manySaveFiles(40, "v2")
	names := make([]string, 0, len(v2))
	for rel := range v2 {
		names = append(names, rel)
	}
	sort.Strings(names)
	for _, rel := range names[:20] { // the game writes half its save...
		a.WriteSave(rel, v2[rel])
	}
	b.API(http.MethodPost, "/api/games/"+gameID+"/sync", nil, nil) // ...B pulls it
	settledOnBoth(t, gameID, a, b)
	for _, rel := range names[20:] { // ...and the game finishes
		a.WriteSave(rel, v2[rel])
	}
	status, _ := syncTo(a, gameID, b.NodeID())
	t.Logf("A's sync after the game finished: %s%s%s", status, describeConflict(a, gameID), describeConflict(b, gameID))
	requireSameFilesNoConflict(t, a, b, gameID, v2)
}
