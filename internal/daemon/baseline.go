package daemon

import (
	"github.com/opensave/opensave/internal/delta"
	"github.com/opensave/opensave/internal/ignore"
	"github.com/opensave/opensave/internal/store"
	"github.com/opensave/opensave/internal/watcher"
)

// SnapshotWithBaseline takes a snapshot of a game's save and records, as the
// hash the watcher compares against, the save as it was just before it — for
// the snapshots taken outside the watcher: a newly tracked game's first one,
// and those taken by hand.
//
// Without it, a newly tracked game had no baseline, and one snapshotted by
// hand kept the one from before. Either way the next file event of any kind —
// a log written into an excluded folder — or the catch-up a watch runs when it
// starts read as the save changing, and an automatic snapshot was taken of a
// save already kept. With cloud sync on, that copy was then this device's
// newest save, and a newer one another device had put in the cloud meanwhile
// was no longer offered.
//
// Taken before the snapshot, not after: a change made while it is being
// written then differs from the baseline, and the watcher snapshots it. The
// worst case is one snapshot too many, as before; never a change taken for the
// baseline. (The watcher itself never writes a baseline for that reason — see
// catchUpOne.)
//
// Recorded only if no other snapshot recorded one in the meantime. A newly
// tracked game's first snapshot runs in the background; a snapshot taken by
// hand meanwhile finished first, and the first one's older value then replaced
// its newer one, and the watcher snapshotted the save again.
func (d *Daemon) SnapshotWithBaseline(gameID, comment string, isSystemAuto bool) (store.Snapshot, error) {
	before, was, hashErr := "", "", error(nil)
	if game, err := d.Store.GetGame(gameID); err == nil {
		was = game.LastManifestHash
		before, hashErr = d.currentContentHash(game)
	} else {
		hashErr = err
	}
	snap, err := d.Snapshots.Create(gameID, comment, isSystemAuto)
	if err != nil {
		return snap, err
	}
	if hashErr == nil {
		_, _ = d.Store.SetLastManifestHashIf(snap.GameID, was, before)
	}
	return snap, nil
}

// LastSnapshotHashUnder restates a game's last-snapshot hash under the
// exclusion rules it has just been given (game.SyncIgnore), or returns "" when
// that cannot be done safely. oldIgnore is the rules the hash was recorded
// under.
//
// The recorded value was taken over the old rules, so it can never match
// again. Cleared, the next file event of any kind — a log written into a
// folder that was just excluded — read as the save changing, and an automatic
// snapshot was taken of a save nobody had changed. With cloud sync on, that
// copy was then this device's newest save, and a newer one from another
// device was no longer offered.
//
// Restated only when that claims nothing new: the save is exactly as it was at
// the last snapshot under the old rules, and the new rules bring no file back
// into view. Otherwise "", as before: the safe direction, which can cost one
// unnecessary snapshot but never leaves a change unsnapshotted.
func (d *Daemon) LastSnapshotHashUnder(game store.Game, oldIgnore string) string {
	if game.LastManifestHash == "" {
		return ""
	}
	extra, err := d.Store.GameRootPaths(game.ID)
	if err != nil {
		return ""
	}
	// As the save is now, not as a cache last saw it: a change it has not
	// noticed yet must count as one.
	delta.InvalidateRoot(game.SavePath)
	for _, p := range extra {
		delta.InvalidateRoot(p)
	}
	m, failures, err := delta.BuildMultiManifest(game.SavePath, extra)
	if err != nil || len(failures) > 0 {
		return ""
	}
	// In the terms the watcher hashes in: the game's rules together with the
	// device settings kept out of sync (IgnoreText).
	old := game
	old.SyncIgnore = oldIgnore
	oldText, newText := d.IgnoreText(old), d.IgnoreText(game)
	if watcher.ContentHash(m, oldText) != game.LastManifestHash {
		return "" // changed since the last snapshot: the next one takes it
	}
	if bringsBack(m, ignore.Parse(oldText), ignore.Parse(newText)) {
		return ""
	}
	return watcher.ContentHash(m, newText)
}

// LocationsChanged restates a game's last-snapshot hash after one of its save
// locations was added or removed (oldRoots is GameRootPaths from before), when
// that claims nothing new. Called before the game is watched again.
//
// The hash covers every location, so adding or removing one changes it while
// no file changed: the catch-up a watch runs when it starts then took a
// snapshot of a save already kept. The test that adds a location ended while
// that snapshot was still being written, which failed its cleanup on Linux CI.
//
// Restated only when the save is as it was at the last snapshot over the old
// locations, and a location added holds no file yet. A location that brings
// files with it is left to be snapshotted: the backup should hold it.
func (d *Daemon) LocationsChanged(gameID string, oldRoots map[string]string) {
	game, err := d.Store.GetGame(gameID)
	if err != nil || game.LastManifestHash == "" {
		return
	}
	newRoots, err := d.Store.GameRootPaths(gameID)
	if err != nil {
		return
	}
	build := func(roots map[string]string) (delta.Manifest, bool) {
		delta.InvalidateRoot(game.SavePath)
		for _, p := range roots {
			delta.InvalidateRoot(p)
		}
		m, failures, err := delta.BuildMultiManifest(game.SavePath, roots)
		return m, err == nil && len(failures) == 0
	}
	before, ok := build(oldRoots)
	if !ok || watcher.ContentHash(before, d.IgnoreText(game)) != game.LastManifestHash {
		return // changed since the last snapshot: the next one takes it
	}
	after, ok := build(newRoots)
	if !ok {
		return
	}
	for name := range newRoots {
		if _, had := oldRoots[name]; !had && len(after.Extra[name].Files) > 0 {
			return // a location that brings files: snapshotted, as it should be
		}
	}
	_ = d.Store.SetLastManifestHash(gameID, watcher.ContentHash(after, d.IgnoreText(game)))
}

// bringsBack reports whether any path in m was excluded by the old rules and
// is not by the new ones. Such a file was never part of the hash recorded, so
// nothing says it is unchanged since the last snapshot.
func bringsBack(m delta.Manifest, oldRules, newRules ignore.Rules) bool {
	if oldRules.Empty() {
		return false
	}
	back := func(p string) bool {
		return oldRules.Match(p) && (newRules.Empty() || !newRules.Match(p))
	}
	for p := range m.Files {
		if back(p) {
			return true
		}
	}
	for _, dir := range m.Dirs {
		if back(dir) {
			return true
		}
	}
	for _, root := range m.Extra {
		for p := range root.Files {
			if back(p) {
				return true
			}
		}
		for _, dir := range root.Dirs {
			if back(dir) {
				return true
			}
		}
	}
	return false
}
