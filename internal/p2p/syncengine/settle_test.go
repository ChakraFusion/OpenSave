package syncengine

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/opensave/opensave/internal/delta"
)

// settledWithin reports whether the game's save could be read within d —
// whether nothing was writing it.
func settledWithin(e *Engine, gameID string, d time.Duration) bool {
	ctx, cancel := context.WithTimeout(context.Background(), d)
	defer cancel()
	done, err := e.Reading(ctx, gameID)
	if err != nil {
		return false
	}
	done()
	return true
}

func TestSettle_AReaderWaitsForEveryHoldToBeLetGo(t *testing.T) {
	env := setupEngine(t)
	e := env.engine

	if !settledWithin(e, "game1", time.Second) {
		t.Fatal("a save nothing is writing must be readable at once")
	}

	first, second := e.Writing("game1"), e.Writing("game1")
	if settledWithin(e, "game1", 30*time.Millisecond) {
		t.Fatal("a save being written must not be readable")
	}
	if !settledWithin(e, "game2", time.Second) {
		t.Error("a write to one game must not hold up reading another")
	}

	// A reader already waiting is let in the moment the last hold goes.
	got := make(chan error, 1)
	go func() {
		done, err := e.Reading(context.Background(), "game1")
		if err == nil {
			done()
		}
		got <- err
	}()

	first()
	first() // letting go twice must not count as the other hold going too
	if settledWithin(e, "game1", 30*time.Millisecond) {
		t.Fatal("still held by the second writer, but read as settled")
	}
	select {
	case err := <-got:
		t.Fatalf("the waiting reader got in while a hold remained: %v", err)
	default:
	}

	second()
	select {
	case err := <-got:
		if err != nil {
			t.Fatalf("waiting reader: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the waiting reader was never let in")
	}
	if !errors.Is(func() error {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		done := e.Writing("game1")
		defer done()
		_, err := e.Reading(ctx, "game1")
		return err
	}(), ErrSettling) {
		t.Error("a wait that runs out must say the save is still settling")
	}
}

// Waiting for writes to stop and then reading is not enough: a write can
// begin while the folder is being walked. The first version of the gate did
// exactly that, and a pull starting mid-walk was caught half-way regardless.
// A writer waits for a read in progress, and a new read queues behind it.
func TestSettle_AWriteWaitsForAReadInProgress(t *testing.T) {
	env := setupEngine(t)
	e := env.engine

	readDone, err := e.Reading(context.Background(), "game1")
	if err != nil {
		t.Fatal(err)
	}
	writing := make(chan func(), 1)
	go func() { writing <- e.Writing("game1") }()
	select {
	case <-writing:
		t.Fatal("a write began while the save was being read")
	case <-time.After(100 * time.Millisecond):
	}
	// A reader arriving now waits behind the writer rather than starving it.
	if settledWithin(e, "game1", 50*time.Millisecond) {
		t.Fatal("a new read was let in ahead of a waiting write")
	}

	readDone()
	var writeDone func()
	select {
	case writeDone = <-writing:
	case <-time.After(5 * time.Second):
		t.Fatal("the write never began after the read finished")
	}
	if settledWithin(e, "game1", 50*time.Millisecond) {
		t.Fatal("read while being written")
	}
	writeDone()
	if !settledWithin(e, "game1", time.Second) {
		t.Fatal("unreadable after the write finished")
	}
}

// The gate is only worth anything if a sync holds it for as long as it
// writes: through a pull, and through the deletions before it.
func TestSettle_ASyncHoldsItsSaveWhileItWrites(t *testing.T) {
	env := setupEngine(t)
	write(t, env.localDir, "shared.dat", "both")
	write(t, env.remoteDir, "shared.dat", "both")
	write(t, env.localDir, "gone.dat", "shared once")
	hourAgo := time.Now().Add(-time.Hour)
	for _, dir := range []string{env.localDir, env.remoteDir} {
		_ = os.Chtimes(filepath.Join(dir, "shared.dat"), hourAgo, hourAgo)
	}
	_ = os.Chtimes(filepath.Join(env.localDir, "gone.dat"), hourAgo, hourAgo)
	// The peer deleted gone.dat, which both had; and has a new file to pull.
	if err := env.store.SetSyncState("game1", env.peer.ID, []string{"shared.dat", "gone.dat"}, nil); err != nil {
		t.Fatal(err)
	}
	// Last synced after those files were written, so only the peer's new file
	// and the two deletions read as changes.
	lastSync := time.Now().Add(-30 * time.Minute).UTC().Format("2006-01-02T15:04:05.000Z")
	if err := env.store.UpdatePeerLastSynced(env.peer.ID, lastSync); err != nil {
		t.Fatal(err)
	}
	write(t, env.remoteDir, "new.dat", "from the peer")

	var duringPull, duringDelete []bool
	env.transport.onFetchBlocks = func() {
		duringPull = append(duringPull, settledWithin(env.engine, "game1", 20*time.Millisecond))
	}
	// And a local deletion to propagate: this side removes shared.dat.
	if err := os.Remove(filepath.Join(env.localDir, "shared.dat")); err != nil {
		t.Fatal(err)
	}
	env.transport.onDeleteRemote = func() {
		duringDelete = append(duringDelete, settledWithin(env.engine, "game1", 20*time.Millisecond))
	}

	if _, err := env.engine.SyncWithPeer(context.Background(), "game1", env.peer); err != nil {
		t.Fatal(err)
	}
	if len(duringPull) == 0 || len(duringDelete) == 0 {
		t.Fatalf("the sync did not pull and propagate as set up (pull %v, delete %v)", duringPull, duringDelete)
	}
	for _, settled := range append(duringPull, duringDelete...) {
		if settled {
			t.Fatalf("the save read as settled while the sync was writing it (pull %v, delete %v)", duringPull, duringDelete)
		}
	}
	if !settledWithin(env.engine, "game1", time.Second) {
		t.Error("the sync finished but its save still reads as being written")
	}
}

// A peer that answers "still writing" has told us nothing about its save, so
// nothing is judged and nothing is stamped — and the sync is asked again.
func TestSettle_ABusyPeerIsAskedAgainNotJudged(t *testing.T) {
	env := setupEngine(t)
	write(t, env.remoteDir, "save.dat", "the peer's")
	env.transport.busyFor = 2

	res, err := env.engine.SyncWithPeer(context.Background(), "game1", env.peer)
	if err != nil {
		t.Fatalf("a busy peer is not an error: %v", err)
	}
	if res.Status != "peer_busy" {
		t.Fatalf("status = %q, want peer_busy", res.Status)
	}
	if _, conflicted := env.engine.ActiveConflicts()["game1"]; conflicted {
		t.Fatal("a busy answer raised a conflict")
	}

	// Through SyncGame the follow-up runs by itself until the peer answers.
	if _, err := env.engine.SyncGame(context.Background(), "game1", []Peer{env.peer}); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for env.engine.SyncBusy("game1") && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	got, _ := os.ReadFile(filepath.Join(env.localDir, "save.dat"))
	if string(got) != "the peer's" {
		t.Fatalf("after the peer finished, the save was never taken: %q", got)
	}
}

func TestSettle_APeerThatNeverFinishesIsNotAskedForever(t *testing.T) {
	env := setupEngine(t)
	env.transport.busyFor = 1000

	if _, err := env.engine.SyncGame(context.Background(), "game1", []Peer{env.peer}); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for env.engine.SyncBusy("game1") && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if env.engine.SyncBusy("game1") {
		t.Fatal("still re-asking a peer that never finishes")
	}
	env.transport.mu.Lock()
	calls := env.transport.manifestCalls
	env.transport.mu.Unlock()
	if want := maxBusyFollowUps + 1; calls != want {
		t.Errorf("asked %d times, want the first sync plus %d follow-ups = %d", calls, maxBusyFollowUps, want)
	}

	// And the count starts again: a later sync is not refused its follow-ups
	// because an earlier one used them up.
	env.transport.mu.Lock()
	env.transport.busyFor, env.transport.manifestCalls = 1, 0
	env.transport.mu.Unlock()
	if _, err := env.engine.SyncGame(context.Background(), "game1", []Peer{env.peer}); err != nil {
		t.Fatal(err)
	}
	for env.engine.SyncBusy("game1") && time.Now().Before(deadline.Add(10*time.Second)) {
		time.Sleep(20 * time.Millisecond)
	}
	env.transport.mu.Lock()
	calls = env.transport.manifestCalls
	env.transport.mu.Unlock()
	if calls != 2 {
		t.Errorf("a fresh busy answer got %d requests, want 2 (the sync and its follow-up)", calls)
	}
}

// What a device looks like part-way through its first pull, if it is asked
// by one that does not know to wait: a part of the other side's files, every
// one identical. Behind, not diverged.
func TestSettle_AFirstSyncAgainstAPartOfTheSameSaveIsNotAConflict(t *testing.T) {
	env := setupEngine(t)
	for _, f := range []string{"a.sav", "b.sav", "c.sav"} {
		write(t, env.localDir, f, "content of "+f)
	}
	write(t, env.remoteDir, "a.sav", "content of a.sav")

	res, err := env.engine.SyncWithPeer(context.Background(), "game1", env.peer)
	if err != nil {
		t.Fatal(err)
	}
	if res.Status == "conflict" {
		t.Fatal("a peer holding part of this save, unchanged, was taken for a different save")
	}
	if res.Status != "triggered_peer_pull" {
		t.Errorf("status = %q, want the rest pushed (triggered_peer_pull)", res.Status)
	}
}

// Two saves that really grew apart are still a conflict on a first sync.
func TestSettle_AFirstSyncBetweenDifferentSavesStillConflicts(t *testing.T) {
	for name, remote := range map[string]map[string]string{
		"a shared file differs":  {"a.sav": "the peer's own a"},
		"each has its own files": {"a.sav": "content of a.sav", "z.sav": "only the peer has this"},
	} {
		t.Run(name, func(t *testing.T) {
			env := setupEngine(t)
			for _, f := range []string{"a.sav", "b.sav"} {
				write(t, env.localDir, f, "content of "+f)
			}
			for f, c := range remote {
				write(t, env.remoteDir, f, c)
			}
			res, err := env.engine.SyncWithPeer(context.Background(), "game1", env.peer)
			if err != nil {
				t.Fatal(err)
			}
			if res.Status != "conflict" {
				t.Errorf("status = %q, want conflict", res.Status)
			}
		})
	}
}

// The lineage is what stops "behind" from hiding a real change: a file the
// peer deleted was shared, so a peer that deleted one while this side edited
// another has moved, and that is still a conflict.
func TestSettle_APeerThatDeletedASharedFileIsNotMerelyBehind(t *testing.T) {
	env := setupEngine(t)
	for _, dir := range []string{env.localDir, env.remoteDir} {
		write(t, dir, "keep.sav", "shared")
		write(t, dir, "drop.sav", "shared")
	}
	base, err := delta.BuildManifest(env.localDir)
	if err != nil {
		t.Fatal(err)
	}
	if err := env.store.SetSyncState("game1", env.peer.ID, []string{"keep.sav", "drop.sav"}, nil); err != nil {
		t.Fatal(err)
	}
	_ = env.store.SetAgreedHash("game1", env.peer.ID, base.ManifestHash())

	// The peer deletes drop.sav; this side adds a file of its own.
	if err := os.Remove(filepath.Join(env.remoteDir, "drop.sav")); err != nil {
		t.Fatal(err)
	}
	write(t, env.localDir, "new.sav", "made here")

	if !OnlyBehind(mustManifest(t, env.localDir), mustManifest(t, env.remoteDir), map[string]struct{}{}) {
		t.Fatal("control: without the lineage the peer would look merely behind")
	}
	res, err := env.engine.SyncWithPeer(context.Background(), "game1", env.peer)
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != "conflict" {
		t.Errorf("status = %q: a deletion on one side and an edit on the other must still conflict", res.Status)
	}
}

func mustManifest(t *testing.T, dir string) delta.Manifest {
	t.Helper()
	m, err := delta.BuildManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func TestOnlyBehind(t *testing.T) {
	m := func(files map[string]string) delta.Manifest {
		out := delta.Manifest{Files: map[string]delta.FileEntry{}}
		for p, h := range files {
			out.Files[p] = delta.FileEntry{Hash: h}
		}
		return out
	}
	lineage := func(paths ...string) map[string]struct{} {
		out := map[string]struct{}{}
		for _, p := range paths {
			out[p] = struct{}{}
		}
		return out
	}
	full := m(map[string]string{"a": "1", "b": "2", "c": "3"})
	for _, c := range []struct {
		name          string
		local, remote delta.Manifest
		lineage       map[string]struct{}
		want          bool
	}{
		{"remote has a part, never shared the rest", full, m(map[string]string{"a": "1"}), lineage("a"), true},
		{"local has a part", m(map[string]string{"b": "2"}), full, lineage(), true},
		{"remote has nothing yet", full, m(map[string]string{}), lineage(), true},
		{"remote is missing a file both once held", full, m(map[string]string{"a": "1"}), lineage("a", "b"), false},
		{"remote emptied a shared save", full, m(map[string]string{}), lineage("a", "b", "c"), false},
		{"a shared path differs", full, m(map[string]string{"a": "9"}), lineage(), false},
		{"each side has its own file", m(map[string]string{"a": "1", "x": "7"}), m(map[string]string{"a": "1", "y": "8"}), lineage(), false},
	} {
		if got := OnlyBehind(c.local, c.remote, c.lineage); got != c.want {
			t.Errorf("%s: OnlyBehind = %v, want %v", c.name, got, c.want)
		}
	}
}
