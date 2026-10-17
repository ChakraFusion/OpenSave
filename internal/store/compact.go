package store

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/opensave/opensave/internal/fsx"
)

// Giving the space of deleted rows back to the disk.
//
// SQLite keeps the pages rows were deleted from, for rows to come; the file
// never shrinks on its own. Snapshots are merged, pruned and replaced all the
// time, and each held a row per file of its save — a quarter of a million
// for one game — so the file grew by what every snapshot ever deleted had
// held: 663 MB on one device, 262 MB of it unused.
//
// With auto_vacuum set to INCREMENTAL the free pages can be handed back a
// few at a time (Compact), never holding the database for long. A database
// being created gets the setting for free: it takes effect only on an empty
// file, so openDB asks for it before anything else. One created by an older
// build needs a full VACUUM to take it (EnableIncrementalVacuum), which the
// daemon runs once in the background after it has started, not in Open: on a
// large database that is a rewrite of some seconds, and the first start after
// updating would wait for it.

// freePagesWorthCompacting is how many free pages are given back at a time,
// and fewer are not worth the bother: 4 MB at the default page size.
const freePagesWorthCompacting = 1024

func (s *Store) incrementalVacuum() bool {
	var mode int
	return s.db.Get(&mode, `PRAGMA auto_vacuum`) == nil && mode == 2 // 2 = INCREMENTAL
}

// EnableIncrementalVacuum switches a database created by an older build to
// incremental auto-vacuum, with the one full VACUUM that takes. It reports
// whether it did; on a database that already has it, it does nothing.
//
// The VACUUM rewrites the whole file: it holds the database while it runs and
// needs about the file's size free on the same disk for the copy. Without
// that much free it is not attempted, and the error says so.
func (s *Store) EnableIncrementalVacuum() (bool, error) {
	if s.incrementalVacuum() {
		return false, nil
	}
	if s.path != "" {
		if info, err := os.Stat(s.path); err == nil {
			if free, ok := fsx.FreeBytes(filepath.Dir(s.path)); ok && free < uint64(info.Size()) {
				return false, fmt.Errorf("giving unused space back needs about %d MB free next to the database, and %d MB are",
					info.Size()>>20, free>>20)
			}
		}
	}
	if _, err := s.db.Exec(`PRAGMA auto_vacuum = INCREMENTAL`); err != nil {
		return false, err
	}
	if _, err := s.db.Exec(`VACUUM`); err != nil {
		return false, err
	}
	return true, nil
}

// Compact hands the database file's unused pages back to the disk, when
// there are enough of them to matter.
func (s *Store) Compact() {
	var free int
	if err := s.db.Get(&free, `PRAGMA freelist_count`); err != nil || free < freePagesWorthCompacting {
		return
	}
	_, _ = s.db.Exec(`PRAGMA incremental_vacuum`)
}
