package store

import (
	"database/sql"
	"errors"
	"fmt"
)

// CreateBranch adds a new (initially empty) branch to a game.
func (s *Store) CreateBranch(gameID, name string) error {
	_, err := s.db.Exec(`INSERT INTO branches (game_id, name) VALUES (?, ?)`, gameID, name)
	if err != nil {
		return fmt.Errorf("create branch %s/%s: %w", gameID, name, err)
	}
	return nil
}

// ListBranches returns every branch name for a game.
func (s *Store) ListBranches(gameID string) ([]string, error) {
	var names []string
	if err := s.db.Select(&names, `SELECT name FROM branches WHERE game_id = ? ORDER BY name`, gameID); err != nil {
		return nil, fmt.Errorf("list branches for %s: %w", gameID, err)
	}
	return names, nil
}

// DeleteBranchRow removes a branch's row (its snapshots are deleted
// separately by the snapshot manager so the zip files go too).
func (s *Store) DeleteBranchRow(gameID, name string) error {
	_, err := s.db.Exec(`DELETE FROM branches WHERE game_id = ? AND name = ?`, gameID, name)
	if err != nil {
		return fmt.Errorf("delete branch %s/%s: %w", gameID, name, err)
	}
	return nil
}

// SwitchActiveBranch updates a game's active_branch pointer. Callers are
// responsible for the filesystem side (auto-snapshotting the outgoing
// branch, clearing the save folder, restoring the incoming branch's latest
// snapshot) via internal/snapshot before calling this — this method only
// updates the pointer once that has succeeded.
//
// A real switch also forgets what this device and its peers were last known
// to share of the game: the paths, the agreed state, the state handed over,
// and the files recorded as deleted — for every peer and every save location.
// All of it describes the branch being left. Kept, it was read against the
// branch arrived at: a new branch starts with an empty folder, and against
// the old record of shared files that folder reads as every file deleted
// here — which a sync then passes on, deleting them on the other device. It
// is how answering a conflict with "keep both" on both devices emptied both
// saves. With the record gone, the next sync meets the other device as it
// would the first time: it takes what it lacks, and asks where both differ,
// and never deletes on the strength of history that is not this branch's.
func (s *Store) SwitchActiveBranch(gameID, branchName string) error {
	tx, err := s.db.Beginx()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var current string
	if err := tx.Get(&current, `SELECT active_branch FROM games WHERE id = ?`, gameID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		return fmt.Errorf("switch active branch for %s: %w", gameID, err)
	}
	if current == branchName {
		return tx.Commit()
	}
	if _, err := tx.Exec(`UPDATE games SET active_branch = ? WHERE id = ?`, branchName, gameID); err != nil {
		return fmt.Errorf("switch active branch for %s: %w", gameID, err)
	}
	for _, q := range []string{
		`UPDATE game_peer_sync_state SET last_synced_files = '[]', last_synced_dirs = '[]', agreed_hash = '', pushed_hash = '' WHERE game_id = ?`,
		`UPDATE game_root_sync_state SET last_synced_files = '[]', last_synced_dirs = '[]', agreed_hash = '', pushed_hash = '' WHERE game_id = ?`,
		`DELETE FROM deleted_files WHERE game_id = ?`,
	} {
		if _, err := tx.Exec(q, gameID); err != nil {
			return fmt.Errorf("forget %s's shared history on switching branch: %w", gameID, err)
		}
	}
	return tx.Commit()
}
