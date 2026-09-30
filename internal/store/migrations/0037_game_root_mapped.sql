-- When each extra save location got the folder it has now. A snapshot taken
-- before that says nothing about what this folder held: its files for the
-- location came from another folder, or from another machine — a backup
-- restored onto a replacement PC. Read as what the location held before,
-- they made a location just given its folder, still empty until the files
-- arrived, look emptied, and the game was held back instead of fetching them
-- (internal/p2p/syncengine/hold.go).
--
-- A table of its own rather than a column on game_roots: older versions read
-- game_roots rows strictly and a new column breaks them; a table they do not
-- know is simply ignored. A location with no row here has had its folder
-- since before this was recorded.
CREATE TABLE game_root_mapped (
    game_id   TEXT NOT NULL REFERENCES games(id) ON DELETE CASCADE,
    name      TEXT NOT NULL,
    mapped_ms INTEGER NOT NULL,
    PRIMARY KEY (game_id, name)
);
