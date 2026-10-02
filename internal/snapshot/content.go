package snapshot

import (
	"archive/zip"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/opensave/opensave/internal/delta"
	"github.com/opensave/opensave/internal/store"
)

// Snapshot content.
//
// A snapshot is named by what it holds: every file's name in the archive and
// the SHA-256 of its content, hashed together. The file hashes are the ones the
// sync compares (the hash cache both use), so the same save gives the same
// value on every device — which is how a device knows the snapshot another
// device took is one it already has, and keeps the save archived once.
//
// Before this, a save of a quarter-million files was archived again for every
// device it was compared with, and again on every new snapshot over there:
// gigabytes of identical archives for one 780 MB save.

// contentEntry is one file of a snapshot: its name in the archive and its hash.
type contentEntry struct{ name, hash string }

func contentKeyOf(entries []contentEntry) string {
	sort.Slice(entries, func(i, j int) bool { return entries[i].name < entries[j].name })
	h := sha256.New()
	for _, e := range entries {
		h.Write([]byte(e.name))
		h.Write([]byte{0})
		h.Write([]byte(e.hash))
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))
}

// archiveName is the name a captured file has in the archive.
func archiveName(c store.CapturedFile) string {
	if c.Root == "" {
		return c.Path
	}
	return RootPrefix + c.Root + "/" + c.Path
}

// ContentKey names what a snapshot holds, from its recorded files.
func ContentKey(files []store.CapturedFile) string {
	entries := make([]contentEntry, 0, len(files))
	for _, f := range files {
		entries = append(entries, contentEntry{archiveName(f), f.Hash})
	}
	return contentKeyOf(entries)
}

// ContentKeyOfArchive names what a snapshot holds by reading its archive: for
// a snapshot recorded without its file list (one taken for a sync with a peer).
func ContentKeyOfArchive(zipPath string) (string, error) {
	path, done, err := OpenArchive(zipPath)
	if err != nil {
		return "", err
	}
	defer done()
	r, err := zip.OpenReader(path)
	if err != nil {
		return "", err
	}
	defer r.Close()
	entries := make([]contentEntry, 0, len(r.File))
	for _, f := range r.File {
		if f.FileInfo().IsDir() || strings.HasSuffix(f.Name, "/") || strings.HasSuffix(f.Name, delta.TmpSuffix) {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return "", fmt.Errorf("%s: %w", f.Name, err)
		}
		h := sha256.New()
		_, err = io.Copy(h, rc)
		rc.Close()
		if err != nil {
			return "", fmt.Errorf("%s: %w", f.Name, err)
		}
		entries = append(entries, contentEntry{f.Name, hex.EncodeToString(h.Sum(nil))})
	}
	return contentKeyOf(entries), nil
}

// contentHashOf works out what a snapshot holds, from its file list when it
// has one and from its archive otherwise.
func (m *Manager) contentHashOf(snap store.Snapshot) (string, error) {
	if files, err := m.Store.SnapshotFiles(snap.ID); err == nil && len(files) > 0 {
		return ContentKey(files), nil
	}
	return ContentKeyOfArchive(snap.ZipPath)
}

// isNewest reports whether id is the newest snapshot on a game's branch.
func (m *Manager) isNewest(gameID, branch, id string) bool {
	snaps, err := m.Store.ListSnapshots(gameID, branch)
	return err == nil && len(snaps) > 0 && snaps[0].ID == id
}

// MergeDuplicates names every snapshot of a game by its content and keeps each
// content archived once per branch: of snapshots holding the same files, one
// keeps its archive — a pinned one, else one taken by hand, else the newest,
// which retention keeps longest —
// and the others are removed, their ids kept as aliases of it so a device that
// knows a snapshot by one of them still finds it. Pinned snapshots are never
// removed. Returns how many were merged and the bytes freed.
func (m *Manager) MergeDuplicates(gameID string) (merged int, freed int64, err error) {
	missing, err := m.Store.SnapshotsWithoutContentHash(gameID)
	if err != nil {
		return 0, 0, err
	}
	for _, snap := range missing {
		if !ArchiveExists(snap.ZipPath) {
			continue
		}
		key, err := m.contentHashOf(snap)
		if err != nil {
			if m.Log != nil {
				m.Log("warn", fmt.Sprintf("could not read snapshot %s to name its content: %v", snap.ID, err))
			}
			continue
		}
		_ = m.Store.SetSnapshotContentHash(snap.ID, key)
	}

	snaps, err := m.Store.AllSnapshotsOfGame(gameID)
	if err != nil {
		return 0, 0, err
	}
	groups := map[string][]store.Snapshot{}
	for _, s := range snaps {
		if s.ContentHash == "" {
			continue
		}
		k := s.BranchName + "\x00" + s.ContentHash
		groups[k] = append(groups[k], s)
	}
	for _, group := range groups {
		if len(group) < 2 {
			continue
		}
		keep := group[len(group)-1] // newest (oldest first)
		for _, s := range group {
			if s.Pinned && !keep.Pinned || s.Pinned == keep.Pinned && !s.IsSystemAuto && keep.IsSystemAuto {
				keep = s
			}
		}
		for _, s := range group {
			if s.ID == keep.ID || s.Pinned {
				continue
			}
			_ = m.Store.RepointSnapshotAliases(s.ID, keep.ID)
			f, err := m.DeleteSnapshot(gameID, s.ID)
			if err != nil {
				continue
			}
			_ = m.Store.AddSnapshotAlias(s.ID, keep.ID)
			merged++
			freed += f
			if m.Log != nil {
				m.Log("info", fmt.Sprintf("snapshot %s held the same files as %s; kept once", s.ID, keep.ID))
			}
		}
	}
	return merged, freed, nil
}
