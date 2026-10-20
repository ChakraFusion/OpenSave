package e2e

import (
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/opensave/opensave/testutil"
)

// linkFolder makes link point at the folder target: a junction on Windows,
// which needs no special rights, and a symbolic link elsewhere.
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

var memoryCards = map[string]string{
	"Mcd001.ps2": "card one",
	"Mcd002.ps2": "card two",
}

// A save folder that is itself a link — EmuDeck makes PCSX2's memcards folder
// one — is synced as the folder it points at.
//
// It read as empty. The first sync wrote the other device's memory cards
// through the link, and the next look found nothing and held the game as
// emptied. "Put them back" fetched the cards again, which still read as
// missing, and the sync after took them for deleted here: the other device
// deleted its memory cards. Reported from a Steam Deck and a desktop.
func TestLinkedSaveFolder_SyncsWithoutLosingTheOtherDevicesSave(t *testing.T) {
	name := "Linked Memcards"
	a := testutil.NewTestDaemon(t, name+"-A")
	b := testutil.NewTestDaemon(t, name+"-B")
	a.PairWith(b)
	for rel, body := range memoryCards {
		a.WriteSave(rel, body)
	}

	real := t.TempDir()
	link := filepath.Join(t.TempDir(), "memcards")
	linkFolder(t, link, real)

	restore := suppressSyncOnTrack(a, b)
	gameID := a.TrackGame(name)
	b.API(http.MethodPost, "/api/games", map[string]string{"name": name, "savePath": link}, nil)
	restore()

	syncBothWays(t, gameID, a, b)
	if e := emptiedState(t, b, gameID); e != nil {
		t.Errorf("the linked folder, holding the memory cards, was taken for emptied: %+v", e)
		// As the user did: put them back, and let the devices sync on.
		b.API(http.MethodPost, "/api/games/"+gameID+"/emptied", map[string]string{"answer": "restore"}, nil)
		syncBothWays(t, gameID, a, b)
	}
	syncBothWays(t, gameID, a, b)

	for rel, want := range memoryCards {
		if got := a.ReadSave(rel); got != want {
			t.Errorf("the other device's %s is %q, want %q", rel, got, want)
		}
		got, err := os.ReadFile(filepath.Join(real, rel))
		if err != nil || string(got) != want {
			t.Errorf("the linked folder's %s is %q (%v), want %q", rel, got, err, want)
		}
	}

	// And a card changed on the handheld, through the link, reaches the
	// desktop.
	if err := os.WriteFile(filepath.Join(real, "Mcd001.ps2"), []byte("card one, saved on the Deck"), 0o666); err != nil {
		t.Fatal(err)
	}
	b.API(http.MethodPost, "/api/games/"+gameID+"/sync", nil, nil)
	if !testutil.WaitFor(30*time.Second, func() bool { return a.ReadSave("Mcd001.ps2") == "card one, saved on the Deck" }) {
		t.Errorf("a change in the linked folder never reached the other device: %q", a.ReadSave("Mcd001.ps2"))
	}
}
