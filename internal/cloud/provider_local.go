package cloud

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/opensave/opensave/internal/store"
)

// localFolder keeps snapshots in a folder on this machine or a mounted share
// (a NAS, a synced folder another program uploads).
type localFolder struct {
	cfg store.CloudConfig
}

// folderMarker is the file that says a folder is OpenSave's backup folder.
// It is written when the folder is chosen (MarkLocalFolder), and a folder
// without it is not written to or read as a backup.
//
// A network share that is not connected leaves its mount point behind: an
// empty folder, or none. Backups were written into it, onto this machine's
// own disk, and counted as sent, while the share never got them; and its
// listing read as a backup with nothing in it. The marker is on the share,
// so it is missing from the mount point.
const folderMarker = ".opensave-backup-folder"

// MarkLocalFolder makes dir OpenSave's backup folder: created if need be,
// and marked. It runs when the folder is chosen, which is when it is known to
// be the one meant.
func MarkLocalFolder(dir string) error {
	dir = strings.TrimSpace(dir)
	if dir == "" {
		return nil
	}
	if err := os.MkdirAll(dir, 0o777); err != nil {
		return fmt.Errorf("could not use %q as the backup folder: %w", dir, err)
	}
	return writeMarker(dir)
}

func writeMarker(dir string) error {
	path := filepath.Join(dir, folderMarker)
	if _, err := os.Stat(path); err == nil {
		return nil
	}
	body := "This folder holds OpenSave's backups. OpenSave looks for this file " +
		"to know the folder is there: please leave it in place."
	if err := os.WriteFile(path, []byte(body), 0o666); err != nil {
		return fmt.Errorf("could not use %q as the backup folder: %w", dir, err)
	}
	return nil
}

// ErrFolderUnavailable is a backup folder that is not there to be used: not
// connected, moved, or never chosen as one.
type ErrFolderUnavailable struct{ Dir string }

func (e ErrFolderUnavailable) Error() string {
	return fmt.Sprintf("the backup folder %q is not available — if it is on a network share or a removable drive, "+
		"check that it is connected; if it was moved, choose it again in Cloud Backup", e.Dir)
}

// ready says whether the configured folder is the backup folder now: there,
// and marked. A folder already holding OpenSave's backups, chosen before the
// marker existed, is marked now rather than refused.
func (p localFolder) ready() error {
	dir := p.cfg.URL
	if info, err := os.Stat(dir); err != nil || !info.IsDir() {
		return ErrFolderUnavailable{Dir: dir}
	}
	if _, err := os.Stat(filepath.Join(dir, folderMarker)); err == nil {
		return nil
	}
	if holdsBackups(dir) {
		return writeMarker(dir)
	}
	return ErrFolderUnavailable{Dir: dir}
}

// holdsBackups says whether dir holds a file named as OpenSave names what it
// stores: <game>__<branch>__<snapshot>.zip, <game>__head-<device>-<ms>.zip.
func holdsBackups(dir string) bool {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	for _, e := range entries {
		if e.Type().IsRegular() && strings.HasSuffix(e.Name(), ".zip") && strings.Contains(e.Name(), "__") {
			return true
		}
	}
	return false
}

func (p localFolder) upload(f *os.File, size int64, fileName string) error {
	return p.put(f, fileName)
}

// put stores what r holds under fileName, appearing under that name only
// once it is all there (see writeFileWhole). Another device reading the same
// folder never lists a snapshot that is still being copied, and a copy that
// fails leaves nothing that looks like one.
func (p localFolder) put(r io.Reader, fileName string) error {
	if p.cfg.URL == "" {
		return fmt.Errorf("no local folder destination configured")
	}
	if err := p.ready(); err != nil {
		return err
	}
	return writeFileWhole(filepath.Join(p.cfg.URL, fileName), func(w io.Writer) error {
		_, err := io.Copy(w, r)
		return err
	})
}

func (p localFolder) list() ([]CloudFile, error) {
	if p.cfg.URL == "" {
		return []CloudFile{}, nil
	}
	if err := p.ready(); err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(p.cfg.URL)
	if err != nil {
		return nil, fmt.Errorf("read local folder destination %q: %w", p.cfg.URL, err)
	}
	var files []CloudFile
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".zip") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		files = append(files, CloudFile{
			Name: e.Name(), SizeBytes: info.Size(),
			CreatedTime: info.ModTime().UTC().Format(time.RFC3339),
		})
	}
	return files, nil
}

func (p localFolder) download(fileName, localPath string) error {
	if p.cfg.URL == "" {
		return fmt.Errorf("no local folder destination configured")
	}
	src, err := os.Open(filepath.Join(p.cfg.URL, fileName))
	if err != nil {
		return fmt.Errorf("file %q not found in local folder", fileName)
	}
	defer src.Close()
	return writeFileWhole(localPath, func(w io.Writer) error {
		_, err := io.Copy(w, src)
		return err
	})
}

func (p localFolder) remove(f CloudFile) error {
	if p.cfg.URL == "" {
		return fmt.Errorf("no local folder destination configured")
	}
	return os.Remove(filepath.Join(p.cfg.URL, f.Name))
}
