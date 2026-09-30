-- Snapshots whose cloud copy failed to go up, kept until it does. An upload
-- that failed — the network down, a name that would not resolve, a connection
-- that stalled — used to be logged and forgotten, so the backup was missing
-- every snapshot taken while the network was out. The cloud check sends these
-- again once it can reach the provider (internal/daemon/cloud_retry.go).
--
-- A table of its own rather than a column on snapshots: older versions read
-- snapshot rows strictly and a new column breaks them; a table they do not
-- know is simply ignored.
CREATE TABLE cloud_upload_retry (
    remote_name  TEXT PRIMARY KEY,
    game_id      TEXT NOT NULL REFERENCES games(id) ON DELETE CASCADE,
    zip_path     TEXT NOT NULL,
    failed_at_ms INTEGER NOT NULL,
    attempts     INTEGER NOT NULL DEFAULT 1
);
