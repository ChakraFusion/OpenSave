package e2e

import (
	"net/http"
	"testing"
	"time"

	"github.com/opensave/opensave/testutil"
)

// Steam rewrites remotecache.vdf in a game's userdata folder on every start.
// That is the launcher's file, not the save: it is not snapshotted as a
// change, and the other device's copy is neither replaced nor deleted.
func TestLauncherFile_SteamRewritingRemotecacheIsNoSaveChange(t *testing.T) {
	a, b, gameID := pairAndTrack(t, "Steam Cloud Game", map[string]string{"save.dat": "progress"})
	// Each device's Steam keeps its own bookkeeping beside the save.
	a.WriteSave("remotecache.vdf", "steam bookkeeping on A")
	b.WriteSave("remotecache.vdf", "steam bookkeeping on B")
	testutil.SettleSync(t, gameID, a, b)
	time.Sleep(syncSettleWindow)
	before := len(snapshotsOn(a, gameID, "main"))

	// Steam starts on A and rewrites its file.
	a.WriteSave("remotecache.vdf", "steam bookkeeping on A, rewritten")
	time.Sleep(3 * syncSettleWindow)
	a.API(http.MethodPost, "/api/games/"+gameID+"/sync", nil, nil)
	testutil.SettleSync(t, gameID, a, b)

	if after := len(snapshotsOn(a, gameID, "main")); after != before {
		t.Errorf("Steam rewriting remotecache.vdf was snapshotted as a change: %d -> %d snapshots", before, after)
	}
	if got := b.ReadSave("remotecache.vdf"); got != "steam bookkeeping on B" {
		t.Errorf("B's own remotecache.vdf was replaced: %q", got)
	}
	if got := b.ReadSave("save.dat"); got != "progress" {
		t.Errorf("the save on B changed: %q", got)
	}

	// A real save change still syncs.
	a.WriteSave("save.dat", "more progress")
	time.Sleep(syncSettleWindow)
	a.API(http.MethodPost, "/api/games/"+gameID+"/sync", nil, nil)
	if !testutil.WaitFor(45*time.Second, func() bool { return b.ReadSave("save.dat") == "more progress" }) {
		t.Errorf("a real save change did not reach B: %q", b.ReadSave("save.dat"))
	}
	if got := b.ReadSave("remotecache.vdf"); got != "steam bookkeeping on B" {
		t.Errorf("B's remotecache.vdf was replaced along with the save: %q", got)
	}
}
