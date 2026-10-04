package syncengine

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/opensave/opensave/internal/delta"
	"github.com/opensave/opensave/internal/owntouch"
)

// A pull that finished and whose file the game then saved over is not a
// pull that stopped part-way: resuming it would replace the new save with
// the peer's.
func TestChangedSincePulled(t *testing.T) {
	dir := t.TempDir()
	pulled := filepath.Join(dir, "progress.sav")
	pull := func(content string) {
		owntouch.Mark(pulled)
		if err := os.WriteFile(pulled, []byte(content), 0o666); err != nil {
			t.Fatal(err)
		}
		owntouch.Settled(pulled)
	}
	build := func() delta.Manifest {
		m, err := delta.BuildManifest(dir)
		if err != nil {
			t.Fatal(err)
		}
		return m
	}
	d := Decision{FilesToPull: []string{"progress.sav"}}

	pull("from A")
	remote := build()

	// The game saves straight after the pull.
	time.Sleep(20 * time.Millisecond)
	if err := os.WriteFile(pulled, []byte("from B"), 0o666); err != nil {
		t.Fatal(err)
	}
	delta.InvalidateRoot(dir)
	if !changedSincePulled(dir, build(), remote, d) {
		t.Error("a save written over a finished pull was taken for the pull stopping part-way")
	}

	// The pull wrote an old copy and stopped: still as OpenSave left it.
	pull("old")
	delta.InvalidateRoot(dir)
	if changedSincePulled(dir, build(), remote, d) {
		t.Error("a pull that stopped part-way was taken for a change made here")
	}

	// A file the pull never touched differs: not this pull's to judge.
	if changedSincePulled(dir, build(), remote, Decision{}) {
		t.Error("a difference in a file the pull did not touch was taken for a change after it")
	}
}
