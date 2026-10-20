package delta

import (
	"path/filepath"
	"sync"
	"sync/atomic"
)

// Builds of one folder's manifest that overlap are coalesced.
//
// A manifest is asked for from many places at once: a sync of the game here,
// each paired device asking for it, the check for an emptied save, the
// watcher, cloud backup. For a save of a quarter of a million files each of
// those walked the whole folder, and on a busy machine the walks piled up
// faster than they finished (GitHub #15).
//
// What must not happen is handing out a walk that began before the caller
// asked. A sync started because a file changed would be given a manifest from
// before the change, find nothing to send, and the change would wait for the
// next periodic check. So a caller that arrives while a build is running does
// not share that build: it waits for the next one, which starts once the
// running one is done and is shared by everyone who arrived meanwhile. Every
// caller's manifest comes from a walk that started after it asked, and a
// folder is walked at most twice at a time however many ask.
type manifestBuilds struct {
	mu      sync.Mutex
	folders map[string]*folderBuilds
}

// folderBuilds is one folder's builds: whether one is running, and the call
// the next one will answer, if anyone is waiting for it.
type folderBuilds struct {
	running bool
	next    *manifestCall
}

// manifestCall is one build's result, for the callers sharing it.
type manifestCall struct {
	done   chan struct{}
	m      Manifest
	err    error
	takers atomic.Int32
}

var builds = &manifestBuilds{folders: map[string]*folderBuilds{}}

// Test hooks: after a build's walk, before its result is handed out; and when
// a caller settles down to wait for the next build.
var (
	testHookBuilt  func(root string)
	testHookJoined func(root string)
)

func (b *manifestBuilds) build(root string) (Manifest, error) {
	key := filepath.Clean(root)
	b.mu.Lock()
	f := b.folders[key]
	if f == nil {
		f = &folderBuilds{}
		b.folders[key] = f
	}
	if f.running {
		if f.next == nil {
			f.next = &manifestCall{done: make(chan struct{})}
		}
		call := f.next
		call.takers.Add(1)
		b.mu.Unlock()
		if testHookJoined != nil {
			testHookJoined(key)
		}
		<-call.done
		return call.take()
	}
	f.running = true
	b.mu.Unlock()

	m, err := buildManifest(root)
	if testHookBuilt != nil {
		testHookBuilt(key)
	}
	b.runNext(key, f, root)
	return m, err
}

// runNext starts the build the callers who arrived during the last one are
// waiting for, or marks the folder idle if nobody is. The build runs on its
// own: the caller that finished the last one has its answer and goes.
func (b *manifestBuilds) runNext(key string, f *folderBuilds, root string) {
	b.mu.Lock()
	call := f.next
	f.next = nil
	if call == nil {
		f.running = false
		delete(b.folders, key)
		b.mu.Unlock()
		return
	}
	b.mu.Unlock()
	go func() {
		call.m, call.err = buildManifest(root)
		if testHookBuilt != nil {
			testHookBuilt(key)
		}
		close(call.done)
		b.runNext(key, f, root)
	}()
}

// take hands one caller the shared result. Every caller but the last gets its
// own copy of the file lists: nothing here edits a manifest in place today,
// and a shared one is where a change that starts to would go wrong quietly.
func (c *manifestCall) take() (Manifest, error) {
	if c.err != nil {
		return Manifest{}, c.err
	}
	if c.takers.Add(-1) == 0 {
		return c.m, nil
	}
	return c.m.clone(), nil
}

// clone copies a manifest's maps and lists. File entries are values; their
// block lists are shared, as the hash cache already shares them.
func (m Manifest) clone() Manifest {
	c := m
	c.Files = make(map[string]FileEntry, len(m.Files))
	for p, e := range m.Files {
		c.Files[p] = e
	}
	c.Dirs = append([]string(nil), m.Dirs...)
	if m.Extra != nil {
		c.Extra = make(map[string]RootManifest, len(m.Extra))
		for name, r := range m.Extra {
			files := make(map[string]FileEntry, len(r.Files))
			for p, e := range r.Files {
				files[p] = e
			}
			c.Extra[name] = RootManifest{Files: files, Dirs: append([]string(nil), r.Dirs...)}
		}
	}
	return c
}
