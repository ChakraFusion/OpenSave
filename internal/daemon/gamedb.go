package daemon

import (
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/opensave/opensave/internal/delta"
	"github.com/opensave/opensave/internal/presets"
	"github.com/opensave/opensave/internal/sessions"
)

// Tracking a game by hand, with what the game database knows about it: a
// game picked from those running, found by name, or recognised from a
// program or folder chosen. See presets/gamedb.go.

func (d *Daemon) gameDB() *presets.DeviceSettings {
	if d.Scanner == nil {
		return nil
	}
	d.devMu.Lock()
	defer d.devMu.Unlock()
	return d.devDBLocked()
}

// SearchGames finds games in the game database by name.
func (d *Daemon) SearchGames(query string) []presets.GameMatch {
	return d.gameDB().Search(query, 20)
}

// IdentifyGame says which game a program or folder belongs to: a program by
// where it is installed, a folder by the game whose save location it is.
func (d *Daemon) IdentifyGame(path string) (presets.GameMatch, bool) {
	db := d.gameDB()
	info, err := os.Stat(path)
	if err != nil {
		return presets.GameMatch{}, false
	}
	if !info.IsDir() && strings.EqualFold(filepath.Ext(path), ".exe") {
		return db.IdentifyProgram(path)
	}
	if !info.IsDir() {
		path = filepath.Dir(path) // a save file: its folder
	}
	return db.IdentifyFolder(path)
}

// RunningGame is a game found running on this device.
type RunningGame struct {
	presets.GameMatch
	Exe     string `json:"exe"`
	Tracked bool   `json:"tracked"`
}

// runningMinMemory is what a program must hold to be offered as a game being
// played: a game holds hundreds of megabytes, and what is small is a tool, a
// launcher, or something a game left behind.
const runningMinMemory = 200 << 20

// RunningGames lists the programs running now that the game database knows
// as games, largest first, once per game.
func (d *Daemon) RunningGames() []RunningGame {
	if d.sessions.list == nil {
		return nil
	}
	procs, err := d.sessions.list()
	if err != nil {
		return nil
	}
	sort.Slice(procs, func(i, j int) bool { return procs[i].Memory > procs[j].Memory })
	db := d.gameDB()
	winDir := strings.ToLower(os.Getenv("WINDIR"))
	tracked := d.trackedGameKeys()
	seen := map[string]bool{}
	var out []RunningGame
	for _, p := range procs {
		if !p.Measured || p.Memory < runningMinMemory || p.Exe == "" {
			continue
		}
		if winDir != "" && strings.HasPrefix(strings.ToLower(p.Exe), winDir+string(filepath.Separator)) {
			continue
		}
		m, ok := db.IdentifyProgram(p.Exe)
		if !ok || seen[m.Name] {
			continue
		}
		seen[m.Name] = true
		out = append(out, RunningGame{GameMatch: m, Exe: p.Exe, Tracked: tracked[m.AppID] || tracked[sessions.FolderName(m.Name)]})
	}
	return out
}

// trackedGameKeys holds every tracked game's App ID and name, to say which
// running games are tracked already.
func (d *Daemon) trackedGameKeys() map[string]bool {
	keys := map[string]bool{}
	games, err := d.Store.ListGames()
	if err != nil {
		return keys
	}
	for _, g := range games {
		if g.AppID != "" {
			keys[g.AppID] = true
		}
		keys[sessions.FolderName(g.Name)] = true
	}
	return keys
}

// holdsNoSave reports a folder with no file any save is made of: empty, or
// only what a launcher keeps there (delta.NeverSynced).
func holdsNoSave(path string) bool {
	m, err := delta.BuildManifest(path)
	if err != nil {
		return false // cannot tell: not "nothing"
	}
	for p := range m.Files {
		if !delta.NeverSynced(p) {
			return false
		}
	}
	return true
}
