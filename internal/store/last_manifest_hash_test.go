package store

import "testing"

// A hash taken before a snapshot is recorded only while nothing newer was
// recorded meanwhile: a newly tracked game's first snapshot, running in the
// background, finished after one taken by hand and replaced its newer value.
func TestSetLastManifestHashIf_DoesNotReplaceANewerValue(t *testing.T) {
	s := openTestStore(t)
	if err := s.CreateGame(Game{ID: "g", Name: "G", SavePath: t.TempDir()}); err != nil {
		t.Fatal(err)
	}

	if ok, err := s.SetLastManifestHashIf("g", "", "first"); err != nil || !ok {
		t.Fatalf("recording over nothing: ok=%v err=%v", ok, err)
	}
	// Another snapshot records a newer value…
	if err := s.SetLastManifestHash("g", "newer"); err != nil {
		t.Fatal(err)
	}
	// …and the one that started before it must not replace it.
	if ok, err := s.SetLastManifestHashIf("g", "first", "older"); err != nil || ok {
		t.Fatalf("an older value replaced a newer one: ok=%v err=%v", ok, err)
	}
	g, err := s.GetGame("g")
	if err != nil {
		t.Fatal(err)
	}
	if g.LastManifestHash != "newer" {
		t.Errorf("recorded %q, want %q", g.LastManifestHash, "newer")
	}
}
