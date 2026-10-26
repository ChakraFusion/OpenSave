package cloud

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/opensave/opensave/internal/store"
)

// markedDir is a folder chosen as the backup folder, as Cloud Backup's
// settings make one (MarkLocalFolder).
func markedDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := MarkLocalFolder(dir); err != nil {
		t.Fatal(err)
	}
	return dir
}

// A network share that is not connected leaves an empty mount point behind.
// Backups were written into it, onto this machine's own disk, and counted as
// sent; its listing read as a backup with nothing in it (GitHub #46). Neither
// happens to a folder that was never chosen as the backup folder.
func TestBackupFolder_AnUnconnectedShareIsNotWrittenTo(t *testing.T) {
	mountPoint := t.TempDir() // the share's folder, with the share not there
	p := localFolder{cfg: store.CloudConfig{URL: mountPoint}}

	err := p.put(strings.NewReader("archive"), "game__main__snap_1.zip")
	var unavailable ErrFolderUnavailable
	if !errors.As(err, &unavailable) {
		t.Fatalf("put into an unconnected share = %v, want the folder reported unavailable", err)
	}
	if entries, _ := os.ReadDir(mountPoint); len(entries) != 0 {
		t.Errorf("something was written into the mount point: %v", entries)
	}
	if files, err := p.list(); err == nil {
		t.Errorf("an unconnected share listed as a backup holding %v", files)
	}
}

// A backup folder that is gone is not made again by a backup: that is a
// folder on this machine standing in for one on a drive that is not there.
func TestBackupFolder_AMissingFolderIsNotCreated(t *testing.T) {
	gone := filepath.Join(t.TempDir(), "NAS", "backups")
	p := localFolder{cfg: store.CloudConfig{URL: gone}}
	if err := p.put(strings.NewReader("archive"), "game__main__snap_1.zip"); err == nil {
		t.Fatal("a backup into a missing folder succeeded")
	}
	if _, err := os.Stat(gone); err == nil {
		t.Error("the missing backup folder was created by a backup")
	}
}

// Choosing a folder makes it the backup folder, creating it if need be; a
// share connected again is found by its marker.
func TestBackupFolder_ChosenFolderTakesBackups(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "new backups")
	if err := MarkLocalFolder(dir); err != nil {
		t.Fatal(err)
	}
	p := localFolder{cfg: store.CloudConfig{URL: dir}}
	if err := p.put(strings.NewReader("archive"), "game__main__snap_1.zip"); err != nil {
		t.Fatalf("put into the chosen folder: %v", err)
	}
	files, err := p.list()
	if err != nil || len(files) != 1 || files[0].Name != "game__main__snap_1.zip" {
		t.Fatalf("list = %+v, %v; want the one backup, and not the marker", files, err)
	}
}

// A folder chosen before the marker existed, already holding OpenSave's
// backups, keeps working: it is marked the first time it is used.
func TestBackupFolder_AFolderWithBackupsFromBeforeIsAdopted(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "game__main__snap_1.zip"), []byte("old backup"), 0o666); err != nil {
		t.Fatal(err)
	}
	p := localFolder{cfg: store.CloudConfig{URL: dir}}
	if err := p.put(strings.NewReader("archive"), "game__main__snap_2.zip"); err != nil {
		t.Fatalf("put into a folder of earlier backups: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, folderMarker)); err != nil {
		t.Errorf("the folder was not marked: %v", err)
	}
}
