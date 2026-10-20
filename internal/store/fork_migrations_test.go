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

// A database an earlier fork build made, as it made it: the fork's own
// migrations under their old names, and not yet the two upstream ones that
// came before them since (0037_game_root_mapped, 0038_peer_links). Opened by
// this build, it is renamed and brought up to date in the one transaction,
// and keeps its data.
func TestOpen_MigratesADatabaseFromAnEarlierForkBuild(t *testing.T) {
	path := filepath.Join(t.TempDir(), "opensave.db")
	db, err := openDB(path)
	if err != nil {
		t.Fatal(err)
	}
	oldNames := map[string]string{}
	for old, renamed := range forkMigrationNames {
		oldNames[renamed] = old
	}
	entries, err := migrationsFS.ReadDir("migrations")
	if err != nil {
		t.Fatal(err)
	}
	var earlier []migration
	for _, e := range entries {
		name := e.Name()
		if name == "0037_game_root_mapped.sql" || name == "0038_peer_links.sql" {
			continue
		}
		sql, err := migrationsFS.ReadFile("migrations/" + name)
		if err != nil {
			t.Fatal(err)
		}
		if old, ok := oldNames[name]; ok {
			name = old
		}
		earlier = append(earlier, migration{name: name, sql: string(sql)})
	}
	old := &Store{db: db}
	if err := old.applyMigrations(earlier); err != nil {
		t.Fatalf("setting up the earlier build's database: %v", err)
	}
	if err := old.CreateGame(Game{ID: "g", Name: "g", SavePath: t.TempDir(), ActiveBranch: "main"}); err != nil {
		t.Fatal(err)
	}
	old.Close()

	s, err := Open(path)
	if err != nil {
		t.Fatalf("a database from an earlier fork build no longer opens: %v", err)
	}
	defer s.Close()
	var recorded []string
	if err := s.db.Select(&recorded, `SELECT name FROM schema_migrations ORDER BY name`); err != nil {
		t.Fatal(err)
	}
	if len(recorded) != len(entries) {
		t.Errorf("%d migrations recorded, want all %d: %v", len(recorded), len(entries), recorded)
	}
	for i, e := range entries {
		if i < len(recorded) && recorded[i] != e.Name() {
			t.Errorf("migration %d recorded as %s, want %s", i, recorded[i], e.Name())
		}
	}
	for _, table := range []string{"game_root_mapped", "peer_links"} {
		var n int
		if err := s.db.Get(&n, `SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = ?`, table); err != nil || n != 1 {
			t.Errorf("table %s missing after migrating (%v)", table, err)
		}
	}
	if _, err := s.GetGame("g"); err != nil {
		t.Errorf("the game is gone after migrating: %v", err)
	}
}
