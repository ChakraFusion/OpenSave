package daemon

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/opensave/opensave/internal/sessions"
	"github.com/opensave/opensave/internal/store"
)

// Play sessions: which tracked game is being played, and what happens when it
// stops. See package sessions for how a game is recognised.
//
// When a session ends, the save as it was left gets a snapshot of its own —
// "After playing (1 h 12 min)" — and is synced to the other devices, so it is
// there when you sit down at one of them. Nothing is taken when the save did
// not change while the game ran.

const (
	// sessionPoll is how often running programs are looked at.
	sessionPoll = 10 * time.Second
	// sessionGrace is how long a game may be gone before its session ends.
	sessionGrace = 25 * time.Second
	// shortestSession is the least that counts as having played: less is a
	// launcher opening and closing, or a crash at start.
	shortestSession = 30 * time.Second
	// installDirsFresh is how long what was read about install folders is
	// trusted before it is read again.
	installDirsFresh = 10 * time.Minute
)

// sessionState is the daemon's side of play sessions.
type sessionState struct {
	tracker *sessions.Tracker
	// list finds running programs; a test gives its own.
	list func() ([]sessions.Proc, error)

	mu sync.Mutex
	// checkpoint is when each playing game last had a snapshot taken while
	// it was played (holdSnapshotWhilePlaying).
	checkpoint map[string]time.Time
	// programs: what programsIn found in each install folder.
	programs map[string]programList
	// active: stretches of changes no session accounts for (noteActivity);
	// warned: games already said to change with no program seen.
	active map[string]*activity
	warned map[string]bool
	// lastChange: when each game's save last changed here.
	lastChange map[string]time.Time
	// startHash is each playing game's save as it was when play began, to
	// tell afterwards whether the session changed it.
	startHash map[string]string
	// seen counts, for each game being played whose program is still to be
	// learned, the polls each program from its folder was running in.
	seen     map[string]map[string]int
	installs struct {
		at       time.Time
		byAppID  map[string]string
		byFolder map[string]string
		steam    bool // a Steam library was found at all
	}
}

func (d *Daemon) initSessions() {
	d.sessions.list = sessions.List
	d.sessions.startHash = map[string]string{}
	d.sessions.seen = map[string]map[string]int{}
	d.sessions.tracker = &sessions.Tracker{
		Grace:   sessionGrace,
		OnStart: d.sessionStarted,
		OnEnd:   d.sessionEnded,
	}
}

// SessionTracker is the tracker, for the API and the CLI's `wrap`.
func (d *Daemon) SessionTracker() *sessions.Tracker { return d.sessions.tracker }

// PlayingSince says when a game's current session began, or zero.
func (d *Daemon) PlayingSince(gameID string) time.Time {
	if d.sessions.tracker == nil {
		return time.Time{}
	}
	return d.sessions.tracker.Playing()[gameID]
}

// PollSessions looks at what is running once.
func (d *Daemon) PollSessions() {
	targets, learn, err := d.sessionTargets()
	if err != nil || len(targets) == 0 {
		d.sessions.tracker.Poll(nil, time.Now())
		return
	}
	procs, err := d.sessions.list()
	if err != nil {
		return
	}
	running := sessions.Running(procs, targets)
	d.notePrograms(procs, targets, learn, running)
	d.sessions.tracker.Poll(running, time.Now())
}

func (d *Daemon) runSessions(ctx context.Context) {
	ticker := time.NewTicker(sessionPoll)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			d.PollSessions()
		}
	}
}

// launcherPrograms are launchers, not games: a game whose launch program is
// one of these is not recognised by it, or every time the launcher ran would
// count as playing.
var launcherPrograms = map[string]bool{
	"steam.exe": true, "steam": true, "epicgameslauncher.exe": true, "galaxyclient.exe": true,
	"heroic.exe": true, "heroic": true, "lutris": true, "playnite.desktopapp.exe": true,
	"playnite.fullscreenapp.exe": true, "eadesktop.exe": true, "ubisoftconnect.exe": true,
	"battle.net.exe": true, "xboxpcapp.exe": true,
}

