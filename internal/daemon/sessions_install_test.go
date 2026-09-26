package daemon

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/opensave/opensave/internal/sessions"
	"github.com/opensave/opensave/internal/store"
)

// A machine for these tests: a Steam library, which has installed only what a
// test puts in it, and a Games folder of the kind people keep copies in that
// no launcher knows about.
type installFixture struct {
	d        *Daemon
	steamLib string // a Steam library, or "" for a machine with no Steam
	games    string // the Games folder
}

func newInstallFixture(t *testing.T, withSteam bool) *installFixture {
	t.Helper()
	d := newTestDaemon(t)
	f := &installFixture{d: d, games: filepath.Join(t.TempDir(), "Games")}
	d.Scanner.SteamRoots = []string{}
	if withSteam {
		f.steamLib = filepath.Join(t.TempDir(), "Steam")
		mustMkdir(t, filepath.Join(f.steamLib, "steamapps"))
		d.Scanner.SteamRoots = []string{f.steamLib}
	}
	mustMkdir(t, f.games)
	d.Scanner.InstallParentDirs = []string{f.games}
	d.Scanner.EpicManifestDirs = []string{}
	return f
}

func mustMkdir(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o777); err != nil {
		t.Fatal(err)
	}
}

// program writes a stand-in program of the given size and returns its path.
func program(t *testing.T, path string, size int) string {
	t.Helper()
	mustMkdir(t, filepath.Dir(path))
	if err := os.WriteFile(path, []byte(strings.Repeat("x", size)), 0o777); err != nil {
		t.Fatal(err)
	}
	return path
}

// steamInstalls records a game as installed by Steam, and returns its folder.
func (f *installFixture) steamInstalls(t *testing.T, appID, dir string) string {
	t.Helper()
	acf := `"AppState"
{
	"appid"		"` + appID + `"
	"name"		"` + dir + `"
	"installdir"		"` + dir + `"
}`
	if err := os.WriteFile(filepath.Join(f.steamLib, "steamapps", "appmanifest_"+appID+".acf"), []byte(acf), 0o666); err != nil {
		t.Fatal(err)
	}
	folder := filepath.Join(f.steamLib, "steamapps", "common", dir)
	mustMkdir(t, folder)
	return folder
}

func (f *installFixture) track(t *testing.T, id, name, appID string) store.Game {
	t.Helper()
	g := store.Game{ID: id, Name: name, AppID: appID, SavePath: t.TempDir(), ActiveBranch: "main", MaxSnapshots: 20}
	if err := f.d.Store.CreateGame(g); err != nil {
		t.Fatal(err)
	}
	return g
}

// playFor runs a session: each poll sees the given programs running, then the
// session ends 45 minutes after it began.
func (f *installFixture) playFor(t *testing.T, gameID string, polls ...[]string) {
	t.Helper()
	for _, running := range polls {
		var procs []sessions.Proc
		for i, exe := range running {
			procs = append(procs, sessions.Proc{PID: 100 + i, Exe: exe})
		}
		f.d.sessions.list = func() ([]sessions.Proc, error) { return procs, nil }
		f.d.PollSessions()
	}
	since := f.d.PlayingSince(gameID)
	if since.IsZero() {
		t.Fatal("the game was never seen as playing")
	}
	f.d.sessions.tracker.Finish(gameID, since.Add(45*time.Minute))
}

// A copy kept outside Steam has no way to be launched until it is given a
// program. Played once, it has one: the game itself, not the small program
// that started it, nor the crash reporter that ran beside it the whole time —
// though that one is the larger file.
func TestSessionLearnsTheProgramOfAGameSteamCannotStart(t *testing.T) {
	f := newInstallFixture(t, true)
	g := f.track(t, "elden-ring", "Elden Ring", "1245620")
	dir := filepath.Join(f.games, "Elden Ring", "Game")
	starter := program(t, filepath.Join(dir, "start_protected_game.exe"), 2_000)
	game := program(t, filepath.Join(dir, "eldenring.exe"), 80_000)
	reporter := program(t, filepath.Join(dir, "CrashReporter.exe"), 120_000)

	f.playFor(t, g.ID,
		[]string{starter},
		[]string{game, reporter},
		[]string{game, reporter},
		[]string{game, reporter},
	)
	got, err := f.d.Store.GetGame(g.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.ExePath != game {
		t.Errorf("the program learned is %q, want the game, %q", got.ExePath, game)
	}
}

// A game Steam has installed is Steam's to start: giving it a program would
// have Launch bypass Steam for it.
func TestSessionLeavesASteamGameToSteam(t *testing.T) {
	f := newInstallFixture(t, true)
	g := f.track(t, "hades", "Hades", "1145360")
	game := program(t, filepath.Join(f.steamInstalls(t, "1145360", "Hades"), "Hades.exe"), 50_000)

	f.playFor(t, g.ID, []string{game}, []string{game})
	got, err := f.d.Store.GetGame(g.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.ExePath != "" {
		t.Errorf("a Steam game was given the program %q", got.ExePath)
	}
}

// And a program someone set is never replaced.
func TestSessionKeepsAProgramSomeoneSet(t *testing.T) {
	f := newInstallFixture(t, true)
	g := f.track(t, "elden-ring", "Elden Ring", "1245620")
	dir := filepath.Join(f.games, "Elden Ring", "Game")
	chosen := program(t, filepath.Join(dir, "start_protected_game.exe"), 2_000)
	game := program(t, filepath.Join(dir, "eldenring.exe"), 80_000)
	g.ExePath = chosen
	if err := f.d.Store.UpdateGame(g); err != nil {
		t.Fatal(err)
	}

	f.playFor(t, g.ID, []string{game}, []string{game})
	if got, _ := f.d.Store.GetGame(g.ID); got.ExePath != chosen {
		t.Errorf("the program set, %q, was replaced with %q", chosen, got.ExePath)
	}
}

func TestInstallState(t *testing.T) {
	f := newInstallFixture(t, true)
	program(t, filepath.Join(f.games, "Elden Ring", "Game", "eldenring.exe"), 10)
	f.steamInstalls(t, "1145360", "Hades")
	gone := filepath.Join(t.TempDir(), "gone.exe")
	for _, c := range []struct {
		why  string
		game store.Game
		want string
	}{
		{"in a Games folder", store.Game{Name: "Elden Ring", AppID: "1245620"}, InstallFound},
		{"installed by Steam", store.Game{Name: "Hades", AppID: "1145360"}, InstallFound},
		{"a program that is there", store.Game{Name: "X", ExePath: program(t, filepath.Join(t.TempDir(), "x.exe"), 10)}, InstallFound},
		{"Steam has no such app, nor any folder", store.Game{Name: "Animal Well", AppID: "813230"}, InstallNotFound},
		{"its program is gone, and Steam lacks it", store.Game{Name: "Animal Well", AppID: "813230", ExePath: gone}, InstallNotFound},
		{"no App ID: no telling", store.Game{Name: "Some Emulator Save"}, ""},
	} {
		if got := f.d.InstallState(c.game); got != c.want {
			t.Errorf("%s: %q, want %q", c.why, got, c.want)
		}
	}

	// With no Steam on the machine, an App ID not installed says nothing.
	noSteam := newInstallFixture(t, false)
	if got := noSteam.d.InstallState(store.Game{Name: "Animal Well", AppID: "813230"}); got != "" {
		t.Errorf("no Steam here, yet a Steam game was reported %q", got)
	}
}
