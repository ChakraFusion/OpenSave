package syncengine

import "github.com/opensave/opensave/internal/delta"

// A sync is held for a decision when it would delete more than both of these:
// enough files that it is not a game tidying its slots, and enough of the save
// that it is not one corner of a big one. Ordinary play deletes a handful of
// files; a device holding part of a save being read as the other's deletions
// deletes thousands.
const (
	massDeleteMinFiles = 100
	massDeleteFraction = 0.10
)

// massDeletion reports how many files the decision would delete, on either
// side, and how many the save holds, when that crosses the thresholds above;
// zero otherwise.
func massDeletion(local, remote delta.Manifest, d Decision) (deleting, total int) {
	n := len(d.FilesToDeleteLocally) + len(d.FilesToDeleteOnPeer)
	total = len(local.Files)
	if len(remote.Files) > total {
		total = len(remote.Files)
	}
	if n <= massDeleteMinFiles || total == 0 || float64(n) <= massDeleteFraction*float64(total) {
		return 0, total
	}
	return n, total
}
