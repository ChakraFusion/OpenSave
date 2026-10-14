package store

import (
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"modernc.org/sqlite"
)

// commitCounts counts the commits made on each database file, by the path in
// its connection string. The hook is the driver's and so global: registered
// once, it watches every store any test in this package opens.
var commitCounts = struct {
	once sync.Once
	mu   sync.Mutex
	n    map[string]*atomic.Int64
}{n: map[string]*atomic.Int64{}}

func countCommits(t *testing.T, dbPath string) *atomic.Int64 {
	t.Helper()
	commitCounts.once.Do(func() {
		sqlite.RegisterConnectionHook(func(conn sqlite.ExecQuerierContext, dsn string) error {
			hooks, ok := conn.(sqlite.HookRegisterer)
			if !ok {
				return nil
			}
			commitCounts.mu.Lock()
			defer commitCounts.mu.Unlock()
			for path, n := range commitCounts.n {
				if strings.Contains(dsn, path) {
					hooks.RegisterCommitHook(func() int32 { n.Add(1); return 0 })
				}
			}
			return nil
		})
	})
	n := new(atomic.Int64)
	commitCounts.mu.Lock()
	commitCounts.n[dbPath] = n
	commitCounts.mu.Unlock()
	return n
}

// A new database is set up in one commit, not one per migration.
//
// Every commit flushes the journal and the database to disk, and a new
// database used to take one per migration: 37 of them, some 150 flushes
// before the daemon could start. On a busy Windows disk that took over thirty
// seconds, so a freshly installed daemon could miss the 30 s its clients
// give it to come up — CI saw `daemon never became ready` with the daemon
// still inside store.Open, flushing a migration's commit.
func TestOpeningANewDatabaseCommitsOnce(t *testing.T) {
	path := filepath.Join(t.TempDir(), "opensave.db")
	commits := countCommits(t, path)

	s, err := Open(path)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer s.Close()

	if got := commits.Load(); got != 1 {
		t.Errorf("setting up a new database took %d commits, want 1", got)
	}

	// Every migration is recorded as applied, so the next open does nothing.
	var applied int
	if err := s.db.Get(&applied, `SELECT COUNT(*) FROM schema_migrations`); err != nil {
		t.Fatal(err)
	}
	entries, err := migrationsFS.ReadDir("migrations")
	if err != nil {
		t.Fatal(err)
	}
	if applied != len(entries) {
		t.Errorf("%d migrations recorded, want all %d", applied, len(entries))
	}
}

// Opening a database that is already up to date writes nothing.
func TestOpeningAnUpToDateDatabaseDoesNotCommit(t *testing.T) {
	path := filepath.Join(t.TempDir(), "opensave.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	s.Close()

	commits := countCommits(t, path)
	s, err = Open(path)
	if err != nil {
		t.Fatalf("reopening: %v", err)
	}
	defer s.Close()
	if got := commits.Load(); got != 0 {
		t.Errorf("reopening an up-to-date database made %d commits, want 0", got)
	}
}

// A migration that fails leaves the database as it was: none of the ones
// before it in the same run are recorded either, so the next start retries
// the whole step instead of resuming from a half-applied schema.
func TestAFailedMigrationAppliesNothing(t *testing.T) {
	path := filepath.Join(t.TempDir(), "opensave.db")
	s := &Store{}
	db, err := openDB(path)
	if err != nil {
		t.Fatal(err)
	}
	s.db = db
	defer s.Close()

	err = s.applyMigrations([]migration{
		{name: "0001_ok.sql", sql: `CREATE TABLE fine (id INTEGER)`},
		{name: "0002_broken.sql", sql: `CREATE TABLE broken (`},
	})
	if err == nil || !strings.Contains(err.Error(), "0002_broken.sql") {
		t.Fatalf("applyMigrations error = %v, want one naming the broken migration", err)
	}
	var tables int
	if err := s.db.Get(&tables, `SELECT COUNT(*) FROM sqlite_master WHERE name IN ('fine', 'schema_migrations')`); err != nil {
		t.Fatal(err)
	}
	if tables != 0 {
		t.Errorf("%d tables left behind by a failed migration run, want none", tables)
	}
}
