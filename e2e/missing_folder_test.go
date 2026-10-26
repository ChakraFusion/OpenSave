package e2e

import (
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/opensave/opensave/testutil"
)

// A save folder that has gone — deleted, on a drive that is not plugged in,
// renamed by a reinstall — is not a save that was emptied. Every file in it
// looks deleted, and passing that on to a paired device would delete that
// device's save too: the one copy left.
func TestMissingFolder_DoesNotWipeThePeer(t *testing.T) {
	a, b, gameID := pairAndTrack(t, "Missing Folder Game", map[string]string{
		"slot1.sav":        "hours of play",
		"slot2.sav":        "more hours",
		"profile/user.cfg": "settings",
	})
	testutil.SettleSync(t, gameID, a, b)

	// B's folder goes, whole.
	testutil.RemoveTree(t, b.SaveDir)

	// Both sides reach for each other, as they would on their own. B may
	// refuse: removing a folder deletes its files first, and if B's watcher
	// sees them go before the folder does, B holds the game back as emptied
	// ("not synced until you say whether that was meant") instead of calling
	// it missing. Either keeps the loss from A, and that is what this checks —
	// so a refusal is an answer here, not a failure.
	for i := 0; i < 3; i++ {
		for _, d := range []*testutil.TestDaemon{b, a} {
			if code := d.APIStatus(http.MethodPost, "/api/games/"+gameID+"/sync", nil, nil); code >= 400 && code != http.StatusConflict {
				t.Fatalf("%s: sync answered %d", d.Name(), code)
			}
		}
		time.Sleep(time.Second)
	}
	testutil.SettleSync(t, gameID, a, b)

	for rel, want := range map[string]string{"slot1.sav": "hours of play", "slot2.sav": "more hours", "profile/user.cfg": "settings"} {
		if got := a.ReadSave(rel); got != want {
			t.Errorf("after B's folder went missing, A's %s is %q — the loss was passed on", rel, got)
		}
	}
}

// The same, across what a restart does: the watch is set up again. That used
// to create the folder afresh, empty — and the next sync read every file in
// it as deleted, and deleted them on the other device too. Then, when the
// folder comes back, it is watched and synced again.
func TestMissingFolder_ARestartDoesNotRecreateItEmpty(t *testing.T) {
	a, b, gameID := pairAndTrack(t, "Missing Restart Game", map[string]string{
		"slot1.sav": "hours of play",
		"slot2.sav": "more hours",
	})
	testutil.SettleSync(t, gameID, a, b)

	testutil.RemoveTree(t, b.SaveDir)
	// What starting up does, for this game.
	b.API(http.MethodPatch, "/api/games/"+gameID, map[string]any{"autoSync": false}, nil)
	b.API(http.MethodPatch, "/api/games/"+gameID, map[string]any{"autoSync": true}, nil)
	b.API(http.MethodPost, "/api/watch/reload", nil, nil)

	if _, err := os.Stat(b.SaveDir); err == nil {
		t.Errorf("the missing save folder was created again")
	}
	var games map[string]struct {
		SavePathMissing bool `json:"savePathMissing"`
	}
	b.API(http.MethodGet, "/api/games", nil, &games)
	if !games[gameID].SavePathMissing {
		t.Errorf("the game does not say its save folder is missing")
	}

	for i := 0; i < 3; i++ {
		b.API(http.MethodPost, "/api/games/"+gameID+"/sync", nil, nil)
		a.API(http.MethodPost, "/api/games/"+gameID+"/sync", nil, nil)
		time.Sleep(time.Second)
	}
	testutil.SettleSync(t, gameID, a, b)
	for rel, want := range map[string]string{"slot1.sav": "hours of play", "slot2.sav": "more hours"} {
		if got := a.ReadSave(rel); got != want {
			t.Errorf("after B's folder went missing and B restarted, A's %s is %q — the loss was passed on", rel, got)
		}
	}

	// It comes back — a drive plugged in again — and is watched again.
	if err := os.MkdirAll(b.SaveDir, 0o777); err != nil {
		t.Fatal(err)
	}
	b.WriteSave("slot1.sav", "hours of play")
	b.WriteSave("slot2.sav", "more hours")
	b.API(http.MethodPost, "/api/watch/reload", nil, nil)
	b.API(http.MethodGet, "/api/games", nil, &games)
	if games[gameID].SavePathMissing {
		t.Errorf("the save folder is back and the game still says it is missing")
	}
	if _, watching := b.Daemon.Watcher.Watching(gameID); !watching {
		t.Errorf("the save folder is back and it is not watched")
	}
}

