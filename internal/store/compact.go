package store

// Giving the space of deleted rows back to the disk.
//
// SQLite keeps the pages rows were deleted from, for rows to come; the file
// never shrinks on its own. Snapshots are merged, pruned and replaced all the
// time, and each held a row per file of its save — a quarter of a million
// for one game — so the file grew by what every snapshot ever deleted had
// held: 663 MB on one device, 262 MB of it unused.
//
// With auto_vacuum set to INCREMENTAL the free pages can be handed back a
// few at a time (Compact), never holding the database for long. Setting it
// takes one full VACUUM, done once, when the database is opened and nothing
// else is using it: some seconds for a large one.

// freePagesWorthCompacting is how many free pages are given back at a time,
// and fewer are not worth the bother: 4 MB at the default page size.
const freePagesWorthCompacting = 1024

func (s *Store) compactOnOpen() {
	var mode int
	if err := s.db.Get(&mode, `PRAGMA auto_vacuum`); err != nil {
		return
	}
	if mode != 2 { // 2 = INCREMENTAL
		if _, err := s.db.Exec(`PRAGMA auto_vacuum = INCREMENTAL`); err != nil {
			return
		}
		_, _ = s.db.Exec(`VACUUM`)
		return
	}
	s.Compact()
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
