package e2e

import (
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/opensave/opensave/testutil"
)

// A newly tracked game's save is recorded as its baseline, so a file event
// that changes nothing synced takes no second snapshot of it.
//
// It had none: the first event of any kind after tracking snapshotted the
// unchanged save again, and so did the first after the game's exclusions
// changed. A game writing a log into a folder excluded from syncing did that
// straight away. With cloud sync on, the copy was then the device's newest
// save, and a newer one another device had put in the cloud was no longer
// offered — TestIgnored_ASaveFromTheCloudLeavesAnIgnoredFileAlone failed that
// way whenever the extra snapshot came after the other device's.

func TestBaseline_AFileInAnExcludedFolderTakesNoSnapshot(t *testing.T) {
	d := testutil.NewTestDaemon(t, "BaselineExcluded")
	d.WriteSave("settings.ini", "graphics")
	d.WriteSave("slot1.sav", "act 1")
	game := d.TrackGame("Baseline Game")
	waitForWatch(t, d, game)
	if !testutil.WaitFor(30*time.Second, func() bool { return countSnapshots(d, game) == 1 }) {
		t.Fatalf("the initial snapshot was not taken: %d snapshots", countSnapshots(d, game))
	}
	d.API(http.MethodPatch, "/api/games/"+game, map[string]any{"syncIgnore": "settings.ini\nlogs/"}, nil)

	d.WriteSave("logs/session.log", "started")
	d.WriteSave("settings.ini", "other graphics")
	time.Sleep(8 * time.Second) // well past the watcher's debounce
	if n := countSnapshots(d, game); n != 1 {
		t.Errorf("%d snapshots after only excluded files changed, want the initial one alone", n)
	}

	// A real change is still taken.
	d.WriteSave("slot1.sav", "act 2")
	if !testutil.WaitFor(30*time.Second, func() bool { return countSnapshots(d, game) == 2 }) {
		t.Errorf("a change to the save was not snapshotted: %d snapshots", countSnapshots(d, game))
	}
}

// A snapshot taken by hand is the baseline from then on: the change it kept is
// not snapshotted a second time when the watcher gets to it.
func TestBaseline_ASnapshotByHandIsNotTakenAgain(t *testing.T) {
	d := testutil.NewTestDaemon(t, "BaselineManual")
	d.WriteSave("slot1.sav", "act 1")
	game := d.TrackGame("Baseline Manual Game")
	waitForWatch(t, d, game)
	if !testutil.WaitFor(30*time.Second, func() bool { return countSnapshots(d, game) == 1 }) {
		t.Fatalf("the initial snapshot was not taken: %d snapshots", countSnapshots(d, game))
	}
	d.WriteSave("slot1.sav", "act 2")
	d.API(http.MethodPost, "/api/games/"+game+"/snapshot", map[string]string{"comment": "before the boss"}, nil)
	time.Sleep(8 * time.Second) // well past the watcher's debounce
	if n := countSnapshots(d, game); n != 2 {
		t.Errorf("%d snapshots, want the initial one and the one taken by hand", n)
	}
}

// The first real change after tracking is snapshotted as before.
func TestBaseline_TheFirstChangeAfterTrackingIsSnapshotted(t *testing.T) {
	d := testutil.NewTestDaemon(t, "BaselineFirst")
	d.WriteSave("slot1.sav", "act 1")
	game := d.TrackGame("Baseline First Game")
	waitForWatch(t, d, game)
	if !testutil.WaitFor(30*time.Second, func() bool { return countSnapshots(d, game) == 1 }) {
		t.Fatalf("the initial snapshot was not taken: %d snapshots", countSnapshots(d, game))
	}
	d.WriteSave("slot1.sav", "act 2")
	if !testutil.WaitFor(30*time.Second, func() bool { return countSnapshots(d, game) == 2 }) {
		t.Errorf("the first change after tracking was not snapshotted: %d snapshots", countSnapshots(d, game))
	}
}

// Adding a save location changes no file, so it takes no snapshot; one that
// brings files with it is snapshotted, as the backup should hold them.
//
// The hash the watcher compares against covers every location, so adding one
// changed it, and the catch-up a watch runs when it starts snapshotted a save
// already kept.
func TestBaseline_AddingASaveLocationTakesNoSnapshotUnlessItBringsFiles(t *testing.T) {
	d := testutil.NewTestDaemon(t, "BaselineLocation")
	d.WriteSave("slot1.sav", "act 1")
	game := d.TrackGame("Baseline Location Game")
	waitForWatch(t, d, game)
	if !testutil.WaitFor(30*time.Second, func() bool { return countSnapshots(d, game) == 1 }) {
		t.Fatalf("the initial snapshot was not taken: %d snapshots", countSnapshots(d, game))
	}

	empty := filepath.Join(testutil.TempDir(t), "Config")
	if err := os.MkdirAll(empty, 0o777); err != nil {
		t.Fatal(err)
	}
	d.API(http.MethodPost, "/api/games/"+game+"/roots", map[string]string{"name": "config", "path": empty}, nil)
	time.Sleep(8 * time.Second) // past the debounce and the catch-up
	if n := countSnapshots(d, game); n != 1 {
		t.Errorf("%d snapshots after adding an empty location, want the initial one alone", n)
	}

	full := filepath.Join(testutil.TempDir(t), "Mods")
	if err := os.MkdirAll(full, 0o777); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(full, "mod.cfg"), []byte("on"), 0o666); err != nil {
		t.Fatal(err)
	}
	// A new snapshot, not a second one: here an older snapshot that a newer
	// one holds entirely is removed as the newer is taken.
	before := map[string]bool{}
	for _, s := range snapshotsOn(d, game, "main") {
		before[s.ID] = true
	}
	d.API(http.MethodPost, "/api/games/"+game+"/roots", map[string]string{"name": "mods", "path": full}, nil)
	if !testutil.WaitFor(30*time.Second, func() bool {
		for _, s := range snapshotsOn(d, game, "main") {
			if !before[s.ID] {
				return true
			}
		}
		return false
	}) {
		t.Errorf("a location that brought files was not snapshotted: %d snapshots", countSnapshots(d, game))
	}
}
