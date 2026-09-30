package delta

import (
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

// Callers who ask for a folder's manifest while it is being built share one
// build between them — the next one, which starts after they asked, so each
// sees what the folder held when it asked (manifest_builds.go, GitHub #15).
func TestOverlappingBuildsOfOneFolderShareTheNextWalk(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.sav"), []byte("a"), 0o666); err != nil {
		t.Fatal(err)
	}

	var walks atomic.Int32
	firstWalked, release := make(chan struct{}), make(chan struct{})
	joined := make(chan struct{}, 16)
	testHookBuilt = func(string) {
		if walks.Add(1) == 1 {
			close(firstWalked)
			<-release // the first build's answer is held back while others arrive
		}
	}
	testHookJoined = func(string) { joined <- struct{}{} }
	defer func() { testHookBuilt, testHookJoined = nil, nil }()

	type answer struct {
		m   Manifest
		err error
	}
	first := make(chan answer, 1)
	go func() { m, err := BuildManifest(dir); first <- answer{m, err} }()
	<-firstWalked

	// Written after the first walk: a caller asking from now on must see it.
	if err := os.WriteFile(filepath.Join(dir, "b.sav"), []byte("b"), 0o666); err != nil {
		t.Fatal(err)
	}
	const late = 9
	later := make(chan answer, late)
	for i := 0; i < late; i++ {
		go func() { m, err := BuildManifest(dir); later <- answer{m, err} }()
	}
	for i := 0; i < late; i++ {
		select {
		case <-joined:
		case <-time.After(10 * time.Second):
			close(release)
			t.Fatalf("only %d of %d callers waited for the next build; the rest walked the folder themselves (%d walks)",
				i, late, walks.Load())
		}
	}
	close(release)

	if a := <-first; a.err != nil || len(a.m.Files) != 1 {
		t.Errorf("the first caller got %d files (%v), want the folder as it was: a.sav", len(a.m.Files), a.err)
	}
	for i := 0; i < late; i++ {
		a := <-later
		if a.err != nil {
			t.Fatal(a.err)
		}
		if _, ok := a.m.Files["b.sav"]; !ok {
			t.Errorf("a caller who asked after b.sav was written got a manifest without it: %v", a.m.Files)
		}
	}
	if got := walks.Load(); got != 2 {
		t.Errorf("the folder was walked %d times for %d callers, want 2", got, late+1)
	}

	// Each caller has a manifest of its own.
	one, _ := BuildManifest(dir)
	two, _ := BuildManifest(dir)
	delete(one.Files, "a.sav")
	if _, ok := two.Files["a.sav"]; !ok {
		t.Error("two callers' manifests share one file map")
	}
}