// A save folder that went missing and comes back empty — recreated by the
// game or an uninstaller — is held and asked about, as an emptied folder is,
// never synced as deletions (#66).
func TestMissingFolder_ComingBackEmptyIsHeld(t *testing.T) {
	a, b, gameID := missingThenBack(t, "Missing Back Empty Game", nil)
	if e := emptiedState(t, b, gameID); e == nil || e.Files != 2 {
		t.Errorf("the folder came back empty and B says %+v; want it held back over 2 files", e)
	}
	syncBothWays(t, gameID, a, b)
	for rel, want := range map[string]string{"slot1.sav": "hours of play", "slot2.sav": "more hours"} {
		if got := a.ReadSave(rel); got != want {
			t.Errorf("A's %s is %q after B's folder came back empty — the loss was passed on", rel, got)
		}
	}
}

// The same with some files back: fewer than both devices last shared is held
// too, over the files that are not there.
func TestMissingFolder_ComingBackWithFewerFilesIsHeld(t *testing.T) {
	a, b, gameID := missingThenBack(t, "Missing Back Fewer Game", map[string]string{"slot1.sav": "hours of play"})
	if e := emptiedState(t, b, gameID); e == nil || e.Files != 1 {
		t.Errorf("the folder came back without slot2.sav and B says %+v; want it held back over 1 file", e)
	}
	syncBothWays(t, gameID, a, b)
	if got := a.ReadSave("slot2.sav"); got != "more hours" {
		t.Errorf("A's slot2.sav is %q after B's folder came back without it — the loss was passed on", got)
	}
}

// Deleting a whole folder removes its files first. A watcher that saw them go
// held the game as emptied; once the folder itself is gone, "missing" wins:
// nothing is asked, the game waits quietly, and a folder that comes back
// whole simply syncs again (#66).
func TestMissingFolder_AnEmptiedHoldGivesWayToMissing(t *testing.T) {
	a, b, gameID := pairAndTrack(t, "Missing Wins Game", map[string]string{
		"slot1.sav": "hours of play",
		"slot2.sav": "more hours",
	})
	testutil.SettleSync(t, gameID, a, b)
	emptyFolder(t, b.SaveDir)
	syncBothWays(t, gameID, a, b)
	if e := emptiedState(t, b, gameID); e == nil {
		t.Fatal("setup: emptying B's folder did not hold the game back")
	}

	testutil.RemoveTree(t, b.SaveDir)
	b.API(http.MethodPost, "/api/watch/reload", nil, nil)
	if e := emptiedState(t, b, gameID); e != nil {
		t.Errorf("the whole folder is gone and B still asks about it: %+v", e)
	}
	syncBothWays(t, gameID, a, b)
	if got := a.ReadSave("slot1.sav"); got != "hours of play" {
		t.Errorf("A's slot1.sav is %q while B's folder is missing", got)
	}

	if err := os.MkdirAll(b.SaveDir, 0o777); err != nil {
		t.Fatal(err)
	}
	b.WriteSave("slot1.sav", "hours of play")
	b.WriteSave("slot2.sav", "more hours")
	b.API(http.MethodPost, "/api/watch/reload", nil, nil)
	if e := emptiedState(t, b, gameID); e != nil {
		t.Errorf("the folder came back whole and B holds it: %+v", e)
	}
}

// missingThenBack pairs two devices over a two-file save, takes B's folder
// away whole, lets both try to sync while it is missing, then brings it back
// holding back (nil: empty) and has B look again.
func missingThenBack(t *testing.T, name string, back map[string]string) (a, b *testutil.TestDaemon, gameID string) {
	t.Helper()
	a, b, gameID = pairAndTrack(t, name, map[string]string{
		"slot1.sav": "hours of play",
		"slot2.sav": "more hours",
	})
	testutil.SettleSync(t, gameID, a, b)
	testutil.RemoveTree(t, b.SaveDir)
	b.API(http.MethodPost, "/api/watch/reload", nil, nil)
	syncBothWays(t, gameID, a, b)

	if err := os.MkdirAll(b.SaveDir, 0o777); err != nil {
		t.Fatal(err)
	}
	for rel, body := range back {
		b.WriteSave(rel, body)
	}
	b.API(http.MethodPost, "/api/watch/reload", nil, nil)
	return a, b, gameID
}
