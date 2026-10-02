package snapshot

import (
	"testing"
)

// The content a snapshot records and the content read back from its archive
// are named alike — which is what lets a device that only has another's
// archive, or only its file list, tell they hold the same save.
func TestContentKey_FromFilesAndFromArchiveAgree(t *testing.T) {
	env := setup(t)
	writeSave(t, env.saveDir, "slot1.sav", "one")
	writeSave(t, env.saveDir, "sub/slot2.sav", "two")
	snap, err := env.mgr.Create("game1", "", true)
	if err != nil {
		t.Fatal(err)
	}
	files, err := env.store.SnapshotFiles(snap.ID)
	if err != nil || len(files) == 0 {
		t.Fatalf("no file list recorded: %v", err)
	}
	fromArchive, err := ContentKeyOfArchive(snap.ZipPath)
	if err != nil {
		t.Fatal(err)
	}
	if got := ContentKey(files); got != fromArchive {
		t.Errorf("from the file list %s, from the archive %s", got, fromArchive)
	}
	stored, _ := env.store.GetSnapshot(snap.ID)
	if stored.ContentHash != fromArchive {
		t.Errorf("recorded content %q, want %q", stored.ContentHash, fromArchive)
	}
}

// A copy taken before a sync replaces a save already archived is that archive,
// not a second one.
func TestBeforeReplacing_AnArchivedSaveIsNotArchivedAgain(t *testing.T) {
	env := setup(t)
	writeSave(t, env.saveDir, "slot1.sav", "progress")
	first, err := env.mgr.Create("game1", "", true)
	if err != nil {
		t.Fatal(err)
	}
	copy, err := env.mgr.CreateBeforeReplacing("game1", "before sync")
	if err != nil {
		t.Fatal(err)
	}
	if copy.ID != first.ID {
		t.Errorf("a second archive %s of the save %s already holds", copy.ID, first.ID)
	}
	snaps, _ := env.store.ListSnapshots("game1", "main")
	if len(snaps) != 1 {
		t.Errorf("%d snapshots, want 1", len(snaps))
	}
}

// Several snapshots of the same files — one per device the save was compared
// with — are kept once: the newest unless one is pinned or taken by hand;
// pinned ones are never removed; the others' ids still lead to it.
func TestMergeDuplicates(t *testing.T) {
	env := setup(t)
	// Room for every snapshot below: this is about merging, not retention.
	game, err := env.store.GetGame("game1")
	if err != nil {
		t.Fatal(err)
	}
	game.MaxSnapshots = 50
	if err := env.store.UpdateGame(game); err != nil {
		t.Fatal(err)
	}
	writeSave(t, env.saveDir, "slot1.sav", "the save")
	var same []string
	for i := 0; i < 3; i++ {
		s, err := env.mgr.Create("game1", "", true)
		if err != nil {
			t.Fatal(err)
		}
		same = append(same, s.ID)
	}
	pinned, err := env.mgr.Create("game1", "pinned copy", true)
	if err != nil {
		t.Fatal(err)
	}
	if err := env.store.SetSnapshotPinned(pinned.ID, true); err != nil {
		t.Fatal(err)
	}
	writeSave(t, env.saveDir, "slot1.sav", "played on")
	other, err := env.mgr.Create("game1", "", true)
	if err != nil {
		t.Fatal(err)
	}
	// As if taken before contents were recorded.
	for _, id := range append(same, pinned.ID, other.ID) {
		_ = env.store.SetSnapshotContentHash(id, "")
	}

	merged, _, err := env.mgr.MergeDuplicates("game1")
	if err != nil {
		t.Fatal(err)
	}
	if merged != 3 {
		t.Errorf("merged %d, want the 3 unpinned copies of the pinned one", merged)
	}
	if _, err := env.store.GetSnapshot(pinned.ID); err != nil {
		t.Error("the pinned snapshot was removed")
	}
	if _, err := env.store.GetSnapshot(other.ID); err != nil {
		t.Error("a snapshot of different content was removed")
	}
	for _, id := range same {
		if got, ok := env.store.ResolveSnapshotID(id); !ok || got != pinned.ID {
			t.Errorf("%s leads to %q (%v), want the snapshot kept, %s", id, got, ok, pinned.ID)
		}
	}
	// Run again: nothing more to do.
	if again, _, _ := env.mgr.MergeDuplicates("game1"); again != 0 {
		t.Errorf("a second pass merged %d more", again)
	}
}
