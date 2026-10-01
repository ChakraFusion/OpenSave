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
