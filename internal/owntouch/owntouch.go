// Package owntouch remembers which paths in save folders OpenSave itself has
// just written or removed, so the watcher can tell those changes apart from
// ones a game or a person made.
//
// Without it every change OpenSave applied — a file pulled from a peer, a
// deletion a peer asked for, the mtimes "keep mine" touches — looked to the
// watcher exactly like the game saving. A peer deleting files one request at
// a time over several minutes produced a full auto-snapshot every couple of
// seconds, each of a save that was neither the old one nor the new one, and
// those filled the retention budget and pushed out the snapshots that mattered.
package owntouch

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

// window is how long a mark counts. Long enough to cover the watcher's
// debounce and a slow burst of events behind it; short enough that the same
// path changed again by a game a little later is not mistaken for ours.
const window = 2 * time.Minute

var (
	mu     sync.Mutex
	marked = map[string]time.Time{}
)

func key(path string) string {
	p := filepath.Clean(path)
	if runtime.GOOS == "windows" {
		p = strings.ToLower(p)
	}
	return p
}

// Mark records that OpenSave itself just created, wrote, removed or re-dated
// path. Its parent folder is marked too: creating or removing an entry is an
// event on the folder as well.
func Mark(path string) {
	if path == "" {
		return
	}
	now := time.Now()
	mu.Lock()
	defer mu.Unlock()
	marked[key(path)] = now
	marked[key(filepath.Dir(path))] = now
	if len(marked) > 50000 {
		for k, at := range marked {
			if now.Sub(at) > window {
				delete(marked, k)
			}
		}
	}
}

// slack is how much later than its mark a file OpenSave wrote may be dated:
// re-dating to now happens just after marking, and some filesystems keep
// times to two seconds.
const slack = 2 * time.Second

// Recent reports whether the change just seen at path is one OpenSave made:
// the path was marked within the window, and nothing has written it since.
//
// The second half matters as much as the first. A game often saves straight
// after a sync — the same file OpenSave has just put there — and a mark alone
// would take that save for the sync's for two minutes: no snapshot of it, and
// no new version to hand to anyone. Whatever OpenSave leaves behind is dated
// no later than its mark (a pulled file keeps the time it was written to its
// temporary name, or is given the peer's; re-dating sets now), so a file
// dated later was written by someone else. A path that is gone was removed by
// OpenSave; a folder carries no save of its own, so its mark is enough.
func Recent(path string) bool {
	mu.Lock()
	at, ok := marked[key(path)]
	mu.Unlock()
	if !ok || time.Since(at) > window {
		return false
	}
	fi, err := os.Lstat(path)
	if err != nil || fi.IsDir() {
		return true
	}
	return !fi.ModTime().After(at.Add(slack))
}
