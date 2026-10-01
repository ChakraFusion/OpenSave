package syncengine

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/opensave/opensave/internal/delta"
	"github.com/opensave/opensave/internal/snapshot"
	"github.com/opensave/opensave/internal/store"
)

func TestCompareVersions(t *testing.T) {
	cases := []struct {
		a, b VersionVector
		want versionOrder
	}{
		{VersionVector{}, VersionVector{}, versionEqual},
		{VersionVector{}, VersionVector{"x": 1}, versionOlder},
		{VersionVector{"x": 2}, VersionVector{"x": 1}, versionNewer},
		{VersionVector{"x": 1, "y": 1}, VersionVector{"x": 1, "y": 1}, versionEqual},
		{VersionVector{"x": 2}, VersionVector{"x": 1, "y": 1}, versionConcurrent},
		{VersionVector{"x": 1}, VersionVector{"x": 1, "y": 1}, versionOlder},
	}
	for _, c := range cases {
		if got := compareVersions(c.a, c.b); got != c.want {
			t.Errorf("compare(%v, %v) = %d, want %d", c.a, c.b, got, c.want)
		}
	}
}

// --- a network of real engines -------------------------------------------

// meshNode is one device: its own store, save folder and engine.
type meshNode struct {
	name string
	dir  string
	st   *store.Store
	eng  *Engine
	// blockBudget, when >= 0, is how many more files this device serves
	// before every further block request fails: a pull that stops part-way.
	blockBudget int
}

func (n *meshNode) peer() Peer {
	return Peer{ID: "node_" + n.name, Name: n.name, Address: "127.0.0.1", Port: 1}
}

type mesh struct {
	mu      sync.Mutex
	nodes   map[string]*meshNode // by peer id
	offline map[string]bool
	// deletesSent counts DeleteRemote calls: the version path never makes one.
	deletesSent int
}

type meshTransport struct {
	m    *mesh
	self *meshNode
}

var errUnreachable = errors.New("peer unreachable")

func (t *meshTransport) target(peer Peer) (*meshNode, error) {
	t.m.mu.Lock()
	defer t.m.mu.Unlock()
	n := t.m.nodes[peer.ID]
	if n == nil || t.m.offline[peer.ID] {
		return nil, errUnreachable
	}
	return n, nil
}

// FetchManifest answers the way the real route does: the save, and the
// device's version of it.
func (t *meshTransport) FetchManifest(ctx context.Context, peer Peer, gameID string, q ManifestQuery) (ManifestResponse, error) {
	n, err := t.target(peer)
	if err != nil {
		return ManifestResponse{}, err
	}
	game, err := n.st.GetGame(gameID)
	if err != nil {
		return ManifestResponse{}, errors.New("Game not found.")
	}
	m, err := delta.BuildManifest(n.dir)
	if err != nil {
		return ManifestResponse{}, err
	}
	return ManifestResponse{
		Manifest:          m,
		ActiveBranch:      game.ActiveBranch,
		DeletionConfirmed: n.eng.DeletionConfirmed(gameID),
		Version:           n.eng.LocalVersion(game, m),
	}, nil
}

func (t *meshTransport) FetchBlocks(ctx context.Context, peer Peer, ref FileRef, blockIndices []int, blockSize int) ([]BlockData, error) {
	n, err := t.target(peer)
	if err != nil {
		return nil, err
	}
	t.m.mu.Lock()
	if n.blockBudget == 0 {
		t.m.mu.Unlock()
		return nil, errors.New("connection reset")
	}
	if n.blockBudget > 0 {
		n.blockBudget--
	}
	t.m.mu.Unlock()
	full := filepath.Join(n.dir, filepath.FromSlash(ref.RelPath))
	entry, err := delta.HashFile(full)
	if err != nil {
		return nil, err
	}
	raw, err := os.ReadFile(full)
	if err != nil {
		return nil, err
	}
	var out []BlockData
	for _, idx := range blockIndices {
		if idx >= len(entry.Blocks) {
			continue
		}
		start := idx * entry.BlockSize
		end := start + entry.Blocks[idx].Length
		if end > len(raw) {
			end = len(raw)
		}
		out = append(out, BlockData{Index: idx, Data: raw[start:end], Length: end - start})
	}
	return out, nil
}

