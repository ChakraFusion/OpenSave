package daemon

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/opensave/opensave/internal/presets"
	"github.com/opensave/opensave/internal/store"
)

// A folder inside a tracked game's folder, or holding it, is not tracked as
// another game: the same files would be watched, snapshotted and synced twice.
// The very same folder keeps its own refusal.
func TestTrackGame_RefusesAFolderInsideAnother(t *testing.T) {
	d := newTestDaemon(t)
	outer := t.TempDir()
	inner := filepath.Join(outer, "Saves")
	if err := os.MkdirAll(inner, 0o777); err != nil {
		t.Fatal(err)
	}
	if _, err := d.TrackGame(store.Game{Name: "Duckov", SavePath: inner}); err != nil {
		t.Fatal(err)
	}
	if _, err := d.TrackGame(store.Game{Name: "Escape From Duckov", SavePath: outer}); err == nil {
		t.Error("a folder holding a tracked game's was tracked as another game")
	} else if !strings.Contains(err.Error(), "Duckov") {
		t.Errorf("the refusal does not name the game: %v", err)
	}
	deeper := filepath.Join(inner, "slot1")
	if err := os.MkdirAll(deeper, 0o777); err != nil {
		t.Fatal(err)
	}
	if _, err := d.TrackGame(store.Game{Name: "Duckov Slot", SavePath: deeper}); err == nil {
		t.Error("a folder inside a tracked game's was tracked as another game")
	}
	if _, err := d.TrackGame(store.Game{Name: "Duckov Again", SavePath: inner}); err == nil || !strings.Contains(err.Error(), "already tracks this folder") {
		t.Errorf("the same folder again: %v, want the usual \"already tracks this folder\"", err)
	}
	if _, err := d.TrackGame(store.Game{Name: "Unrelated", SavePath: t.TempDir()}); err != nil {
		t.Errorf("an unrelated folder was refused: %v", err)
	}
}

// A game picked from the game database says which entry here it already is:
// one with its App ID, or one whose folder is, holds or is held by a save
// location the database knows for it.
func TestMarkTracked_NamesTheEntryAGameIsAlready(t *testing.T) {
	d := newTestDaemon(t)
	saves := t.TempDir()
	g, err := d.TrackGame(store.Game{Name: "Duckov", SavePath: saves})
	if err != nil {
		t.Fatal(err)
	}

	byFolder := presets.GameMatch{Name: "Escape From Duckov", SavePaths: []string{filepath.Join(saves, "Saves")}}
	d.markTracked(&byFolder)
	if byFolder.TrackedID != g.ID || byFolder.TrackedName != "Duckov" {
		t.Errorf("by folder: tracked as %q (%q), want %q", byFolder.TrackedID, byFolder.TrackedName, g.ID)
	}

	g.AppID = "3167020"
	if err := d.Store.UpdateGame(g); err != nil {
		t.Fatal(err)
	}
	byID := presets.GameMatch{Name: "Escape From Duckov", AppID: "3167020"}
	d.markTracked(&byID)
	if byID.TrackedID != g.ID {
		t.Errorf("by App ID: tracked as %q, want %q", byID.TrackedID, g.ID)
	}

	other := presets.GameMatch{Name: "Something Else", AppID: "1", SavePaths: []string{t.TempDir()}}
	d.markTracked(&other)
	if other.TrackedID != "" {
		t.Errorf("an unrelated game was marked as tracked: %q", other.TrackedID)
	}
}
