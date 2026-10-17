package snapshot

import (
	"archive/zip"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// A restore or branch switch whose snapshot cannot be put back must change
// nothing: the save stays, and so does the branch. Both used to empty the
// save folder first and find out afterwards. The scenarios are the ones in
// GitHub #59 and #60, reported by Seagulls with tests these are adapted from.

// writeZip writes an archive holding the named entries, each a small file
// (or a folder, for a name ending in "/"), with valid checksums.
func writeZip(t *testing.T, path string, names ...string) {
	t.Helper()
	out, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	w := zip.NewWriter(out)
	for _, name := range names {
		f, err := w.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.HasSuffix(name, "/") {
			if _, err := f.Write([]byte("bytes")); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	if err := out.Close(); err != nil {
		t.Fatal(err)
	}
}

func readOrMissing(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		return "<missing>"
	}
	return string(b)
}

// branchWhoseSnapshotIs sets up a game on main holding work no snapshot has,
// and a branch "other" whose one snapshot's archive is then spoiled.
func branchWhoseSnapshotIs(t *testing.T, spoil func(zipPath string)) *testEnv {
	t.Helper()
	env := setup(t)
	writeSave(t, env.saveDir, "slot.sav", "INITIAL")
	if _, err := env.mgr.CreateBranch("game1", "other", true); err != nil {
		t.Fatal(err)
	}
	snaps, err := env.store.ListSnapshots("game1", "other")
	if err != nil || len(snaps) == 0 {
		t.Fatalf("the new branch has no snapshot: %v", err)
	}
	spoil(snaps[0].ZipPath)
	writeSave(t, env.saveDir, "slot.sav", "CURRENT-ONLY")
	return env
}

func TestSwitchBranch_ToAnUnusableSnapshotChangesNothing(t *testing.T) {
	for why, spoil := range map[string]func(string){
		"its archive is missing": func(p string) {
			if err := os.Remove(p); err != nil {
				t.Fatal(err)
			}
		},
		"its archive is not an archive": func(p string) {
			if err := os.WriteFile(p, []byte("not a zip"), 0o666); err != nil {
				t.Fatal(err)
			}
		},
		"it holds a file where a folder must go": func(p string) {
			writeZip(t, p, "block", "block/child.sav")
		},
	} {
		t.Run(why, func(t *testing.T) {
			env := branchWhoseSnapshotIs(t, spoil)

			if err := env.mgr.SwitchBranch("game1", "other"); err == nil {
				t.Error("the switch reported success")
			}
			if got := readOrMissing(t, filepath.Join(env.saveDir, "slot.sav")); got != "CURRENT-ONLY" {
				t.Errorf("the save was changed: slot.sav = %q", got)
			}
			if game, err := env.store.GetGame("game1"); err != nil || game.ActiveBranch != "main" {
				t.Errorf("the active branch moved: %q (%v)", game.ActiveBranch, err)
			}
		})
	}
}

// A good branch still switches, both ways.
func TestSwitchBranch_StillSwitches(t *testing.T) {
	env := setup(t)
	writeSave(t, env.saveDir, "slot.sav", "ON-MAIN")
	if _, err := env.mgr.CreateBranch("game1", "other", true); err != nil {
		t.Fatal(err)
	}
	writeSave(t, env.saveDir, "slot.sav", "STILL-MAIN")
	if err := env.mgr.SwitchBranch("game1", "other"); err != nil {
		t.Fatal(err)
	}
	if got := readOrMissing(t, filepath.Join(env.saveDir, "slot.sav")); got != "ON-MAIN" {
		t.Errorf("after switching to other, slot.sav = %q, want the branch's ON-MAIN", got)
	}
	if err := env.mgr.SwitchBranch("game1", "main"); err != nil {
		t.Fatal(err)
	}
	if got := readOrMissing(t, filepath.Join(env.saveDir, "slot.sav")); got != "STILL-MAIN" {
		t.Errorf("after switching back, slot.sav = %q, want STILL-MAIN", got)
	}
}

func TestRestore_AnArchiveThatCannotBeLaidOutChangesNothing(t *testing.T) {
	env := setup(t)
	writeSave(t, env.saveDir, "important.sav", "CURRENT-ONLY")
	snap, err := env.mgr.Create("game1", "baseline", false)
	if err != nil {
		t.Fatal(err)
	}
	writeZip(t, snap.ZipPath, "block", "block/child.sav")
	if err := VerifyArchive(snap.ZipPath); err != nil {
		t.Fatalf("the checksums should pass, only the layout is wrong: %v", err)
	}

	if _, err := env.mgr.Restore("game1", snap.ID); err == nil {
		t.Error("the restore reported success")
	}
	if got := readOrMissing(t, filepath.Join(env.saveDir, "important.sav")); got != "CURRENT-ONLY" {
		t.Errorf("the save was changed: important.sav = %q", got)
	}
}

// Every caller of UnzipRoots — an imported backup among them — is refused
// before any of its folders is cleared.
func TestUnzipRoots_RefusesBeforeClearingAnyLocation(t *testing.T) {
	primary, extra := t.TempDir(), t.TempDir()
	writeSave(t, primary, "important.sav", "LOCAL-PRIMARY")
	writeSave(t, extra, "config.ini", "LOCAL-EXTRA")
	archive := filepath.Join(t.TempDir(), "crafted.zip")
	writeZip(t, archive, "archive.sav", RootPrefix+"config/", RootPrefix+"config/block", RootPrefix+"config/block/child.ini")

	if _, err := UnzipRoots(archive, primary, map[string]string{"config": extra}); err == nil {
		t.Error("the archive was extracted")
	}
	if got := readOrMissing(t, filepath.Join(primary, "important.sav")); got != "LOCAL-PRIMARY" {
		t.Errorf("the save folder was changed: important.sav = %q", got)
	}
	if got := readOrMissing(t, filepath.Join(extra, "config.ini")); got != "LOCAL-EXTRA" {
		t.Errorf("the extra location was changed: config.ini = %q", got)
	}
}

func TestCheckExtractable(t *testing.T) {
	dir := t.TempDir()
	for name, c := range map[string]struct {
		entries []string
		ok      bool
	}{
		"ordinary files and folders":      {[]string{"a.sav", "slots/", "slots/1.sav", "slots/2.sav"}, true},
		"a file, then a file inside it":   {[]string{"block", "block/child.sav"}, false},
		"a file inside, then the file":    {[]string{"block/child.sav", "block"}, false},
		"a folder entry and a file":       {[]string{"block/", "block"}, false},
		"an entry escaping its folder":    {[]string{"../outside.sav"}, false},
		"the same clash in an extra one":  {[]string{RootPrefix + "config/x", RootPrefix + "config/x/y"}, false},
		"the same name in two locations":  {[]string{"x", RootPrefix + "config/x/y"}, true},
		"the same file twice":             {[]string{"slots/1.sav", "slots/1.sav"}, false},
		"the same file twice in an extra": {[]string{RootPrefix + "config/x", RootPrefix + "config/x"}, false},
		"one file in two locations":       {[]string{"x", RootPrefix + "config/x"}, true},
	} {
		path := filepath.Join(dir, strings.ReplaceAll(name, " ", "_")+".zip")
		writeZip(t, path, c.entries...)
		if err := CheckExtractable(path); (err == nil) != c.ok {
			t.Errorf("%s: CheckExtractable = %v, want ok=%v", name, err, c.ok)
		}
	}
}

// On a filesystem that ignores case, two names differing only in case are one
// file, and parallel extraction would write it twice at once.
func TestCheckExtractable_NamesDifferingOnlyInCase(t *testing.T) {
	path := filepath.Join(t.TempDir(), "case.zip")
	writeZip(t, path, "Slots/1.sav", "slots/1.sav")
	err := CheckExtractable(path)
	if caseInsensitive := runtime.GOOS == "windows" || runtime.GOOS == "darwin"; caseInsensitive && err == nil {
		t.Errorf("CheckExtractable = nil, want the two names refused as one file")
	} else if !caseInsensitive && err != nil {
		t.Errorf("CheckExtractable = %v, want two different files accepted", err)
	}
}