func (t *meshTransport) DeleteRemote(ctx context.Context, peer Peer, ref FileRef) error {
	n, err := t.target(peer)
	if err != nil {
		return err
	}
	t.m.mu.Lock()
	t.m.deletesSent++
	t.m.mu.Unlock()
	return os.RemoveAll(filepath.Join(n.dir, filepath.FromSlash(ref.RelPath)))
}

func (t *meshTransport) TriggerPeerPull(peer Peer, gameID string)                                 {}
func (t *meshTransport) ReportSyncEvent(peer Peer, gameID, eventType string, data map[string]any) {}

func newMesh(t *testing.T, names ...string) (*mesh, map[string]*meshNode) {
	t.Helper()
	m := &mesh{nodes: map[string]*meshNode{}, offline: map[string]bool{}}
	byName := map[string]*meshNode{}
	for _, name := range names {
		root := t.TempDir()
		dir := filepath.Join(root, "saves")
		if err := os.MkdirAll(dir, 0o777); err != nil {
			t.Fatal(err)
		}
		st, err := store.Open(filepath.Join(root, "opensave.db"))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { st.Close() })
		if err := st.EnsureDefaultSettings(root, filepath.Join(root, "backups")); err != nil {
			t.Fatal(err)
		}
		if err := st.CreateGame(store.Game{ID: "game1", Name: "Game One", SavePath: dir, AutoSync: true, MaxSnapshots: 50}); err != nil {
			t.Fatal(err)
		}
		n := &meshNode{name: name, dir: dir, st: st, blockBudget: -1}
		n.eng = New(st, snapshot.New(st), &meshTransport{m: m, self: n})
		m.nodes[n.peer().ID] = n
		byName[name] = n
	}
	for _, a := range byName {
		for _, b := range byName {
			if a != b {
				p := b.peer()
				if err := a.st.UpsertPeer(store.Peer{ID: p.ID, Name: p.Name, Address: p.Address, Port: p.Port, Status: "online"}); err != nil {
					t.Fatal(err)
				}
			}
		}
	}
	return m, byName
}

func (m *mesh) setOffline(n *meshNode, off bool) {
	m.mu.Lock()
	m.offline[n.peer().ID] = off
	m.mu.Unlock()
}

func (m *mesh) deletes() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.deletesSent
}

// syncPair runs one sync of a with b, as a's engine would.
func syncPair(t *testing.T, a, b *meshNode) (Result, error) {
	t.Helper()
	return a.eng.SyncWithPeer(context.Background(), "game1", b.peer())
}

// change edits the save the way a game does, then tells the engine — the
// watcher's part.
func (n *meshNode) change(t *testing.T, edit func(dir string)) {
	t.Helper()
	edit(n.dir)
	n.eng.NoteLocalChange("game1")
}

func (n *meshNode) files(t *testing.T) map[string]string {
	t.Helper()
	m, err := delta.BuildManifest(n.dir)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	for p, f := range m.Files {
		out[p] = f.Hash
	}
	return out
}

func (n *meshNode) version(t *testing.T) VersionInfo {
	t.Helper()
	game, err := n.st.GetGame("game1")
	if err != nil {
		t.Fatal(err)
	}
	m, err := delta.BuildManifest(n.dir)
	if err != nil {
		t.Fatal(err)
	}
	return *n.eng.LocalVersion(game, m)
}

func sameSave(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for p, h := range a {
		if b[p] != h {
			return false
		}
	}
	return true
}

