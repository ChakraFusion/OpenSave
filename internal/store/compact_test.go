package store

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jmoiron/sqlx"
)

// Deleted rows give their space back to the disk: the file shrinks, rather
// than holding everything it ever held.
func TestCompactGivesSpaceBack(t *testing.T) {
	path := filepath.Join(t.TempDir(), "opensave.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	var mode int
	_ = s.db.Get(&mode, `PRAGMA auto_vacuum`)
	if mode != 2 {
		t.Fatalf("auto_vacuum = %d, want incremental (2)", mode)
	}
	// A table of the test's own, so the test depends on no other.
	if _, err := s.db.Exec(`CREATE TABLE padding (k TEXT PRIMARY KEY, v TEXT NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	pad := strings.Repeat("x", 2000)
	tx := s.db.MustBegin()
	for i := 0; i < 4000; i++ {
		tx.MustExec(`INSERT INTO padding (k, v) VALUES (?, ?)`, fmt.Sprint("k", i), pad)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	_, _ = s.db.Exec(`PRAGMA wal_checkpoint(TRUNCATE)`)
	full, _ := os.Stat(path)
	if _, err := s.db.Exec(`DELETE FROM padding`); err != nil {
		t.Fatal(err)
	}
	s.Compact()
	_, _ = s.db.Exec(`PRAGMA wal_checkpoint(TRUNCATE)`)
	after, _ := os.Stat(path)
	if after.Size() >= full.Size()/2 {
		t.Errorf("file %d bytes after deleting everything, %d before: nothing given back", after.Size(), full.Size())
	}
	s.Close()
}

// Clearing a large save's deletion records is one transaction, not one write
// to disk per file.
func TestClearDeletedFilesIsOneTransaction(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "opensave.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.CreateGame(Game{ID: "g", Name: "g", SavePath: t.TempDir(), ActiveBranch: "main"}); err != nil {
		t.Fatal(err)
	}
	var files []DeletedFile
	var paths []string
	for i := 0; i < 50000; i++ {
		p := fmt.Sprintf("map/%d/%d.bin", i/100, i)
		files = append(files, DeletedFile{Path: p, Hash: "h"})
		paths = append(paths, p)
	}
	if err := s.RecordDeletedFiles("g", files); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	if err := s.ClearDeletedFiles("g", "", paths); err != nil {
		t.Fatal(err)
	}
	took := time.Since(start)
	left, _ := s.DeletedFiles("g", "")
	if len(left) != 0 {
		t.Errorf("%d records left", len(left))
	}
	// Generous: CI runs this under the race detector on shared runners, where
	// one statement per file took 10s; what this guards against took minutes.
	if took > 30*time.Second {
		t.Errorf("clearing 50,000 records took %v", took)
	}
	t.Logf("cleared 50,000 deletion records in %v", took)
}

// The database writes through a write-ahead log, not a rollback journal
// created and deleted for every write.
func TestDatabaseUsesWAL(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "opensave.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	var mode string
	if err := s.db.Get(&mode, `PRAGMA journal_mode`); err != nil || mode != "wal" {
		t.Errorf("journal_mode = %q (%v), want wal", mode, err)
	}
}

// A database created by an older build, without incremental auto-vacuum, is
// not rewritten when it is opened: the start stays fast. It is switched over
// once, by EnableIncrementalVacuum, and after that gives space back as usual.
func TestOlderDatabaseIsSwitchedOverAfterOpenNotDuringIt(t *testing.T) {
	path := filepath.Join(t.TempDir(), "opensave.db")
	old, err := sqlx.Open("sqlite", "file:"+path) // as an older build left it: no auto-vacuum
	if err != nil {
		t.Fatal(err)
	}
	if _, err := old.Exec(`CREATE TABLE older_build (k TEXT)`); err != nil {
		t.Fatal(err)
	}
	old.Close()

	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if s.incrementalVacuum() {
		t.Fatal("Open rewrote an older database; that is for after the start")
	}
	converted, err := s.EnableIncrementalVacuum()
	if err != nil || !converted {
		t.Fatalf("EnableIncrementalVacuum = %v, %v; want it switched over", converted, err)
	}
	if !s.incrementalVacuum() {
		t.Fatal("auto_vacuum is still not incremental")
	}
	if converted, err := s.EnableIncrementalVacuum(); err != nil || converted {
		t.Errorf("a second EnableIncrementalVacuum = %v, %v; want nothing to do", converted, err)
	}
}
