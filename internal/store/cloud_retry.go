package store

import "fmt"

// CloudRetry is a snapshot whose cloud copy failed to go up, kept until it
// does. See migrations/0036_cloud_upload_retry.sql.
type CloudRetry struct {
	RemoteName string `db:"remote_name"`
	GameID     string `db:"game_id"`
	ZipPath    string `db:"zip_path"`
	FailedAtMs int64  `db:"failed_at_ms"`
	Attempts   int    `db:"attempts"`
}

// NoteCloudUploadFailed keeps a failed upload to try again, counting the
// attempts when it has failed before.
func (s *Store) NoteCloudUploadFailed(gameID, remoteName, zipPath string, atMs int64) error {
	_, err := s.db.Exec(`
		INSERT INTO cloud_upload_retry (remote_name, game_id, zip_path, failed_at_ms)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(remote_name) DO UPDATE SET
			zip_path = excluded.zip_path,
			failed_at_ms = excluded.failed_at_ms,
			attempts = attempts + 1`,
		remoteName, gameID, zipPath, atMs)
	if err != nil {
		return fmt.Errorf("note failed upload of %s: %w", remoteName, err)
	}
	return nil
}

// CloudRetries lists the failed uploads waiting to be sent again, oldest
// first.
func (s *Store) CloudRetries() ([]CloudRetry, error) {
	var out []CloudRetry
	if err := s.db.Select(&out, `
		SELECT remote_name, game_id, zip_path, failed_at_ms, attempts
		FROM cloud_upload_retry ORDER BY failed_at_ms, remote_name`); err != nil {
		return nil, fmt.Errorf("list failed uploads: %w", err)
	}
	return out, nil
}

// ForgetCloudRetry drops a failed upload: sent now, or nothing left to send.
func (s *Store) ForgetCloudRetry(remoteName string) error {
	if _, err := s.db.Exec(`DELETE FROM cloud_upload_retry WHERE remote_name = ?`, remoteName); err != nil {
		return fmt.Errorf("forget failed upload of %s: %w", remoteName, err)
	}
	return nil
}
