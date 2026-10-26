package daemon

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/opensave/opensave/internal/delta"
	"github.com/opensave/opensave/internal/store"
	"github.com/opensave/opensave/internal/watcher"
)

// A game's last-snapshot hash restated under new exclusion rules: only when
// the save is provably as it was at that snapshot, and the new rules bring no
// file back; "" otherwise, which takes the next snapshot as before.
func TestLastSnapshotHashUnder(t *testing.T) {
	d := newTestDaemon(t)
	write := func(dir, rel, content string) {
		t.Helper()
		p := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		delta.InvalidateRoot(dir)
	}
	hashOf := func(dir, rules string) string {
		t.Helper()
		m, _, err := delta.BuildMultiManifest(dir, nil)
		if err != nil {
			t.Fatal(err)
		}
		return watcher.ContentHash(m, rules)
	}
	setUp := func(oldRules string) (store.Game, string) {
		dir := t.TempDir()
		write(dir, "slot1.sav", "act 1")
		write(dir, "settings.ini", "graphics")
		write(dir, "logs/a.log", "log")
		return store.Game{ID: "g-" + filepath.Base(dir), SavePath: dir, SyncIgnore: oldRules,
			LastManifestHash: hashOf(dir, oldRules)}, dir
	}

	t.Run("unchanged save, narrower view: restated", func(t *testing.T) {
		game, dir := setUp("")
		game.SyncIgnore = "logs/\nsettings.ini"
		if got, want := d.LastSnapshotHashUnder(game, ""), hashOf(dir, game.SyncIgnore); got != want {
			t.Errorf("got %q, want the save's hash under the new rules %q", got, want)
		}
	})
	t.Run("changed since the snapshot: cleared", func(t *testing.T) {
		game, dir := setUp("")
		write(dir, "slot1.sav", "act 2")
		game.SyncIgnore = "logs/"
		if got := d.LastSnapshotHashUnder(game, ""); got != "" {
			t.Errorf("got %q for a save changed since its snapshot; that change would go unsnapshotted", got)
		}
	})
	t.Run("a file brought back into view: cleared", func(t *testing.T) {
		game, _ := setUp("logs/\nsettings.ini")
		game.SyncIgnore = "logs/"
		if got := d.LastSnapshotHashUnder(game, "logs/\nsettings.ini"); got != "" {
			t.Errorf("got %q, though settings.ini was never part of the recorded hash", got)
		}
	})
	t.Run("nothing recorded: nothing to restate", func(t *testing.T) {
		game, _ := setUp("")
		game.LastManifestHash = ""
		game.SyncIgnore = "logs/"
		if got := d.LastSnapshotHashUnder(game, ""); got != "" {
			t.Errorf("got %q with no recorded hash", got)
		}
	})
}