// programName is a launch program's file name, lower-cased, whichever
// separator its path was written with: a path from Windows names Steam as
// steam.exe on any system.
func programName(exe string) string {
	exe = strings.ReplaceAll(exe, `\`, "/")
	return strings.ToLower(exe[strings.LastIndex(exe, "/")+1:])
}

// sessionTargets is every tracked game to look for, and the ones whose
// program is to be learned from what runs in their folder (see learnProgram).
func (d *Daemon) sessionTargets() ([]sessions.Target, map[string]bool, error) {
	games, err := d.Store.ListGames()
	if err != nil {
		return nil, nil, err
	}
	byAppID, byFolder, _ := d.installDirs()
	targets := make([]sessions.Target, 0, len(games))
	learn := map[string]bool{}
	for _, g := range games {
		t := sessions.Target{GameID: g.ID, AppID: g.AppID}
		exe := strings.TrimSpace(g.ExePath)
		if exe != "" && !launcherPrograms[programName(exe)] {
			t.Exe = exe
			if dir := programFolder(exe); dir != "" {
				t.Dirs = append(t.Dirs, dir)
			}
		}
		dir, viaSteam := installFolder(g, byAppID, byFolder)
		if dir != "" {
			t.Dirs = append(t.Dirs, dir)
		}
		for _, dir := range t.Dirs {
			t.Programs = append(t.Programs, d.programsIn(dir)...)
		}
		// Steam launches the games it has installed; the rest need a
		// program to start them, and none was given.
		if dir != "" && !viaSteam && exe == "" {
			learn[g.ID] = true
		}
		targets = append(targets, t)
	}
	return targets, learn, nil
}

// installFolder is where a game is installed on this device, if that is
// known, and whether Steam installed it there.
func installFolder(g store.Game, byAppID, byFolder map[string]string) (dir string, viaSteam bool) {
	if dir := byAppID[g.AppID]; g.AppID != "" && dir != "" {
		return dir, true
	}
	if dir := byFolder[strings.ToLower(g.Name)]; dir != "" && specificEnough(dir) {
		return dir, false
	}
	if dir := byFolder[folderish(g.Name)]; dir != "" && specificEnough(dir) {
		return dir, false
	}
	return "", false
}

// Install states, as a game's entry reports them.
const (
	InstallFound    = "found"
	InstallNotFound = "not-found"
)

// InstallState says whether a game is installed on this device: found, not
// found, or "" when there is no telling.
//
// "Not found" is said only when it is certain enough to be worth saying: the
// game has a Steam App ID, Steam is here and does not have it, and it is not
// in any other folder games are kept in. A game with no App ID — an
// emulator's save, one installed somewhere of the user's own choosing — is
// not reported missing merely because this device cannot see where it is.
func (d *Daemon) InstallState(g store.Game) string {
	if exe := strings.TrimSpace(g.ExePath); exe != "" && !launcherPrograms[programName(exe)] {
		if _, err := os.Stat(exe); err == nil {
			return InstallFound
		}
	}
	byAppID, byFolder, steam := d.installDirs()
	if dir, _ := installFolder(g, byAppID, byFolder); dir != "" {
		return InstallFound
	}
	if g.AppID != "" && steam {
		return InstallNotFound
	}
	return ""
}

// SteamInstall reads afresh whether Steam on this device has a game
// installed, and whether there is a Steam here to ask at all. Fresh, not the
// session poll's copy: it answers a click, and a game installed a minute ago
// must launch. dir is where the game was found otherwise, if it was.
func (d *Daemon) SteamInstall(g store.Game) (installed, steamHere bool, dir string) {
	d.sessions.mu.Lock()
	d.sessions.installs.at = time.Time{} // read again below
	d.sessions.mu.Unlock()
	byAppID, byFolder, steam := d.installDirs()
	dir, viaSteam := installFolder(g, byAppID, byFolder)
	return viaSteam, steam, dir
}

// notePrograms counts, for the games whose program is being learned, which
// programs from their folders are running.
func (d *Daemon) notePrograms(procs []sessions.Proc, targets []sessions.Target, learn map[string]bool, running map[string]int) {
	d.sessions.mu.Lock()
	defer d.sessions.mu.Unlock()
	for _, t := range targets {
		if _, playing := running[t.GameID]; !playing || !learn[t.GameID] {
			continue
		}
		for _, p := range procs {
			if !sessions.InFolder(p, t) {
				continue
			}
			if d.sessions.seen[t.GameID] == nil {
				d.sessions.seen[t.GameID] = map[string]int{}
			}
			d.sessions.seen[t.GameID][p.Exe]++
		}
	}
}

// helperNames mark programs that run from a game's folder beside the game
// without being it: crash reporters, anti-cheat, installers.
var helperNames = []string{
	"crash", "reporter", "werfault", "unins", "redist", "dxsetup",
	"anticheat", "battleye", "beservice", "helper", "updater",
}

// learnProgram gives a game the program it was just played with, when it had
// none and Steam cannot start it: a copy kept in D:\Games, say. Without one,
// Launch had nothing to run, or went to Steam and asked to install the game.
//
// The program is the one that ran for most of the session. A game started
// through a small program of its own shows that one for a poll or two, and
// the game itself for the rest; a crash reporter runs as long as the game,
// but is named as one and skipped. Of two running equally long, the larger
// file is the game.
func (d *Daemon) learnProgram(gameID string, seen map[string]int) {
	best, bestN, bestSize := "", 0, int64(-1)
	for exe, n := range seen {
		name := programName(exe)
		helper := false
		for _, h := range helperNames {
			if strings.Contains(name, h) {
				helper = true
				break
			}
		}
		info, err := os.Stat(exe)
		if helper || err != nil {
			continue
		}
		size := info.Size()
		if n > bestN || (n == bestN && (size > bestSize || (size == bestSize && exe < best))) {
			best, bestN, bestSize = exe, n, size
		}
	}
	if best == "" {
		return
	}
	game, err := d.Store.GetGame(gameID)
	if err != nil || strings.TrimSpace(game.ExePath) != "" {
		return // one was set meanwhile, and that one is the user's
	}
	game.ExePath = best
	if err := d.Store.UpdateGame(game); err != nil {
		d.Log.Log("warn", fmt.Sprintf("could not remember how %q is started: %v", game.Name, err))
		return
	}
	d.Log.Log("info", fmt.Sprintf("%q runs from %s; Launch starts it from there now", game.Name, best))
}

// programFolder is the folder a game's launch program is in, whose programs
// all count as the game running.
//
// The program chosen is often not the one that stays running. Many games
// start through a small program of their own that hands over and exits —
// Elden Ring's start_protected_game.exe starts eldenring.exe beside it — so
// matching the chosen program alone saw the game for a few seconds, or not
// at all. Whichever of them was picked, the game runs from that folder.
//
// Not for a shortcut, which is kept anywhere and points somewhere else, and
// not for a folder near a drive root, which holds more than one game.
func programFolder(exe string) string {
	switch strings.ToLower(filepath.Ext(exe)) {
	case ".lnk", ".url":
		return ""
	}
	dir := filepath.Dir(exe)
	if !specificEnough(dir) {
		return ""
	}
	return dir
}

// folderish is a game name as a folder is usually named: "Hollow Knight: Silksong"
// is in "hollow knight silksong".
func folderish(name string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(name) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == ' ' {
			b.WriteRune(r)
		}
	}
	return strings.Join(strings.Fields(b.String()), " ")
}

// specificEnough keeps a folder from claiming too much: a drive root, or a
// folder right under one, holds more than one game.
func specificEnough(dir string) bool {
	clean := filepath.Clean(dir)
	parts := strings.FieldsFunc(clean, func(r rune) bool { return r == '/' || r == '\\' })
	return len(parts) >= 3
}

func (d *Daemon) installDirs() (byAppID, byFolder map[string]string, steam bool) {
	d.sessions.mu.Lock()
	defer d.sessions.mu.Unlock()
	if d.Scanner == nil {
		return nil, nil, false
	}
	in := &d.sessions.installs
	if time.Since(in.at) > installDirsFresh || in.byAppID == nil {
		in.byAppID, in.byFolder = d.Scanner.InstallDirs()
		in.steam = d.Scanner.HasSteam()
		in.at = time.Now()
	}
	return in.byAppID, in.byFolder, in.steam
}

func (d *Daemon) sessionStarted(gameID string, at time.Time) {
	game, err := d.Store.GetGame(gameID)
	if err != nil {
		return
	}
	hash, _ := d.currentContentHash(game)
	d.sessions.mu.Lock()
	d.sessions.startHash[gameID] = hash
	d.sessions.mu.Unlock()
	d.Log.Log("info", fmt.Sprintf("playing %q", game.Name))
	if d.OnGameChanged != nil {
		d.OnGameChanged(gameID)
	}
}

func (d *Daemon) sessionEnded(gameID string, started, ended time.Time) {
	// The session's end keeps the save and syncs it; a stretch of changes
	// it overlapped would otherwise hold the sync back for quietAfter more.
	d.endActivity(gameID)
	d.sessions.mu.Lock()
	startHash := d.sessions.startHash[gameID]
	delete(d.sessions.startHash, gameID)
	delete(d.sessions.checkpoint, gameID)
	seen := d.sessions.seen[gameID]
	delete(d.sessions.seen, gameID)
	d.sessions.mu.Unlock()

	game, err := d.Store.GetGame(gameID)
	if err != nil {
		return
	}
	defer func() {
		if d.OnGameChanged != nil {
			d.OnGameChanged(gameID)
		}
	}()
	length := ended.Sub(started)
	if length < shortestSession {
		return
	}
	if err := d.Store.AddPlaySession(gameID, started.UnixMilli(), ended.UnixMilli()); err != nil {
		d.Log.Log("warn", err.Error())
	}
	d.learnProgram(gameID, seen)
	d.Log.Log("info", fmt.Sprintf("stopped playing %q after %s", game.Name, spokenLength(length)))

	hash, err := d.currentContentHash(game)
	if err != nil || hash == startHash {
		return // the save did not change, or cannot be read: nothing to keep
	}
	comment := fmt.Sprintf("After playing (%s)", spokenLength(length))
	if err := d.markSessionEnd(game, hash, started, comment); err != nil {
		d.Log.Log("warn", fmt.Sprintf("could not keep %q as it was left: %v", game.Name, err))
		return
	}
	// Onward to the other devices, so it is there on the next one picked up.
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		_, _ = d.P2P.SyncGame(ctx, gameID)
	}()
}

// markSessionEnd keeps the save as a session left it. The watcher has usually
// snapshotted it already, moments before the game closed; that snapshot is
// then named for the session rather than a copy of it taken. Otherwise one is
// taken.
func (d *Daemon) markSessionEnd(game store.Game, hash string, started time.Time, comment string) error {
	if game.LastManifestHash == hash {
		snaps, err := d.Store.ListSnapshots(game.ID, game.ActiveBranch)
		if err == nil && len(snaps) > 0 {
			latest := snaps[0] // newest first
			if at, perr := time.Parse(time.RFC3339Nano, latest.Timestamp); perr == nil && !at.Before(started) && latest.IsSystemAuto {
				return d.Store.SetSnapshotComment(latest.ID, comment)
			}
		}
	}
	if _, err := d.Snapshots.Create(game.ID, comment, true); err != nil {
		return err
	}
	return d.Store.SetLastManifestHash(game.ID, hash)
}

// spokenLength says how long a session was: "45 min", "1 h 12 min", "3 h".
func spokenLength(dur time.Duration) string {
	dur = dur.Round(time.Minute)
	h, m := int(dur/time.Hour), int(dur%time.Hour/time.Minute)
	switch {
	case h == 0:
		if m < 1 {
			m = 1
		}
		return fmt.Sprintf("%d min", m)
	case m == 0:
		return fmt.Sprintf("%d h", h)
	default:
		return fmt.Sprintf("%d h %d min", h, m)
	}
}

// endOpenSessions records the sessions still open when the daemon stops, as
// ending now: the game may go on, but nothing will be watching it.
func (d *Daemon) endOpenSessions() {
	if d.sessions.tracker == nil {
		return
	}
	now := time.Now()
	for id, since := range d.sessions.tracker.Playing() {
		if now.Sub(since) >= shortestSession {
			_ = d.Store.AddPlaySession(id, since.UnixMilli(), now.UnixMilli())
		}
	}
}

// checkpointEvery is how often a game being played gets a snapshot of its
// save: one per autosave pushed the snapshots worth keeping out, and none
// until the session ends leaves nothing behind a crash.
const checkpointEvery = 30 * time.Minute

// holdSnapshotWhilePlaying tells the watcher not to snapshot a change to a
// game being played — in a session, or changing with no session seen
// (noteActivity) — unless checkpointEvery has passed since play began or
// since the last checkpoint. The end of the session, or of the stretch of
// changes, keeps the save as it was left.
func (d *Daemon) holdSnapshotWhilePlaying(gameID string) bool {
	// Every change made here counts, session seen or not (noteActivity).
	start, stretch := d.noteActivity(gameID)
	since := d.PlayingSince(gameID)
	if since.IsZero() {
		if !stretch {
			return false
		}
		since = start
	}
	d.sessions.mu.Lock()
	defer d.sessions.mu.Unlock()
	if d.sessions.checkpoint == nil {
		d.sessions.checkpoint = map[string]time.Time{}
	}
	now := time.Now()
	if !checkpointDue(since, d.sessions.checkpoint[gameID], now) {
		return true
	}
	d.sessions.checkpoint[gameID] = now
	return false
}

// checkpointDue says whether checkpointEvery has passed since play began, or
// since the last checkpoint when there was one.
func checkpointDue(since, last, now time.Time) bool {
	if last.Before(since) {
		last = since
	}
	return now.Sub(last) >= checkpointEvery
}

// Programs that come with a game without being it: installers, crash
// reporters, redistributables, anti-cheat services. A process known only by
// name is never matched by one of these — every game ships the same few.
var notTheGame = []string{
	"unins", "setup", "install", "crash", "report", "redist", "vcredist", "vc_redist", "dxsetup",
	"directx", "prereq", "launcher", "easyanticheat", "eac", "battleye", "beservice", "update",
	"patch", "config", "settings", "helper", "service", "cef", "webhelper", "dotnet", "physx",
}

// programsIn lists the programs in a game's install folder by file name,
// lower-case, for a process known only by name (sessions.Proc.Name). Read at
// most every installDirsFresh, three folders deep.
func (d *Daemon) programsIn(dir string) []string {
	d.sessions.mu.Lock()
	if c, ok := d.sessions.programs[dir]; ok && time.Since(c.at) < installDirsFresh {
		d.sessions.mu.Unlock()
		return c.names
	}
	d.sessions.mu.Unlock()

	var names []string
	root := filepath.Clean(dir)
	depth := strings.Count(root, string(filepath.Separator))
	_ = filepath.WalkDir(root, func(p string, e os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if e.IsDir() {
			if strings.Count(filepath.Clean(p), string(filepath.Separator))-depth >= 3 {
				return filepath.SkipDir
			}
			return nil
		}
		name := strings.ToLower(e.Name())
		if !strings.HasSuffix(name, ".exe") || len(names) >= 64 {
			return nil
		}
		for _, w := range notTheGame {
			if strings.Contains(name, w) {
				return nil
			}
		}
		names = append(names, name)
		return nil
	})

	d.sessions.mu.Lock()
	if d.sessions.programs == nil {
		d.sessions.programs = map[string]programList{}
	}
	d.sessions.programs[dir] = programList{at: time.Now(), names: names}
	d.sessions.mu.Unlock()
	return names
}

type programList struct {
	at    time.Time
	names []string
}

// quietAfter is how long a game's save must stay unchanged before a stretch
// of changes no session accounts for is over: longer than the gap between
// two autosaves of the games measured (Crimson Desert: up to six minutes).
const quietAfter = 10 * time.Minute

// activity is a stretch of changes to a game's save while no session of it
// was seen: a game whose program cannot be told apart, or is not installed
// where this device looks. It is handled as a session — not synced either
// way, a checkpoint at most every checkpointEvery — and when the save has
// stayed unchanged for quietAfter, it is kept and synced as a session's end
// would be.
type activity struct {
	start, last time.Time
	timer       *time.Timer
}

// noteActivity records a change to a game's save made here (not by a sync).
// A change within quietAfter of the one before begins a stretch, or goes on
// with one: it returns when the stretch began, and whether there is one. A
// change on its own — a game that saves once, as it is closed — is not one,
// and is kept and synced straight away, as every change was before.
func (d *Daemon) noteActivity(gameID string) (time.Time, bool) {
	now := time.Now()
	d.sessions.mu.Lock()
	defer d.sessions.mu.Unlock()
	if d.sessions.active == nil {
		d.sessions.active = map[string]*activity{}
	}
	if d.sessions.lastChange == nil {
		d.sessions.lastChange = map[string]time.Time{}
	}
	prev, seen := d.sessions.lastChange[gameID]
	d.sessions.lastChange[gameID] = now
	a := d.sessions.active[gameID]
	if a == nil {
		if !seen || now.Sub(prev) >= quietAfter || d.opts.SyncEveryChange {
			return now, false
		}
		a = &activity{start: prev}
		d.sessions.active[gameID] = a
		a.timer = time.AfterFunc(quietAfter, func() { d.activityQuiet(gameID) })
		if d.PlayingSince(gameID).IsZero() && !d.sessions.warned[gameID] {
			if d.sessions.warned == nil {
				d.sessions.warned = map[string]bool{}
			}
			d.sessions.warned[gameID] = true
			name := gameID
			if g, err := d.Store.GetGame(gameID); err == nil {
				name = g.Name
			}
			go d.Log.Log("info", fmt.Sprintf("%q is changing its save, but its program is not seen running here — "+
				"handled as being played: kept and synced once it has not changed for %s", name, spokenLength(quietAfter)))
		}
	} else {
		a.timer.Reset(quietAfter)
	}
	a.last = now
	return a.start, true
}

// changingNow reports a game in a stretch of changes (noteActivity).
func (d *Daemon) changingNow(gameID string) bool {
	d.sessions.mu.Lock()
	defer d.sessions.mu.Unlock()
	_, ok := d.sessions.active[gameID]
	return ok
}

// endActivity forgets a stretch of changes; a session's end takes over.
func (d *Daemon) endActivity(gameID string) {
	d.sessions.mu.Lock()
	if a := d.sessions.active[gameID]; a != nil {
		a.timer.Stop()
		delete(d.sessions.active, gameID)
	}
	d.sessions.mu.Unlock()
}

// activityQuiet ends a stretch of changes once the save has stayed unchanged
// for quietAfter: the save as it was left is kept and goes to the other
// devices, as at the end of a session.
func (d *Daemon) activityQuiet(gameID string) {
	d.sessions.mu.Lock()
	a := d.sessions.active[gameID]
	if a == nil {
		d.sessions.mu.Unlock()
		return
	}
	if wait := quietAfter - time.Since(a.last); wait > 0 {
		a.timer.Reset(wait)
		d.sessions.mu.Unlock()
		return
	}
	delete(d.sessions.active, gameID)
	delete(d.sessions.checkpoint, gameID)
	start, last := a.start, a.last
	d.sessions.mu.Unlock()

	if !d.PlayingSince(gameID).IsZero() {
		return // a session is open after all; its end keeps the save
	}
	game, err := d.Store.GetGame(gameID)
	if err != nil {
		return
	}
	if hash, err := d.currentContentHash(game); err == nil && hash != game.LastManifestHash {
		comment := "Auto backup"
		if last.Sub(start) >= shortestSession {
			comment = fmt.Sprintf("After playing (%s)", spokenLength(last.Sub(start)))
		}
		if err := d.markSessionEnd(game, hash, start, comment); err != nil {
			d.Log.Log("warn", fmt.Sprintf("could not keep %q as it was left: %v", game.Name, err))
			return
		}
	}
	d.P2P.Sync.NoteLocalChange(gameID)
	d.Log.Log("info", fmt.Sprintf("%q has not changed its save for %s; sending it to your other devices",
		game.Name, spokenLength(quietAfter)))
	if d.OnGameChanged != nil {
		d.OnGameChanged(gameID)
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		_, _ = d.P2P.SyncGame(ctx, gameID)
	}()
}
