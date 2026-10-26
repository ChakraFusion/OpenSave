// Package store implements OpenSave's persistence layer: an embedded
// SQLite database (replacing the original single-JSON-file db.js) plus a
// one-time importer for existing users' legacy JSON data.
package store

import (
	"embed"
	"fmt"
	"io/fs"
	"os"
	"runtime"
	"sort"
	"strings"

	"github.com/jmoiron/sqlx"
	_ "modernc.org/sqlite"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

// Store wraps the SQLite connection and exposes entity-scoped query
// methods (see settings.go, games.go, branches.go, snapshots.go, peers.go,
// cloudtokens.go).
type Store struct {
	db *sqlx.DB
}

// Open creates (if needed) and opens the SQLite database at path, applying
// any migrations that haven't run yet.
func Open(path string) (*Store, error) {
	db, err := openDB(path)
	if err != nil {
		return nil, err
	}

	s := &Store{db: db}
	if err := s.migrate(); err != nil {
		db.Close()
		return nil, err
	}
	s.compactOnOpen()
	// The file holds the device's private key, the Google tokens and the
	// vault keys. SQLite creates it with the process umask, which is usually
	// world-readable; the directory around it is private now, but the file
	// should be too, as an ssh key is — the directory is the first wall and
	// this is the second. Best-effort, and a no-op in practice on Windows.
	restrictToOwner(path)
	restrictToOwner(path + "-wal")
	restrictToOwner(path + "-shm")
	return s, nil
}

// openDB opens the SQLite connection Open builds a Store on.
func openDB(path string) (*sqlx.DB, error) {
	// _pragma params ensure foreign keys are enforced (SQLite defaults them
	// off per-connection) and busy_timeout avoids spurious SQLITE_BUSY
	// errors from the watcher/api/p2p goroutines all touching the DB.
	//
	// WAL: a write is appended to one log and the database file is updated
	// from it later, rather than every write creating, flushing and deleting a
	// rollback journal of its own — which made a quarter of a million small
	// writes take most of an hour. synchronous NORMAL is what WAL is meant to
	// run with: the database stays whole whatever happens; a power cut can
	// only lose the last moments' writes, never corrupt it.
	dsn := fmt.Sprintf("file:%s?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)", path)
	db, err := sqlx.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite database: %w", err)
	}
	db.SetMaxOpenConns(1) // modernc.org/sqlite + a single file: avoid concurrent-writer lock contention

	// Tolerate columns the struct doesn't know about. Queries here use
	// SELECT *, so without this a database written by a NEWER build hard-fails
	// an older one ("missing destination name <col>") and the app won't start
	// at all — a downgrade, or just testing a beta and going back, bricks it.
	// Ignoring unmapped columns makes the schema forward-compatible instead.
	return db.Unsafe(), nil
}

// restrictToOwner sets a file to 0600 where mode bits are the access control.
// Missing files are fine (the WAL and SHM files exist only while in use).
func restrictToOwner(path string) {
	if runtime.GOOS == "windows" {
		return
	}
	if info, err := os.Stat(path); err == nil && info.Mode().Perm()&0o077 != 0 {
		_ = os.Chmod(path, 0o600)
	}
}

// Close releases the underlying database connection.
func (s *Store) Close() error {
	return s.db.Close()
}

// migration is one embedded schema step, applied in name order.
type migration struct {
	name string
	sql  string
}

func (s *Store) migrate() error {
	entries, err := fs.ReadDir(migrationsFS, "migrations")
	if err != nil {
		return fmt.Errorf("read embedded migrations: %w", err)
	}
	var migrations []migration
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".sql") {
			continue
		}
		sqlBytes, err := migrationsFS.ReadFile("migrations/" + e.Name())
		if err != nil {
			return fmt.Errorf("read migration %s: %w", e.Name(), err)
		}
		migrations = append(migrations, migration{name: e.Name(), sql: string(sqlBytes)})
	}
	sort.Slice(migrations, func(i, j int) bool { return migrations[i].name < migrations[j].name })
	return s.applyMigrations(migrations)
}

// applyMigrations applies the migrations not yet recorded, all in one
// transaction, after recording under their upstream names any the fork's
// earlier builds applied under their own (renameForkMigrations).
//
// One, not one per migration: a new database has every migration to apply,
// and committed one by one that was a commit each — with a rollback journal
// some 150 flushes before the daemon could start, which a busy Windows disk
// took over thirty seconds to do, past the time the CLI and the tests give a
// daemon to come up. In one transaction it is a single commit, and a failed
// step leaves the schema as it was rather than half upgraded.
func (s *Store) applyMigrations(migrations []migration) error {
	tx, err := s.db.Beginx()
	if err != nil {
		return fmt.Errorf("begin migrations: %w", err)
	}
	defer tx.Rollback() // a no-op once committed

	if _, err := tx.Exec(`CREATE TABLE IF NOT EXISTS schema_migrations (name TEXT PRIMARY KEY)`); err != nil {
		return fmt.Errorf("create schema_migrations table: %w", err)
	}
	if err := renameForkMigrations(tx); err != nil {
		return err
	}
	var names []string
	if err := tx.Select(&names, `SELECT name FROM schema_migrations`); err != nil {
		return fmt.Errorf("read applied migrations: %w", err)
	}
	applied := make(map[string]bool, len(names))
	for _, name := range names {
		applied[name] = true
	}

	for _, m := range migrations {
		if applied[m.name] {
			continue
		}
		if _, err := tx.Exec(m.sql); err != nil {
			return fmt.Errorf("apply migration %s: %w", m.name, err)
		}
		if _, err := tx.Exec(`INSERT INTO schema_migrations (name) VALUES (?)`, m.name); err != nil {
			return fmt.Errorf("record migration %s: %w", m.name, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit migrations: %w", err)
	}
	return nil
}

// forkMigrationNames maps the names earlier builds of this fork gave its
// migrations to the names they have upstream (Liquid-co/OpenSave PRs #27,
// #30, #31, #32, #33), where they follow 0037_game_root_mapped. The files
// carry the upstream names now; a database these builds created recorded
// them under the old ones.
var forkMigrationNames = map[string]string{
	"0037_game_versions.sql":        "0039_game_versions.sql",
	"0039_snapshot_content.sql":     "0040_snapshot_content.sql",
	"0040_marks.sql":                "0041_marks.sql",
	"0041_sync_device_settings.sql": "0042_sync_device_settings.sql",
	"0042_settings_files.sql":       "0043_settings_files.sql",
}

// renameForkMigrations records migrations applied under the fork's old names
// under their upstream names, so they are not applied a second time -
// creating a table or adding a column that is already there fails, and the
// database would not open. Inside the migrations' transaction, so a database
// is renamed and migrated whole or not at all.
func renameForkMigrations(tx *sqlx.Tx) error {
	for old, renamed := range forkMigrationNames {
		// Looked for first: an UPDATE that changes nothing still makes the
		// transaction a write, and an up-to-date database should open
		// without one.
		var found int
		if err := tx.Get(&found, `SELECT COUNT(*) FROM schema_migrations WHERE name = ?`, old); err != nil {
			return fmt.Errorf("look for migration %s: %w", old, err)
		}
		if found == 0 {
			continue
		}
		if _, err := tx.Exec(`UPDATE schema_migrations SET name = ? WHERE name = ?
			AND NOT EXISTS (SELECT 1 FROM schema_migrations WHERE name = ?)`,
			renamed, old, renamed); err != nil {
			return fmt.Errorf("rename migration %s: %w", old, err)
		}
	}
	return nil
}
