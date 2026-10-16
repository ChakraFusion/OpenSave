package snapshot

import (
	"archive/zip"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"runtime"
	"strings"
)

// CheckExtractable reports whether an archive can be put back on disk whole,
// before anything is cleared to make room for it.
//
// VerifyArchive proves every file reads back against its checksum, which says
// nothing about whether the files can all exist at once: an archive holding
// both a file "block" and a file "block/child.sav" passes it, and fails only
// part-way through extracting, after the save folder has been emptied
// (GitHub #60). The same goes for an entry that would land outside its folder,
// which extraction refuses. So the layout is checked here, as extraction would
// lay it out, and a restore or branch switch stops before touching the save.
//
// A file listed twice is refused too, also when the two names differ only in
// case on a filesystem that ignores it. Extraction writes files in parallel,
// so the two entries would be two writers on one file at once, and what is
// left on disk could be neither of them.
func CheckExtractable(zipPath string) error {
	r, err := zip.OpenReader(zipPath)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("%w: its archive is missing (%s)", ErrDamaged, zipPath)
		}
		return fmt.Errorf("%w: its archive cannot be opened: %v", ErrDamaged, err)
	}
	defer r.Close()

	files := map[string]string{} // place → entry, for files
	dirs := map[string]string{}  // place → entry, for folders, stated or implied
	for _, f := range r.File {
		root, rel := "", f.Name
		if name, ok := rootOfEntry(f.Name); ok {
			root = name
			rel = strings.TrimPrefix(f.Name, RootPrefix+name+"/")
			if rel == "" {
				continue // the location's own folder marker
			}
		}
		clean := filepath.Clean(filepath.FromSlash(rel))
		if strings.HasPrefix(clean, "..") || filepath.IsAbs(clean) {
			return fmt.Errorf("%w: %s would be written outside its folder", ErrDamaged, f.Name)
		}
		place := placeKey(root, clean)
		if f.FileInfo().IsDir() {
			dirs[place] = f.Name
		} else {
			if other, ok := dirs[place]; ok {
				return fmt.Errorf("%w: %s is a file, but %s needs a folder of that name", ErrDamaged, f.Name, other)
			}
			if other, ok := files[place]; ok {
				return fmt.Errorf("%w: %s and %s are the same file", ErrDamaged, other, f.Name)
			}
			files[place] = f.Name
		}
		// Every folder above the entry has to be a folder.
		for parent := filepath.Dir(clean); parent != "." && parent != string(filepath.Separator); parent = filepath.Dir(parent) {
			key := placeKey(root, parent)
			if other, ok := files[key]; ok {
				return fmt.Errorf("%w: %s needs a folder where %s is a file", ErrDamaged, f.Name, other)
			}
			dirs[key] = f.Name
		}
	}
	return nil
}

// placeKey names where an entry lands: its location and its path there,
// compared as this device's filesystem compares names.
func placeKey(root, rel string) string {
	if runtime.GOOS == "windows" || runtime.GOOS == "darwin" {
		rel = strings.ToLower(rel)
	}
	return root + "\x00" + rel
}

// checkRestorable is everything a restore needs to know before it clears a
// save: the archive reads back whole (VerifyArchive) and can be laid out on
// disk (CheckExtractable).
func checkRestorable(zipPath string) error {
	if err := VerifyArchive(zipPath); err != nil {
		return err
	}
	return CheckExtractable(zipPath)
}
