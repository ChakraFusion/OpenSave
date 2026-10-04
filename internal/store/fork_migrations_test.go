package store

import (
	"path/filepath"
	"testing"
)

// A database made by an earlier build of the fork recorded its migrations
// under the fork's old names. Opened by this build, it is renamed, not
// migrated a second time - which would fail, tables and columns being there
// already - and its data stays.
func TestOpen_RenamesTheForksOldMigrationNames(t *testing.T) {
	path := filepath.Join(t.TempDir(), "opensave.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.CreateGame(Game{ID: "g", Name: "g", SavePath: t.TempDir(), ActiveBranch: "main"}); err != nil {
		t.Fatal(err)
	}
	for old, renamed := range forkMigrationNames {
		if _, err := s.db.Exec(`UPDATE schema_migrations SET name = ? WHERE name = ?`, old, renamed); err != nil {
			t.Fatal(err)
		}
	}
	s.Close()

	s, err = Open(path)
	if err != nil {
		t.Fatalf("a database from an earlier fork build no longer opens: %v", err)
	}
	defer s.Close()
	for old, renamed := range forkMigrationNames {
		var n int
		if err := s.db.Get(&n, `SELECT COUNT(*) FROM schema_migrations WHERE name = ?`, renamed); err != nil || n != 1 {
			t.Errorf("%s recorded %d times, want once (%v)", renamed, n, err)
		}
		if err := s.db.Get(&n, `SELECT COUNT(*) FROM schema_migrations WHERE name = ?`, old); err != nil || n != 0 {
			t.Errorf("the old name %s is still recorded", old)
		}
	}
	if _, err := s.GetGame("g"); err != nil {
		t.Errorf("the game is gone after renaming: %v", err)
	}
}
