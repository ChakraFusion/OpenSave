package daemon

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/opensave/opensave/internal/store"
)

// freeDriveLetter returns a drive letter not mounted here, or "".
func freeDriveLetter() string {
	for c := 'Z'; c >= 'D'; c-- {
		if _, err := os.Stat(string(c) + `:\`); err != nil {
			return string(c)
		}
	}
	return ""
}

// A game tracked elsewhere with its save on a drive this device lacks, while
// the identical path exists on another drive here: the folder is offered, not
// switched to — it could be an old copy — and the game is switched only when
// the user accepts that folder.
func TestRelocateToOtherDrive_IsOfferedThenAccepted(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("drive letters are a Windows thing")
	}
	free := freeDriveLetter()
	if free == "" {
		t.Skip("no free drive letter")
	}
	d := newTestDaemon(t)
	real := filepath.Join(t.TempDir(), "SteamLibrary", "steamapps", "common", "Game", "save")
	if err := os.MkdirAll(real, 0o777); err != nil {
		t.Fatal(err)
	}
	elsewhere := free + ":" + real[2:] // same path, absent drive
	if err := d.Store.CreateGame(store.Game{ID: "g", Name: "Game", SavePath: elsewhere, ActiveBranch: "main", AutoSync: true}); err != nil {
		t.Fatal(err)
	}

	d.noteMissing("g", "Game", elsewhere, true)

	g, _ := d.Store.GetGame("g")
	if g.SavePath != elsewhere {
		t.Fatalf("save path switched to %s without asking", g.SavePath)
	}
	if got := d.RelocateSuggestion("g"); !strings.EqualFold(got, real) {
		t.Fatalf("offered %q, want %s", got, real)
	}
	d.missingMu.Lock()
	missing := d.missing["g"]
	d.missingMu.Unlock()
	if !missing {
		t.Error("the game is not marked missing while the offer is open")
	}

	if err := d.AcceptRelocation("g", `C:\somewhere\else`); err == nil {
		t.Error("accepted a folder that was not the one offered")
	}
	if err := d.AcceptRelocation("g", real); err != nil {
		t.Fatal(err)
	}
	g, _ = d.Store.GetGame("g")
	if !strings.EqualFold(g.SavePath, real) {
		t.Fatalf("save path = %s after accepting, want %s", g.SavePath, real)
	}
	if d.RelocateSuggestion("g") != "" {
		t.Error("still offered after being accepted")
	}
}

// "Not this one": the folder is not offered again.
func TestRelocateToOtherDrive_Dismissed(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("drive letters are a Windows thing")
	}
	free := freeDriveLetter()
	if free == "" {
		t.Skip("no free drive letter")
	}
	d := newTestDaemon(t)
	real := filepath.Join(t.TempDir(), "Backup", "Game", "save")
	if err := os.MkdirAll(real, 0o777); err != nil {
		t.Fatal(err)
	}
	elsewhere := free + ":" + real[2:]
	if err := d.Store.CreateGame(store.Game{ID: "b", Name: "Backup Game", SavePath: elsewhere, ActiveBranch: "main", AutoSync: true}); err != nil {
		t.Fatal(err)
	}
	d.noteMissing("b", "Backup Game", elsewhere, true)
	if d.RelocateSuggestion("b") == "" {
		t.Fatal("setup: nothing offered")
	}
	if err := d.DismissRelocation("b"); err != nil {
		t.Fatal(err)
	}
	d.missingMu.Lock()
	delete(d.relocateChecked, "b") // as if relocateRecheck had passed
	d.missingMu.Unlock()
	d.noteMissing("b", "Backup Game", elsewhere, true)
	if got := d.RelocateSuggestion("b"); got != "" {
		t.Errorf("offered %s again after it was dismissed", got)
	}
	g, _ := d.Store.GetGame("b")
	if g.SavePath != elsewhere {
		t.Errorf("save path changed to %s", g.SavePath)
	}
}

// Not found: said once, and not searched again on every reconcile.
func TestRelocateNotFound_IsNotRepeated(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("drive letters are a Windows thing")
	}
	free := freeDriveLetter()
	if free == "" {
		t.Skip("no free drive letter")
	}
	d := newTestDaemon(t)
	path := free + `:\NoSuchLibrary\steamapps\common\Nothing\save`
	if err := d.Store.CreateGame(store.Game{ID: "n", Name: "Nothing", SavePath: path, ActiveBranch: "main", AutoSync: true}); err != nil {
		t.Fatal(err)
	}
	if d.suggestOtherDrive("n", "Nothing", path) {
		t.Fatal("relocated to a folder that does not exist")
	}
	first := d.relocateChecked["n"]
	d.suggestOtherDrive("n", "Nothing", path)
	if !d.relocateChecked["n"].Equal(first) {
		t.Error("searched again straight away; it should wait relocateRecheck")
	}
}
