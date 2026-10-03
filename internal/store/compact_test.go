package store

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
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
