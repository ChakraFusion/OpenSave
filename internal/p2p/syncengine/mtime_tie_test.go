package syncengine

import (
	"testing"

	"github.com/opensave/opensave/internal/delta"
)

func tieManifest(hash string, mtime delta.Milli) delta.Manifest {
	return delta.Manifest{Files: map[string]delta.FileEntry{
		"save.dat": {Hash: hash, MtimeMs: mtime, Size: 10},
	}}
}

// Two sides holding different content with equal timestamps used to resolve to
// "pull", unconditionally — so a local edit was replaced by the peer's older
// content with nothing said.
//
// Equal stamps are not exotic. They are what a filesystem with coarse
// timestamps gives you, and an SD card in a Steam Deck is exFAT. The engine
// reaches this code whenever DetectConflict says no, which is exactly the case
// where only one side has moved.
func TestAnMtimeTieGoesToTheSideThatActuallyChanged(t *testing.T) {
	const same = delta.Milli(1_700_000_000_000)
	lineage := map[string]struct{}{"save.dat": {}}

	// The peer still holds the agreed state; this device made the edit.
	local := tieManifest("LOCAL-EDIT", same)
	remote := tieManifest("BASE", same)
	base := remote.ManifestHash()

	if DetectConflict(local, remote, 0, base) {
		t.Fatal("this scenario is supposed to reach Compute, not raise a conflict")
	}
	d := ComputeWithBase(local, remote, lineage, nil, base)
	if len(d.FilesToPull) > 0 {
		t.Errorf("the local edit was pulled over: pull=%v — the peer has not moved, so "+
			"its copy is the old one", d.FilesToPull)
	}
	if len(d.FilesToPush) != 1 {
		t.Errorf("the local edit was not pushed: push=%v", d.FilesToPush)
	}
}

// The mirror image: this device is the one that has not moved, so the peer's
// content is the edit and must be pulled.
func TestAnMtimeTieGoesToThePeerWhenThePeerChanged(t *testing.T) {
	const same = delta.Milli(1_700_000_000_000)
	lineage := map[string]struct{}{"save.dat": {}}

	local := tieManifest("BASE", same)
	remote := tieManifest("REMOTE-EDIT", same)
	base := local.ManifestHash()

	d := ComputeWithBase(local, remote, lineage, nil, base)
	if len(d.FilesToPush) > 0 {
		t.Errorf("stale local content was pushed: push=%v", d.FilesToPush)
	}
	if len(d.FilesToPull) != 1 {
		t.Errorf("the peer's edit was not pulled: pull=%v", d.FilesToPull)
	}
}

// With no base recorded there is nothing to reason from, and the old
// behaviour stands: pull. Pinned so the fallback is a decision rather than an
// accident.
func TestAnMtimeTieWithNoBaseStillPulls(t *testing.T) {
	const same = delta.Milli(1_700_000_000_000)
	d := ComputeWithBase(
		tieManifest("A", same), tieManifest("B", same),
		map[string]struct{}{"save.dat": {}}, nil, "")
	if len(d.FilesToPull) != 1 {
		t.Errorf("with no base the tie should still pull; got pull=%v push=%v",
			d.FilesToPull, d.FilesToPush)
	}
}

// A base matching neither side means both moved. DetectConflict raises a
// conflict before Compute runs, but the tie-break must not claim to know an
// answer if it is ever reached another way.
func TestAnMtimeTieWithAnUnrelatedBaseStillPulls(t *testing.T) {
	const same = delta.Milli(1_700_000_000_000)
	d := ComputeWithBase(
		tieManifest("A", same), tieManifest("B", same),
		map[string]struct{}{"save.dat": {}}, nil, "a-hash-neither-side-holds")
	if len(d.FilesToPull) != 1 {
		t.Errorf("an unrelated base should fall back to pulling; got pull=%v push=%v",
			d.FilesToPull, d.FilesToPush)
	}
}

// An agreed common version beats the filesystem clock. This used to expect
// the newer mtime to win even when that side was exactly the agreed base.
// That overwrote the only real edit. The corrected precedence is explicit.
func TestAgreedBaseOverridesNewerMtime(t *testing.T) {
	const reference = delta.Milli(1_700_000_000_000)
	lineage := map[string]struct{}{"save.dat": {}}

	local := tieManifest("BASE", reference+50)
	remote := tieManifest("REMOTE-EDIT", reference)
	base := local.ManifestHash()
	if DetectConflict(local, remote, 0, base) {
		t.Fatal("one-sided update must reach Compute without a conflict")
	}
	d := ComputeWithBase(local, remote, lineage, nil, base)
	if len(d.FilesToPush) > 0 {
		t.Errorf("old agreed-base data must not be pushed: push=%v", d.FilesToPush)
	}
	if len(d.FilesToPull) != 1 || d.FilesToPull[0] != "save.dat" {
		t.Errorf("changed remote must be pulled despite older mtime: pull=%v push=%v",
			d.FilesToPull, d.FilesToPush)
	}
}