func describe(files map[string]string) string {
	paths := make([]string, 0, len(files))
	for p := range files {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	if len(paths) > 6 {
		return fmt.Sprintf("%d files (%v …)", len(paths), paths[:6])
	}
	return fmt.Sprintf("%v", paths)
}

func writeMany(t *testing.T, dir, prefix string, n int) {
	t.Helper()
	for i := 0; i < n; i++ {
		write(t, dir, fmt.Sprintf("%s/chunk_%03d.bin", prefix, i), fmt.Sprintf("%s-%d", prefix, i))
	}
}

// settleAll syncs every reachable node with every other, a few rounds, the
// way devices that are all online settle.
func settleAll(t *testing.T, nodes ...*meshNode) {
	t.Helper()
	for round := 0; round < 4; round++ {
		for _, a := range nodes {
			for _, b := range nodes {
				if a != b {
					_, _ = syncPair(t, a, b)
				}
			}
		}
	}
}

// --- scenarios ---------------------------------------------------------------

// The case this exists for. Three devices share a save; the server changes it
// a lot. One laptop starts taking the new version and is cut off part-way; the
// server then goes offline. The two laptops must not sync with each other in
// the meantime — not the half-finished copy, and not its missing files read as
// deletions — and when the server is back, both end up with its save exactly.
func TestVersions_IncompleteDevicesWaitForTheNewestOne(t *testing.T) {
	m, n := newMesh(t, "server", "laptop", "desk")
	server, laptop, desk := n["server"], n["laptop"], n["desk"]

	writeMany(t, server.dir, "map", 40)
	write(t, server.dir, "players.db", "v1")
	settleAll(t, server, laptop, desk)
	original := server.files(t)
	for _, d := range []*meshNode{laptop, desk} {
		if !sameSave(d.files(t), original) {
			t.Fatalf("%s did not take the initial save: %s", d.name, describe(d.files(t)))
		}
	}

	// The desk is switched off for a while; the server plays and the laptop
	// takes that. The desk is now two versions behind, the laptop one —
	// which makes the laptop newer than the desk, the comparison a device
	// part-way through a pull must never win.
	m.setOffline(desk, true)
	server.change(t, func(dir string) { write(t, dir, "players.db", "v1.5") })
	settleAll(t, server, laptop)
	m.setOffline(desk, false)

	// The server plays on: new chunks, some removed, one changed.
	server.change(t, func(dir string) {
		writeMany(t, dir, "map2", 30)
		for i := 0; i < 5; i++ {
			_ = os.Remove(filepath.Join(dir, "map", fmt.Sprintf("chunk_%03d.bin", i)))
		}
		write(t, dir, "players.db", "v2")
	})
	newest := server.files(t)

	// The laptop starts taking it and is cut off after 10 files.
	server.blockBudget = 10
	if _, err := syncPair(t, laptop, server); err == nil {
		t.Fatal("a pull cut off part-way reported success")
	}
	server.blockBudget = -1
	if !laptop.version(t).Outdated() {
		t.Fatalf("the laptop does not know it is behind: %+v", laptop.version(t))
	}
	m.setOffline(server, true)

	// Server gone. The laptop and desk meet, repeatedly, both ways.
	deskBefore := desk.files(t)
	laptopBefore := laptop.files(t)
	for i := 0; i < 3; i++ {
		for _, pair := range [][2]*meshNode{{desk, laptop}, {laptop, desk}} {
			res, err := syncPair(t, pair[0], pair[1])
			if err != nil {
				t.Fatalf("%s↔%s: %v", pair[0].name, pair[1].name, err)
			}
			if res.Status == "conflict" {
				t.Fatalf("%s↔%s raised a conflict between two copies of the same history", pair[0].name, pair[1].name)
			}
		}
	}
	if !sameSave(desk.files(t), deskBefore) {
		t.Errorf("the desk changed while the newest device was away: was %s, now %s",
			describe(deskBefore), describe(desk.files(t)))
	}
	if !sameSave(laptop.files(t), laptopBefore) {
		t.Errorf("the laptop's partial copy changed while the newest device was away: was %s, now %s",
			describe(laptopBefore), describe(laptop.files(t)))
	}
	if !desk.version(t).Outdated() {
		t.Error("the desk never learned from the laptop that a newer version exists")
	}
	if m.deletes() != 0 {
		t.Errorf("%d deletion(s) were sent to another device", m.deletes())
	}

	// The server returns; everyone ends with exactly its save.
	m.setOffline(server, false)
	settleAll(t, server, laptop, desk)
	for _, d := range []*meshNode{server, laptop, desk} {
		if !sameSave(d.files(t), newest) {
			t.Errorf("%s does not hold the newest save: %s", d.name, describe(d.files(t)))
		}
		if v := d.version(t); v.Outdated() {
			t.Errorf("%s still thinks it is behind: %+v", d.name, v)
		}
	}
	if m.deletes() != 0 {
		t.Errorf("%d deletion(s) were sent to another device", m.deletes())
	}
}

// An empty folder — a new device, or one whose copy never arrived — is never
// one side of a conflict, and never makes the device with the save lose files.
func TestVersions_AnEmptyDeviceTakesTheSaveWithoutAsking(t *testing.T) {
	m, n := newMesh(t, "full", "empty")
	full, empty := n["full"], n["empty"]
	writeMany(t, full.dir, "map", 100)
	want := full.files(t)

	// The device with the save syncs first: nothing of its own may go.
	res, err := syncPair(t, full, empty)
	if err != nil {
		t.Fatal(err)
	}
	if res.Status == "conflict" {
		t.Fatal("a save against an empty folder raised a conflict")
	}
	if !sameSave(full.files(t), want) {
		t.Fatalf("the device with the save lost files to an empty one: %s", describe(full.files(t)))
	}
	if res, err := syncPair(t, empty, full); err != nil || res.Status == "conflict" {
		t.Fatalf("the empty device's sync: %+v, %v", res, err)
	}
	if !sameSave(empty.files(t), want) {
		t.Errorf("the empty device did not take the save: %s", describe(empty.files(t)))
	}
	if m.deletes() != 0 {
		t.Errorf("%d deletion(s) were sent", m.deletes())
	}
}

// A save that existed before versions did, identical on two devices, is one
// version at once: no conflict, nothing moved.
func TestVersions_IdenticalSavesFromBeforeVersionsAgree(t *testing.T) {
	_, n := newMesh(t, "a", "b")
	a, b := n["a"], n["b"]
	writeMany(t, a.dir, "s", 5)
	writeMany(t, b.dir, "s", 5)
	res, err := syncPair(t, a, b)
	if err != nil || res.Status != "in_sync" {
		t.Fatalf("identical saves: %+v, %v", res, err)
	}
	if compareVersions(a.version(t).Vector, b.version(t).Vector) != versionEqual {
		t.Errorf("identical saves got different versions: %v vs %v", a.version(t).Vector, b.version(t).Vector)
	}
}

// A deletion made in the game is part of the new version, and reaches the
// other device as that — whole — however many files it is.
func TestVersions_DeletionsTravelWithTheVersion(t *testing.T) {
	m, n := newMesh(t, "a", "b")
	a, b := n["a"], n["b"]
	writeMany(t, a.dir, "map", 20)
	settleAll(t, a, b)
	a.change(t, func(dir string) {
		_ = os.RemoveAll(filepath.Join(dir, "map"))
		write(t, dir, "fresh.sav", "new world")
	})
	settleAll(t, a, b)
	if !sameSave(b.files(t), a.files(t)) {
		t.Errorf("b = %s, want %s", describe(b.files(t)), describe(a.files(t)))
	}
	if m.deletes() != 0 {
		t.Errorf("the newer device sent %d deletion(s) itself; the older one takes them", m.deletes())
	}
}

// Both devices changed the save while apart: that, and only that, is asked
// about. Resolution is mutual — keeping A's on A asks B in turn rather than
// overwriting it, and A is not asked again. Once B agrees, the answer spreads,
// and a third device that had changed nothing takes it without being asked.
func TestVersions_IndependentChangesAskEachSideOnceAndTheAnswerSpreads(t *testing.T) {
	_, n := newMesh(t, "a", "b", "c")
	a, b, c := n["a"], n["b"], n["c"]
	write(t, a.dir, "slot.sav", "start")
	settleAll(t, a, b, c)

	a.change(t, func(dir string) { write(t, dir, "slot.sav", "a-played") })
	b.change(t, func(dir string) { write(t, dir, "slot.sav", "b-played") })

	res, err := syncPair(t, a, b)
	if err != nil || res.Status != "conflict" {
		t.Fatalf("independent changes: %+v, %v — want a conflict", res, err)
	}
	if _, err := a.eng.ResolveConflict(context.Background(), "game1", b.peer().ID, "keep-local"); err != nil {
		t.Fatal(err)
	}
	bBefore := b.files(t)
	settleAll(t, a, b, c)
	if len(a.eng.ActiveConflicts()) != 0 {
		t.Error("A was asked again after answering")
	}
	if len(b.eng.ActiveConflicts()) == 0 {
		t.Fatal("B was never asked: A's answer must not decide B's save on its own")
	}
	if !sameSave(b.files(t), bBefore) {
		t.Error("B's save changed before anyone there answered")
	}
	if _, err := b.eng.ResolveConflict(context.Background(), "game1", a.peer().ID, "keep-remote"); err != nil {
		t.Fatal(err)
	}
	settleAll(t, a, b, c)
	for _, d := range []*meshNode{b, c} {
		if got := d.files(t)["slot.sav"]; got != a.files(t)["slot.sav"] {
			t.Errorf("%s did not take the kept version", d.name)
		}
		if len(d.eng.ActiveConflicts()) != 0 {
			t.Errorf("%s still has a conflict after both answered", d.name)
		}
	}
}

// Both sides answer "keep mine" (or "keep both"): the later answer stands and
// both end on one save, instead of asking each other forever.
func TestVersions_TwoAnswersSettleOnTheLater(t *testing.T) {
	_, n := newMesh(t, "a", "b")
	a, b := n["a"], n["b"]
	write(t, a.dir, "slot.sav", "start")
	settleAll(t, a, b)
	a.change(t, func(dir string) { write(t, dir, "slot.sav", "a-played") })
	b.change(t, func(dir string) { write(t, dir, "slot.sav", "b-played") })
	if res, _ := syncPair(t, a, b); res.Status != "conflict" {
		t.Fatalf("A: %+v, want a conflict", res)
	}
	if res, _ := syncPair(t, b, a); res.Status != "conflict" {
		t.Fatalf("B: %+v, want a conflict", res)
	}
	if _, err := a.eng.ResolveConflict(context.Background(), "game1", b.peer().ID, "keep-local"); err != nil {
		t.Fatal(err)
	}
	time.Sleep(5 * time.Millisecond)
	if _, err := b.eng.ResolveConflict(context.Background(), "game1", a.peer().ID, "keep-local"); err != nil {
		t.Fatal(err)
	}
	settleAll(t, a, b)
	if got := a.files(t)["slot.sav"]; got != b.files(t)["slot.sav"] {
		t.Fatal("the two never settled on one save")
	}
	if len(a.eng.ActiveConflicts())+len(b.eng.ActiveConflicts()) != 0 {
		t.Error("a conflict is still open after both answered")
	}
	if got, _ := os.ReadFile(filepath.Join(a.dir, "slot.sav")); string(got) != "b-played" {
		t.Errorf("A holds %q; B answered last, so its save should stand", got)
	}
}

// A change the watcher never reported — made while the app was closed — still
// becomes a version once the two devices meet, instead of leaving them on one
// version with different files, each waiting for the other.
func TestVersions_AnUnnoticedChangeIsFoundWhenTheDevicesMeet(t *testing.T) {
	_, n := newMesh(t, "a", "b")
	a, b := n["a"], n["b"]
	write(t, a.dir, "slot.sav", "start")
	settleAll(t, a, b)
	write(t, a.dir, "slot.sav", "played offline") // no NoteLocalChange
	settleAll(t, b, a)
	if got := b.files(t)["slot.sav"]; got != a.files(t)["slot.sav"] {
		t.Errorf("the unnoticed change never reached b")
	}
}

// Files a sync writes are not a change made here: the pull-in-progress record
// keeps them from becoming a version, even across a restart.
func TestVersions_PulledFilesAreNotANewVersion(t *testing.T) {
	_, n := newMesh(t, "a", "b")
	a, b := n["a"], n["b"]
	writeMany(t, a.dir, "map", 10)
	settleAll(t, a, b)
	before := b.version(t).Vector

	a.change(t, func(dir string) { writeMany(t, dir, "map3", 10) })
	a.blockBudget = 3
	_, _ = syncPair(t, b, a)
	a.blockBudget = -1

	// What a restart leaves: the record says a pull was running, and the
	// watcher reports the half-written folder as a change.
	rec, _ := b.st.GetGameVersion("game1")
	rec.Pulling = true
	_ = b.st.SaveGameVersion(rec)
	b.eng.NoteLocalChange("game1")
	if got := b.version(t).Vector; compareVersions(got, before) != versionEqual {
		t.Fatalf("a half-finished pull became a version of its own: %v (was %v)", got, before)
	}
	settleAll(t, a, b)
	if !sameSave(b.files(t), a.files(t)) {
		t.Errorf("b did not finish taking a's version: %s", describe(b.files(t)))
	}
}
