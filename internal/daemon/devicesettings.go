package daemon

import (
	"strings"

	"github.com/opensave/opensave/internal/delta"
	"github.com/opensave/opensave/internal/presets"
	"github.com/opensave/opensave/internal/store"
	"github.com/opensave/opensave/internal/watcher"
)

// Device settings: the files the game database names as a game's settings
// rather than its save are left out of syncing (presets/devicesettings.go),
// through the game's exclusion list (syncengine.IgnoreText).
//
// Worked out once per run and kept: the database is read when first needed,
// and a game's answer only changes when its name, App ID or folder does —
// which makes it a different key here. A database update takes effect at the
// next start, where adoptExclusionView carries every recorded hash over.

type devSettingsCache struct {
	loaded   bool
	db       *presets.DeviceSettings
	patterns map[string][]string
}

func (d *Daemon) deviceSettingsFor(game store.Game) []string {
	if d.Scanner == nil {
		return nil
	}
	key := game.Name + "\x00" + game.AppID + "\x00" + game.SavePath
	d.devMu.Lock()
	defer d.devMu.Unlock()
	if !d.dev.loaded {
		d.dev.db = d.Scanner.DeviceSettings()
		d.dev.patterns = map[string][]string{}
		d.dev.loaded = true
	}
	if p, ok := d.dev.patterns[key]; ok {
		return p
	}
	p := d.dev.db.Patterns(game.Name, game.AppID, []string{game.SavePath})
	d.dev.patterns[key] = p
	return p
}

// IgnoreText is the exclusion list a game is synced under (see
// syncengine.Engine.IgnoreText).
func (d *Daemon) IgnoreText(game store.Game) string {
	if d.P2P == nil || d.P2P.Sync == nil {
		return game.SyncIgnore
	}
	return d.P2P.Sync.IgnoreText(game)
}

// DeviceSettings returns the patterns for a game's device settings, whether
// or not it is set to sync them — what the window lists.
func (d *Daemon) DeviceSettings(game store.Game) []string {
	return d.deviceSettingsFor(game)
}

// exclusionsMark records, per game, the exclusion list its recorded hashes
// were last taken under, and the never-synced list of the build that took
// them.
func exclusionsMark(gameID string) string { return "exclusions:" + gameID }

func exclusionsValue(text string) string {
	return delta.NeverSyncedList + "\n--\n" + text
}

// NoteExclusions records that a game's hashes are now taken under its
// current exclusion list — after the rules were changed and the hashes
// rebased by whoever changed them.
func (d *Daemon) NoteExclusions(game store.Game) {
	_ = d.Store.SetMark(exclusionsMark(game.ID), exclusionsValue(d.IgnoreText(game)))
}

// adoptExclusionView carries every game's recorded hashes over to what the
// game is excluded by now, when that changed without anyone writing a rule:
// a build with a new delta.NeverSyncedList, or the game database naming
// settings files. Each is re-taken only when the save is the one it was
// recorded for, so a real change still counts.
//
// Without it, a build that starts excluding a file sees every save holding
// one as changed: an auto-snapshot of a game nobody played, and a new version
// of its own on every device at once, which the devices then disagree over.
func (d *Daemon) adoptExclusionView(games []store.Game) {
	neverSyncedDone := d.Store.Mark("never_synced") == delta.NeverSyncedList
	for _, game := range games {
		now := d.IgnoreText(game)
		oldText, beforeNeverSynced := game.SyncIgnore, !neverSyncedDone
		if prev := d.Store.Mark(exclusionsMark(game.ID)); prev != "" {
			list, text, _ := strings.Cut(prev, "\n--\n")
			if list == delta.NeverSyncedList && text == now {
				continue
			}
			oldText, beforeNeverSynced = text, list != delta.NeverSyncedList
		} else if !beforeNeverSynced && oldText == now {
			_ = d.Store.SetMark(exclusionsMark(game.ID), exclusionsValue(now))
			continue
		}

		extra, err := d.Store.GameRootPaths(game.ID)
		if err != nil {
			extra = nil
		}
		m, failures, err := delta.BuildMultiManifest(game.SavePath, extra)
		if err != nil || len(failures) > 0 {
			continue // unreadable now: tried again at the next start
		}
		was := watcher.ContentHash(m, oldText)
		if beforeNeverSynced {
			was = watcher.ContentHashBeforeNeverSynced(m, oldText)
		}
		if game.LastManifestHash != "" && game.LastManifestHash == was {
			_ = d.Store.SetLastManifestHash(game.ID, watcher.ContentHash(m, now))
		}
		if d.P2P != nil && d.P2P.Sync != nil {
			primary := m
			primary.Extra = nil
			d.P2P.Sync.AdoptExclusionView(game.ID, primary, oldText, beforeNeverSynced)
		}
		_ = d.Store.SetMark(exclusionsMark(game.ID), exclusionsValue(now))
	}
	_ = d.Store.SetMark("never_synced", delta.NeverSyncedList)
}
