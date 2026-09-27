package e2e

import (
	"net/http"

	"github.com/opensave/opensave/testutil"
)

// suppressSyncOnTrack turns off the sync each of these devices fires when it
// tracks a game, and returns the call that puts the setting back.
//
// For a test that has two paired devices track the same game and then syncs it
// by hand. Left on, either device's sync can cut into that setup:
//
//   - The first device's can reach the second before the second's own track
//     does. The second adopts the game itself, at the first device's path —
//     which, on the one disk the test daemons share, is the first device's own
//     folder — and its own track then fails with 409 "already exists", or
//     quietly becomes a second game, "<id>-2", that nothing syncs into. See
//     testutil.NewTestDaemon.
//   - The second device's, from a folder still empty, pulls the game while the
//     test's own sync pushes it. Asked for its files part way through taking
//     them in, the second device looks as if it holds a different save, and the
//     first raises a conflict where nothing diverged. That is the app's to fix,
//     and not what these tests are about.
//
// Only while both track. restore waits for each device to finish taking the
// game on, then puts the setting back, so the rest of a test runs as before —
// including anything it says about tracking itself. The wait matters: the
// setting is not read when a game is tracked but afterwards, in the
// background, once the first snapshot is written. Put back straight away, it
// was read as on whenever that snapshot was slow — under the race detector, on
// a loaded machine — and the sync went out after all.
func suppressSyncOnTrack(devices ...*testutil.TestDaemon) (restore func()) {
	was := make([]bool, len(devices))
	for i, d := range devices {
		d.T.Helper()
		var settings struct {
			AutoSyncOnTrack bool `json:"autoSyncOnTrack"`
		}
		d.API(http.MethodGet, "/api/settings", nil, &settings)
		was[i] = settings.AutoSyncOnTrack
		d.API(http.MethodPost, "/api/settings", map[string]any{"autoSyncOnTrack": false}, nil)
	}
	return func() {
		for i, d := range devices {
			d.T.Helper()
			d.Daemon.WaitForTracking()
			d.API(http.MethodPost, "/api/settings", map[string]any{"autoSyncOnTrack": was[i]}, nil)
		}
	}
}
