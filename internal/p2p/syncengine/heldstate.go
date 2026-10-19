package syncengine

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"sort"

	"github.com/opensave/opensave/internal/delta"
)

// Two devices meeting with no agreed state yet, both holding a save, are
// asked which to keep: nothing says which is newer. Unless one of them has
// held the other's save before. A device joining with an old copy — from a
// backup, an earlier install, a folder copied across — holds a state the
// other device has in its own history, and then there is nothing to ask: a
// state both have held is a merge base by definition, the same proof the push
// and served records give (served.go). Banked, the sync judges the two
// against it as always, and the old copy is simply behind.
//
// Each side can only look in its own history. The device holding the newer
// save finds the peer's state there on its own sync; the device holding the
// old copy asks the peer, on a second manifest request carrying the hash of
// its own files (ManifestQuery.Held), and the peer answers HeldYours. Usually
// the old copy's device syncs first: tracking the game is what starts it.
//
// Compared on files alone, as snapshots record them, after each side's own
// exclusion rules: two devices with different rules simply find no match, and
// are asked as before. So is a peer that predates the question.

// heldFilesHash identifies a save by its files alone: paths and content
// hashes, no folders, as a snapshot records it.
func heldFilesHash(files map[string]string) string {
	paths := make([]string, 0, len(files))
	for p := range files {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	h := sha256.New()
	for _, p := range paths {
		io.WriteString(h, p)
		io.WriteString(h, ":")
		io.WriteString(h, files[p])
		io.WriteString(h, "\n")
	}
	return hex.EncodeToString(h.Sum(nil))
}

func manifestFilesHash(m delta.Manifest) string {
	files := make(map[string]string, len(m.Files))
	for p, f := range m.Files {
		files[p] = f.Hash
	}
	return heldFilesHash(files)
}

// HeldState reports whether a snapshot of this game here holds exactly the
// files filesHash names, as this device's exclusion rules see them. Answers a
// peer's ManifestQuery.Held.
func (e *Engine) HeldState(gameID, filesHash string) bool {
	if filesHash == "" {
		return false
	}
	sets, err := e.Store.SnapshotFileSets(gameID, delta.PrimaryRoot)
	if err != nil {
		return false
	}
	rules := e.rulesFor(gameID)
	for _, files := range sets {
		m := delta.Manifest{Files: make(map[string]delta.FileEntry, len(files))}
		for p, h := range files {
			m.Files[p] = delta.FileEntry{Hash: h}
		}
		if manifestFilesHash(filterManifest(m, rules)) == filesHash {
			return true
		}
	}
	return false
}

// heldBase is the merge base two devices with no agreed state have after all:
// the peer's save when this device has held it, this device's when the peer
// has. "" when neither has held the other's, or either holds nothing.
func (e *Engine) heldBase(ctx context.Context, gameID string, peer Peer, q ManifestQuery, local, remote delta.Manifest) string {
	if len(local.Files) == 0 || len(remote.Files) == 0 || sameFiles(local, remote) {
		return ""
	}
	if e.HeldState(gameID, manifestFilesHash(remote)) {
		return remote.ManifestHash()
	}
	q.Held = manifestFilesHash(local)
	again, err := e.Transport.FetchManifest(ctx, peer, gameID, q)
	if err == nil && again.HeldYours {
		return local.ManifestHash()
	}
	return ""
}
