package owntouch

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestMarkAndRecent(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "sub", "slot1.sav")
	if Recent(file) {
		t.Fatal("unmarked path reported as ours")
	}
	Mark(file)
	if err := os.MkdirAll(filepath.Dir(file), 0o777); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte("pulled"), 0o666); err != nil {
		t.Fatal(err)
	}
	if !Recent(file) {
		t.Error("marked path not reported as ours")
	}
	if !Recent(filepath.Dir(file)) {
		t.Error("the parent folder of a marked path not reported as ours")
	}
	if Recent(filepath.Join(dir, "sub", "slot2.sav")) {
		t.Error("a sibling of a marked path reported as ours")
	}
}

// A game saving over a file OpenSave has just written is the game's change,
// however soon after.
func TestRecent_AWriteAfterTheMarkIsNotOurs(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "slot1.sav")
	Mark(file)
	if err := os.WriteFile(file, []byte("pulled"), 0o666); err != nil {
		t.Fatal(err)
	}
	if !Recent(file) {
		t.Fatal("the file OpenSave wrote is not reported as ours")
	}
	// The game saves a little later.
	later := time.Now().Add(5 * time.Second)
	if err := os.WriteFile(file, []byte("played"), 0o666); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(file, later, later); err != nil {
		t.Fatal(err)
	}
	if Recent(file) {
		t.Error("a save written after OpenSave's was taken for OpenSave's")
	}
}

// A file OpenSave has just written, then deleted by a game or a person, is
// their deletion: it has to sync and be snapshotted. Only a path OpenSave
// removed itself is its own when found gone.
func TestRecent_AFileDeletedAfterOurWriteIsNotOurs(t *testing.T) {
	dir := t.TempDir()
	pulled := filepath.Join(dir, "pulled.sav")
	Mark(pulled)
	if err := os.WriteFile(pulled, []byte("from the peer"), 0o666); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(pulled); err != nil {
		t.Fatal(err)
	}
	if Recent(pulled) {
		t.Error("a file deleted by someone else after OpenSave wrote it was taken for OpenSave's")
	}

	removed := filepath.Join(dir, "removed.sav")
	if err := os.WriteFile(removed, []byte("old"), 0o666); err != nil {
		t.Fatal(err)
	}
	MarkRemoved(removed)
	if err := os.Remove(removed); err != nil {
		t.Fatal(err)
	}
	if !Recent(removed) {
		t.Error("a file OpenSave removed was not reported as its own")
	}
}
