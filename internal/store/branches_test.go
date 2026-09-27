package store

import "testing"

// A branch switch forgets what the devices shared of the branch being left —
// paths, agreed state, the state handed over, recorded deletions — for every
// peer and location. Kept, a new branch's empty folder read against it as
// every file deleted, and a sync deleted them on the other device.
func TestSwitchingBranchForgetsTheOldBranchesSharedHistory(t *testing.T) {
	s := openTestStore(t)
	if err := s.CreateGame(Game{ID: "g", Name: "G", SavePath: `C:\Saves\G`, ActiveBranch: "main"}); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateBranch("g", "run2"); err != nil {
		t.Fatal(err)
	}
	remember := func() {
		t.Helper()
		for _, peer := range []string{"deck", "laptop"} {
			if err := s.SetSyncState("g", peer, []string{"slot1.sav"}, []string{"saves"}); err != nil {
				t.Fatal(err)
			}
			_ = s.SetAgreedHash("g", peer, "agreed")
			_ = s.SetPushedHash("g", peer, "pushed")
			if err := s.SetSyncStateForRoot("g", peer, "config", []string{"video.ini"}, nil); err != nil {
				t.Fatal(err)
			}
			_ = s.SetAgreedHashForRoot("g", peer, "config", "agreed-config")
			_ = s.SetPushedHashForRoot("g", peer, "config", "pushed-config")
		}
		if err := s.RecordDeletedFiles("g", []DeletedFile{{Path: "old.sav", Hash: "h", DeletedAtMs: 1}}); err != nil {
			t.Fatal(err)
		}
	}
	remembered := func(peer string) bool {
		files, _, _ := s.GetSyncState("g", peer)
		rootFiles, _, _ := s.GetSyncStateForRoot("g", peer, "config")
		deleted, _ := s.DeletedFiles("g", "")
		return len(files) > 0 || s.GetAgreedHash("g", peer) != "" || s.GetPushedHash("g", peer) != "" ||
			len(rootFiles) > 0 || s.GetAgreedHashForRoot("g", peer, "config") != "" ||
			s.GetPushedHashForRoot("g", peer, "config") != "" || len(deleted) > 0
	}

	remember()
	// Switching to the branch it is already on changes nothing, and forgets
	// nothing.
	if err := s.SwitchActiveBranch("g", "main"); err != nil {
		t.Fatal(err)
	}
	if files, _, _ := s.GetSyncState("g", "deck"); len(files) == 0 || s.GetAgreedHash("g", "deck") != "agreed" {
		t.Fatal("a switch to the branch already active forgot the shared history")
	}

	if err := s.SwitchActiveBranch("g", "run2"); err != nil {
		t.Fatal(err)
	}
	g, err := s.GetGame("g")
	if err != nil {
		t.Fatal(err)
	}
	if g.ActiveBranch != "run2" {
		t.Fatalf("active branch %q, want run2", g.ActiveBranch)
	}
	for _, peer := range []string{"deck", "laptop"} {
		if remembered(peer) {
			t.Errorf("after switching branch, what was shared with %s of the old branch is still remembered", peer)
		}
	}

	if err := s.SwitchActiveBranch("nope", "run2"); err != ErrNotFound {
		t.Errorf("switching a game that does not exist: %v, want ErrNotFound", err)
	}
}
