package watcher

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func watching(eng *Engine, gameID, dir string) bool {
	eng.mu.Lock()
	gw := eng.games[gameID]
	eng.mu.Unlock()
	if gw == nil {
		return false
	}
	for _, w := range gw.fsw.WatchList() {
		if watchKey(w) == watchKey(dir) {
			return true
		}
	}
	return false
}

// A subfolder whose own Create was missed is put under watch on the next
// change in its parent (GitHub #91). Under load, MkdirAll of profiles\slot1
// left slot1 unwatched: the Create for profiles came and profiles was
// registered, but none came for slot1, and a save written into slot1
// afterwards was never seen.
//
// The missed Create is made here by taking slot1's watch away. What still
// arrives is what arrived then: a Write on profiles, when an entry inside it
// changes.
func TestSubfolder_AMissedOneIsWatchedOnTheNextChangeInItsParent(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("the Write on a folder whose entries changed is how Windows reports it, where #91 was seen")
	}
	saveDir := t.TempDir()
	slot1 := filepath.Join(saveDir, "profiles", "slot1")
	if err := os.MkdirAll(slot1, 0o777); err != nil {
		t.Fatal(err)
	}
	col := newCollector()
	eng := New(col.callbacks())
	defer eng.Stop()
	if err := eng.Watch("game1", saveDir); err != nil {
		t.Fatal(err)
	}
	if !watching(eng, "game1", slot1) {
		t.Fatal("setup: slot1 not watched to begin with")
	}
	// Past the first pass: a watch started while its folders changed
	// re-registers the whole tree there, which would put slot1 back for a
	// reason that has nothing to do with this.
	time.Sleep(2*debounceDelay + time.Second)
	eng.mu.Lock()
	gw := eng.games["game1"]
	eng.mu.Unlock()
	if err := gw.fsw.Remove(slot1); err != nil {
		t.Fatal(err)
	}

	// Something else changes inside profiles: Windows reports a Write on
	// profiles itself, which is what the check runs on.
	if err := os.Mkdir(filepath.Join(saveDir, "profiles", "slot2"), 0o777); err != nil {
		t.Fatal(err)
	}
	if !waitFor(t, 10*time.Second, func() bool { return watching(eng, "game1", slot1) }) {
		t.Fatal("slot1 was not put under watch again after a change in profiles")
	}

	// And a save written into slot1 is seen again.
	before := col.snapshotCount()
	time.Sleep(debounceDelay + time.Second)
	if err := os.WriteFile(filepath.Join(slot1, "deep.sav"), []byte("nested"), 0o666); err != nil {
		t.Fatal(err)
	}
	if !waitFor(t, 10*time.Second, func() bool { return col.snapshotCount() > before }) {
		t.Error("a save written into the folder watched again was not seen")
	}
}

// A folder holding only files is listed and nothing more; a burst of Writes
// to one folder is one check.
func TestSubfolder_ChecksStayCheap(t *testing.T) {
	gw := &gameWatch{checks: make(chan string, folderQueueSize)}
	for i := 0; i < 100; i++ {
		gw.queueCheck(`C:\saves\profiles`)
	}
	if n := len(gw.checks); n != 1 {
		t.Errorf("100 Writes to one folder queued %d checks, want 1", n)
	}
}
