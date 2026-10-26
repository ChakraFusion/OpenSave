package daemon

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/opensave/opensave/internal/presets"
	"github.com/opensave/opensave/internal/store"
)

// A game tracked without an App ID is given the game database's, once, when
// its folder plainly is that game's: not for a folder holding many games'
// saves, and not again after someone took it away.
func TestLinkToGameDatabase_OncePerFolder(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("the game database below uses Windows placeholders")
	}
	home := t.TempDir()
	t.Setenv("USERPROFILE", home)
	t.Setenv("HOME", home)
	low := filepath.Join(home, "AppData", "LocalLow")
	duck := filepath.Join(low, "Soda", "Duck")
	if err := os.MkdirAll(filepath.Join(duck, "Saves"), 0o777); err != nil {
		t.Fatal(err)
	}
	d := newTestDaemon(t)
	dir := t.TempDir()
	manifest := `
Duck Game:
  files:
    "<home>/AppData/LocalLow/Soda/Duck/Saves/*.sav":
      tags: [save]
  steam:
    id: 3167020
`
	if err := os.WriteFile(filepath.Join(dir, "ludusavi-manifest.yaml"), []byte(manifest), 0o666); err != nil {
		t.Fatal(err)
	}
	d.Scanner = &presets.Scanner{CacheFile: filepath.Join(dir, "steam-app-cache.json"), GOOS: "windows"}

	game := store.Game{ID: "duckov", Name: "Duckov", SavePath: duck, ActiveBranch: "main", MaxSnapshots: 5}
	wide := store.Game{ID: "lowlow", Name: "Everything in LocalLow", SavePath: low, ActiveBranch: "main", MaxSnapshots: 5}
	for _, g := range []store.Game{game, wide} {
		if err := d.Store.CreateGame(g); err != nil {
			t.Fatal(err)
		}
	}

	if got := d.linkToGameDatabase(game); got.AppID != "3167020" || got.Name != "Duckov" {
		t.Fatalf("linked as %+v, want App ID 3167020 and the name left as it is", got)
	}
	if stored, _ := d.Store.GetGame(game.ID); stored.AppID != "3167020" || stored.CoverURL == "" {
		t.Errorf("stored as %+v", stored)
	}
	if got := d.linkToGameDatabase(wide); got.AppID != "" {
		t.Errorf("a folder holding many games' saves was linked to one: %q", got.AppID)
	}

	// Someone takes the App ID away again: it stays away.
	stored, _ := d.Store.GetGame(game.ID)
	stored.AppID, stored.CoverURL = "", ""
	if err := d.Store.UpdateGame(stored); err != nil {
		t.Fatal(err)
	}
	if got := d.linkToGameDatabase(stored); got.AppID != "" {
		t.Errorf("an App ID taken away was put back: %q", got.AppID)
	}
}
