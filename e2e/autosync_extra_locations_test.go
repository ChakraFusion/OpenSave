package e2e

import (
	"net/http"
	"testing"
	"time"

	"github.com/opensave/opensave/testutil"
)

// Enabling AutoSync through PATCH /api/games/{id} must watch named additional
// save locations, not only the primary path. A change confined to an extra
// location has to reach the peer with no further sync call.
//
// Regression for https://github.com/Liquid-co/OpenSave/issues/88.
func TestEnablingAutoSyncWatchesAdditionalLocations(t *testing.T) {
	a, b, gameID := pairAndTrack(t, "AutoSyncExtra", map[string]string{
		"save.sav": "primary v1",
	})
	pauseAutoSync(t, a, b, gameID)

	aConfig := extraDir(t, a, "config")
	bConfig := extraDir(t, b, "config")
	writeIn(t, aConfig, "graphics.ini", "res=1080p")
	addRoot(t, a, gameID, "config", aConfig)
	addRoot(t, b, gameID, "config", bConfig)

	time.Sleep(syncSettleWindow)
	a.API(http.MethodPost, "/api/games/"+gameID+"/sync", nil, nil)
	if !testutil.WaitFor(45*time.Second, func() bool {
		return readIn(bConfig, "graphics.ini") == "res=1080p" && b.ReadSave("save.sav") == "primary v1"
	}) {
		t.Fatal("explicit sync did not establish the extra location before AutoSync was enabled")
	}

	a.API(http.MethodPatch, "/api/games/"+gameID, map[string]any{"autoSync": true}, nil)
	b.API(http.MethodPatch, "/api/games/"+gameID, map[string]any{"autoSync": true}, nil)
	time.Sleep(syncSettleWindow)

	writeIn(t, aConfig, "graphics.ini", "res=1440p")
	if !testutil.WaitFor(45*time.Second, func() bool {
		return readIn(bConfig, "graphics.ini") == "res=1440p"
	}) {
		t.Fatalf("a change only in the additional location did not auto-sync: peer=%q", readIn(bConfig, "graphics.ini"))
	}
	if got := b.ReadSave("save.sav"); got != "primary v1" {
		t.Errorf("the extra-location sync disturbed the primary save: %q", got)
	}

	time.Sleep(1100 * time.Millisecond)
	a.WriteSave("save.sav", "primary v2")
	if !testutil.WaitFor(45*time.Second, func() bool {
		return b.ReadSave("save.sav") == "primary v2"
	}) {
		t.Fatalf("primary-path AutoSync stopped working: peer=%q", b.ReadSave("save.sav"))
	}

	pauseAutoSync(t, a, b, gameID)
	writeIn(t, aConfig, "graphics.ini", "res=2160p")
	time.Sleep(12 * time.Second)
	if got := readIn(bConfig, "graphics.ini"); got != "res=1440p" {
		t.Fatalf("disabling AutoSync did not stop watcher-driven sync: peer=%q", got)
	}
}
