package daemon

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/opensave/opensave/internal/store"
	"github.com/opensave/opensave/internal/watcher"
)

// adoptAutoTracked gives a game tracked because a paired device asked for it
// what TrackGame gives one tracked by hand: a first snapshot of what its
// folder already holds, and a watch.
//
// Auto-tracking used to create the game and nothing else (GitHub #16). Where
// the save was already the same on both devices — Steam Cloud keeps many of
// them so — no sync ever pulled anything, so no snapshot was ever taken, and
// the game had no history on this device at all. Nor was it watched until the
// periodic check came round, so a change made here meanwhile went nowhere.
//
// In the background: this is called inside the other device's manifest
// request, which must not wait on a snapshot of a large save. Counted with
// TrackGame's own, so Stop waits for it.
//
// Only a folder that already has a file is snapshotted. An empty one has
// nothing to keep, and what a sync brings into it is snapshotted as it
// arrives. The snapshot is taken while no sync is writing the folder: this
// runs just as the other device starts syncing it, and a snapshot of a pull
// half done would be a copy of a save that never existed.
func (d *Daemon) adoptAutoTracked(game store.Game) {
	d.initialSnapshots.Add()
	go func() {
		defer d.initialSnapshots.Done()
		if saveHasAFile(game.SavePath) {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
			readDone, err := d.P2P.Sync.Reading(ctx, game.ID)
			cancel()
			if err == nil {
				_, err = d.Snapshots.Create(game.ID, "Initial snapshot", true)
				readDone()
			}
			if err != nil {
				d.Log.Log("warn", fmt.Sprintf("initial snapshot for %q failed: %v", game.Name, err))
			}
		}
		if _, err := d.Store.GetGame(game.ID); err != nil {
			return // untracked meanwhile: no watch for a game that is gone
		}
		if err := d.watchGame(game.ID, game.SavePath); err != nil && !errors.Is(err, watcher.ErrStopped) {
			d.Log.Log("warn", fmt.Sprintf("could not watch %q: %v", game.Name, err))
		}
	}()
}

// errFoundAFile ends saveHasAFile's walk at the first file.
var errFoundAFile = errors.New("found a file")

// saveHasAFile says whether a save — a folder, or a single file — holds any
// file at all. A folder of empty folders does not: a snapshot of it would be
// empty. Stops at the first file, so a save of a quarter of a million files
// costs no more than one of ten.
func saveHasAFile(savePath string) bool {
	info, err := os.Stat(savePath)
	if err != nil {
		return false
	}
	if !info.IsDir() {
		return true
	}
	err = filepath.WalkDir(savePath, func(_ string, entry fs.DirEntry, err error) error {
		if err != nil {
			return nil // an unreadable corner is not a file
		}
		if !entry.IsDir() {
			return errFoundAFile
		}
		return nil
	})
	return errors.Is(err, errFoundAFile)
}
