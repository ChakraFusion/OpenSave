package cloud

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/opensave/opensave/internal/store"
)

func TestLocalFolderListPropagatesDirectoryErrors(t *testing.T) {
	t.Run("absent directory", func(t *testing.T) {
		missing := filepath.Join(t.TempDir(), "missing-share")
		files, err := (localFolder{cfg: store.CloudConfig{URL: missing}}).list()
		if err == nil {
			t.Fatalf("list = %+v, nil error; want missing-directory error", files)
		}
	})

	t.Run("configured path is not a directory", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "not-a-directory")
		if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
		files, err := (localFolder{cfg: store.CloudConfig{URL: path}}).list()
		if err == nil {
			t.Fatalf("list = %+v, nil error; want directory-read error", files)
		}
	})
}

func TestLocalFolderListStillSupportsOrdinaryFolders(t *testing.T) {
	dir := t.TempDir()
	want := []byte("synthetic zip")
	if err := os.WriteFile(filepath.Join(dir, "game__main__snap_1.zip"), want, 0o600); err != nil {
		t.Fatal(err)
	}
	files, err := (localFolder{cfg: store.CloudConfig{URL: dir}}).list()
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(files) != 1 || files[0].Name != "game__main__snap_1.zip" || files[0].SizeBytes != int64(len(want)) {
		t.Fatalf("list = %+v", files)
	}
}
