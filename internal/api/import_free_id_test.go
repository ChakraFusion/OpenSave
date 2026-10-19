package api

import (
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/opensave/opensave/internal/store"
)

// A tracked game's imported save gets a snapshot id no other snapshot holds
// (GitHub #89). The import used to name it snap_<now> itself. When a snapshot
// taken moments before held that id, or one the snapshot manager had moved
// ahead to, the import took it for an earlier import of the same backup and
// recorded nothing, while its archive replaced that snapshot's own.
//
// Here the ids just ahead of the clock are held by snapshots of the game, as
// the manager leaves them when several are taken in one millisecond, so the
// import's own clock reading lands on one of them.
func TestBackupImport_TakesASnapshotIDNoOtherHolds(t *testing.T) {
	exporter := startTestServer(t)
	if err := os.WriteFile(filepath.Join(exporter.saveDir, "slot1.sav"), []byte("from-the-backup"), 0o666); err != nil {
		t.Fatal(err)
	}
	exporter.do(t, http.MethodPost, "/api/games", map[string]string{"name": "Shared Game", "savePath": exporter.saveDir})
	waitInitialSnapshot(t, exporter, "shared-game")
	exportPath := exportV2(t, exporter, []map[string]string{{"id": "shared-game"}})

	importer := startTestServer(t)
	if err := os.WriteFile(filepath.Join(importer.saveDir, "slot1.sav"), []byte("local"), 0o666); err != nil {
		t.Fatal(err)
	}
	importer.do(t, http.MethodPost, "/api/games", map[string]string{"name": "Shared Game", "savePath": importer.saveDir})
	waitInitialSnapshot(t, importer, "shared-game")
	settings, err := importer.daemon.Store.GetSettings()
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(settings.BackupsDir, "shared-game", "main")

	// Ids held from now to 900 ms ahead; their archives are not on disk, so
	// anything written to one of their paths is the import overwriting a
	// snapshot that was not its own.
	const ahead = 900
	t0 := time.Now().UnixMilli()
	held := map[string]string{}
	for ms := t0; ms < t0+ahead; ms++ {
		id := "snap_" + strconv.FormatInt(ms, 10)
		if _, err := importer.daemon.Store.GetSnapshot(id); err == nil {
			continue // already a real snapshot's, the game's first one
		}
		zip := filepath.Join(dir, id+".zip")
		if err := importer.daemon.Store.CreateSnapshot(store.Snapshot{
			ID: id, GameID: "shared-game", BranchName: "main",
			Timestamp: time.UnixMilli(ms).UTC().Format("2006-01-02T15:04:05.000Z"),
			Comment:   "held", IsSystemAuto: true, ZipPath: zip,
		}); err != nil {
			t.Fatal(err)
		}
		held[id] = zip
	}
	if time.Now().UnixMilli() >= t0+ahead-100 {
		t.Skip("holding the ids took too long on this machine for the import to land among them")
	}

	resp, body := importer.do(t, http.MethodPost, "/api/backup/restore", map[string]string{"sourcePath": exportPath, "mode": "overwrite"})
	if resp.StatusCode != http.StatusOK || string(body["restored"]) != "1" {
		t.Fatalf("import = %d %v, want the game restored", resp.StatusCode, body)
	}

	snaps, err := importer.daemon.Store.ListSnapshots("shared-game", "main")
	if err != nil {
		t.Fatal(err)
	}
	var imported []string
	for _, s := range snaps {
		if s.Comment == "Imported from backup" {
			imported = append(imported, s.ID)
			if _, wasHeld := held[s.ID]; wasHeld {
				t.Errorf("the import was recorded under %s, an id another snapshot held", s.ID)
			}
		}
	}
	if len(imported) != 1 {
		t.Errorf("imported snapshots = %v, want exactly one", imported)
	}
	for id, zip := range held {
		if _, err := os.Stat(zip); err == nil {
			t.Errorf("the import wrote its archive to %s, the archive of snapshot %s", filepath.Base(zip), id)
		}
	}
	if got, _ := os.ReadFile(filepath.Join(importer.saveDir, "slot1.sav")); string(got) != "from-the-backup" {
		t.Errorf("live save = %q, want from-the-backup", got)
	}
}
