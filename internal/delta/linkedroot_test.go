package delta

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

// linkFolder makes link point at the folder target, as an emulator setup
// does: a junction on Windows, which needs no special rights, and a symbolic
// link elsewhere.
func linkFolder(t *testing.T, link, target string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		if out, err := exec.Command("cmd", "/c", "mklink", "/J", filepath.FromSlash(link), filepath.FromSlash(target)).CombinedOutput(); err != nil {
			t.Fatalf("mklink /J: %v: %s", err, out)
		}
		return
	}
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
}

func writeCards(t *testing.T, dir string) {
	t.Helper()
	for name, body := range map[string]string{"Mcd001.ps2": "card one", "Mcd002.ps2": "card two"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o666); err != nil {
			t.Fatal(err)
		}
	}
}

func sameFolder(t *testing.T, a, b string) bool {
	t.Helper()
	sa, err := os.Stat(a)
	if err != nil {
		t.Fatal(err)
	}
	sb, err := os.Stat(b)
	if err != nil {
		t.Fatal(err)
	}
	return os.SameFile(sa, sb)
}

// A save folder that is itself a link is read through to the folder it
// points at. It used to read as empty, with no error.
func TestLinkedSaveFolder_IsReadThrough(t *testing.T) {
	real := t.TempDir()
	writeCards(t, real)
	link := filepath.Join(t.TempDir(), "memcards")
	linkFolder(t, link, real)

	if got := RealRoot(link); !sameFolder(t, got, real) {
		t.Errorf("RealRoot(link) = %s, want the folder it points at", got)
	}
	m, err := BuildManifest(link)
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Files) != 2 {
		t.Fatalf("a linked save folder read as %d files, want 2: %v", len(m.Files), m.Files)
	}
	for _, name := range []string{"Mcd001.ps2", "Mcd002.ps2"} {
		if _, ok := m.Files[name]; !ok {
			t.Errorf("%s missing, or not named relative to the save folder: %v", name, m.Files)
		}
	}
}

// A link to a link is followed to the end.
func TestLinkedSaveFolder_ChainIsFollowed(t *testing.T) {
	real := t.TempDir()
	writeCards(t, real)
	first := filepath.Join(t.TempDir(), "first")
	linkFolder(t, first, real)
	second := filepath.Join(t.TempDir(), "second")
	linkFolder(t, second, first)
	if got := RealRoot(second); !sameFolder(t, got, real) {
		t.Errorf("RealRoot(link to a link) = %s, want the folder at the end", got)
	}
}

// A folder that is not a link, and a path that does not exist, come back as
// they are.
func TestLinkedSaveFolder_PlainFolderUnchanged(t *testing.T) {
	plain := t.TempDir()
	if got := RealRoot(plain); got != plain {
		t.Errorf("RealRoot(plain folder) = %s, want it unchanged", got)
	}
	missing := filepath.Join(plain, "nope")
	if got := RealRoot(missing); got != missing {
		t.Errorf("RealRoot(missing) = %s, want it unchanged", got)
	}
}

// Links inside a save folder are still not followed: only the save folder
// itself is read through.
func TestLinkedSaveFolder_LinksInsideStillSkipped(t *testing.T) {
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "secret.txt"), []byte("not a save"), 0o666); err != nil {
		t.Fatal(err)
	}
	real := t.TempDir()
	writeCards(t, real)
	linkFolder(t, filepath.Join(real, "elsewhere"), outside)
	link := filepath.Join(t.TempDir(), "memcards")
	linkFolder(t, link, real)

	m, err := BuildManifest(link)
	if err != nil {
		t.Fatal(err)
	}
	for name := range m.Files {
		if filepath.Base(name) == "secret.txt" {
			t.Errorf("a link inside the save folder was followed: %s", name)
		}
	}
	if len(m.Files) != 2 {
		t.Errorf("read %d files, want the 2 cards: %v", len(m.Files), m.Files)
	}
}
