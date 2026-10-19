package e2e

import (
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/opensave/opensave/testutil"
)

// The cases sync without save versions still has to infer: more than two
// devices, clocks that disagree, and two devices with no agreed state yet.
// Each asks one thing — is a newer save ever lost without anyone being asked?
// Either the newest save ends up everywhere, or a device shows a conflict and
// the newest is still in a save folder. A prompt that versions would have
// avoided is logged ("PROMPT:"), not failed: noise, not loss.

func pauseSync(t *testing.T, d *testutil.TestDaemon) {
	t.Helper()
	var st struct {
		Paused bool `json:"paused"`
	}
	d.API(http.MethodPost, "/api/sync/pause", map[string]any{"minutes": 60}, &st)
	if !st.Paused {
		t.Fatalf("%s did not pause", d.Name())
	}
}

func resumeSync(d *testutil.TestDaemon) {
	d.API(http.MethodPost, "/api/sync/resume", map[string]any{}, nil)
}

func stamp(t *testing.T, d *testutil.TestDaemon, rel string, at time.Time) {
	t.Helper()
	if err := os.Chtimes(filepath.Join(d.SaveDir, filepath.FromSlash(rel)), at, at); err != nil {
		t.Fatal(err)
	}
}

// trio pairs three LAN daemons with each other and has all three track one
// game holding rel=content, synced and quiet.
func trio(t *testing.T, name, rel, content string) (a, b, c *testutil.TestDaemon, gameID string) {
	t.Helper()
	if testing.Short() {
		t.Skip("three-daemon test; skipped under -short")
	}
	a = testutil.NewTestDaemon(t, name+"-A")
	b = testutil.NewTestDaemon(t, name+"-B")
	c = testutil.NewTestDaemon(t, name+"-C")
	a.PairWith(b)
	a.PairWith(c)
	b.PairWith(c)

	a.WriteSave(rel, content)
	restore := suppressSyncOnTrack(a, b, c)
	gameID = a.TrackGame(name)
	for _, d := range []*testutil.TestDaemon{b, c} {
		if id := d.TrackGame(name); id != gameID {
			t.Fatalf("%s tracked %q as %s, A as %s", d.Name(), name, id, gameID)
		}
	}
	restore()
	syncTo(a, gameID, b.NodeID())
	syncTo(a, gameID, c.NodeID())
	if !testutil.WaitFor(60*time.Second, func() bool {
		return b.ReadSave(rel) == content && c.ReadSave(rel) == content
	}) {
		t.Fatalf("setup: %s never reached both peers", rel)
	}
	time.Sleep(syncSettleWindow)
	settledOnBoth(t, gameID, a, b, c)
	return a, b, c, gameID
}

// outcome waits for the devices to agree on rel or for one of them to ask,
// then judges it: every save in want must still be in some device's folder
// unless everyone holds the one in want[0], the newest. want[1:] are saves
// that are fine to replace only through a prompt (a concurrent edit).
func outcome(t *testing.T, gameID, rel string, want []string, ds ...*testutil.TestDaemon) {
	t.Helper()
	asking := func() []string {
		var names []string
		for _, d := range ds {
			if hasConflict(d, gameID) {
				names = append(names, d.Name())
			}
		}
		return names
	}
	allHold := func(v string) bool {
		for _, d := range ds {
			if d.ReadSave(rel) != v {
				return false
			}
		}
		return true
	}
	held := func() map[string]string {
		m := map[string]string{}
		for _, d := range ds {
			m[d.Name()] = d.ReadSave(rel)
		}
		return m
	}
	testutil.WaitFor(90*time.Second, func() bool {
		if len(asking()) > 0 {
			return true
		}
		for _, d := range ds {
			if d.Daemon.P2P.Sync.SyncBusy(gameID) {
				return false
			}
		}
		return allHold(ds[0].ReadSave(rel))
	})
	time.Sleep(syncSettleWindow)
	settledOnBoth(t, gameID, ds...)

	if who := asking(); len(who) > 0 {
		t.Logf("PROMPT: %v asked; saves %v", who, held())
		for _, v := range want {
			found := false
			for _, d := range ds {
				found = found || d.ReadSave(rel) == v
			}
			if !found {
				t.Errorf("%q is in no save folder although only %v were asked; saves %v", v, who, held())
			}
		}
		return
	}
	if !allHold(want[0]) {
		t.Errorf("nobody was asked and the newest save %q is not everywhere: %v", want[0], held())
	}
	if len(want) > 1 {
		t.Errorf("concurrent edits %v were settled without asking anyone: %v", want, held())
	}
}

// --- three devices -----------------------------------------------------------

// A save travels A→B→C and C then meets A directly. A's base with C is still
// v1, so to A both sides "changed": v2 here, v3 there. v3 was made from v2,
// which only versions (or B's word) can tell.
func TestGaps_ThreeDevices_AChainEndsWithTheNewest(t *testing.T) {
	a, b, c, gameID := trio(t, "GapChain", "slot1.sav", "v1")

	pauseSync(t, c)
	a.WriteSave("slot1.sav", "v2")
	if !testutil.WaitFor(60*time.Second, func() bool { return b.ReadSave("slot1.sav") == "v2" }) {
		t.Fatalf("v2 never reached B: %q", b.ReadSave("slot1.sav"))
	}
	time.Sleep(syncSettleWindow)
	settledOnBoth(t, gameID, a, b)

	pauseSync(t, a)
	b.WriteSave("slot1.sav", "v3")
	resumeSync(c)
	if !testutil.WaitFor(60*time.Second, func() bool { return c.ReadSave("slot1.sav") == "v3" }) {
		t.Fatalf("v3 never reached C: %q", c.ReadSave("slot1.sav"))
	}
	time.Sleep(syncSettleWindow)
	settledOnBoth(t, gameID, b, c)

	// A meets only C.
	pauseSync(t, b)
	resumeSync(a)
	syncTo(a, gameID, c.NodeID())
	outcome(t, gameID, "slot1.sav", []string{"v3"}, a, c)

	resumeSync(b)
	outcome(t, gameID, "slot1.sav", []string{"v3"}, a, b, c)
}

// A device away while the others go through several saves comes back holding
// the save they all started from, and its file was touched in between (the
// game rewrote it unchanged, or its clock runs ahead). It must take the
// newest and give nothing back.
func TestGaps_ThreeDevices_ALongAbsentDeviceCatchesUp(t *testing.T) {
	a, b, c, gameID := trio(t, "GapAbsent", "slot1.sav", "v1")

	pauseSync(t, c)
	for i, w := range []*testutil.TestDaemon{a, b, a, b} {
		v := testutil.Fmt("v%d", i+2)
		w.WriteSave("slot1.sav", v)
		other := b
		if w == b {
			other = a
		}
		if !testutil.WaitFor(60*time.Second, func() bool { return other.ReadSave("slot1.sav") == v }) {
			t.Fatalf("%s never reached %s: %q", v, other.Name(), other.ReadSave("slot1.sav"))
		}
		time.Sleep(syncSettleWindow)
		settledOnBoth(t, gameID, a, b)
	}
	stamp(t, c, "slot1.sav", time.Now().Add(time.Hour))
	time.Sleep(syncSettleWindow)

	resumeSync(c)
	outcome(t, gameID, "slot1.sav", []string{"v5"}, a, b, c)
}

// Two devices edit while away from everyone; the third changed nothing. Each
// reaches the third in turn. Whichever arrives second meets a save on it that
// is not its own base — a concurrent edit, which must be asked about rather
// than one of them dropped.
func TestGaps_ThreeDevices_EditsMadeApartAreAsked(t *testing.T) {
	a, b, c, gameID := trio(t, "GapApart", "slot1.sav", "v1")

	pauseSync(t, b)
	pauseSync(t, c)
	b.WriteSave("slot1.sav", "b-run")
	c.WriteSave("slot1.sav", "c-run")
	time.Sleep(syncSettleWindow)

	resumeSync(b)
	if !testutil.WaitFor(60*time.Second, func() bool { return a.ReadSave("slot1.sav") == "b-run" }) {
		t.Fatalf("B's edit never reached the untouched A: %q", a.ReadSave("slot1.sav"))
	}
	time.Sleep(syncSettleWindow)
	settledOnBoth(t, gameID, a, b)

	resumeSync(c)
	outcome(t, gameID, "slot1.sav", []string{"c-run", "b-run"}, a, b, c)
}

// --- clocks ------------------------------------------------------------------

// Three devices, and the one that made the newest save has a clock running
// half an hour behind, so its save looks older than the one it replaced.
func TestGaps_Clock_ASlowClockOnTheNewestDevice(t *testing.T) {
	a, b, c, gameID := trio(t, "GapSlow", "slot1.sav", "v1")

	pauseSync(t, c)
	a.WriteSave("slot1.sav", "v2")
	stamp(t, a, "slot1.sav", time.Now().Add(-30*time.Minute))
	if !testutil.WaitFor(60*time.Second, func() bool { return b.ReadSave("slot1.sav") == "v2" }) {
		t.Fatalf("v2 never reached B: %q", b.ReadSave("slot1.sav"))
	}
	time.Sleep(syncSettleWindow)
	settledOnBoth(t, gameID, a, b)

	// C's clock is right, and touching its unchanged save stamps it now.
	stamp(t, c, "slot1.sav", time.Now())
	resumeSync(c)
	outcome(t, gameID, "slot1.sav", []string{"v2"}, a, b, c)
}

// --- no agreed state ---------------------------------------------------------

// freshPair pairs two daemons, puts a's and b's saves in place (b's stamped
// at bAt), and has both track the game with nothing synced yet: no agreed
// state between them. b syncs first.
func freshPair(t *testing.T, name string, aSaves []string, bSave string, bAt time.Time) (a, b *testutil.TestDaemon, gameID string) {
	t.Helper()
	a = testutil.NewTestDaemon(t, name+"-A")
	b = testutil.NewTestDaemon(t, name+"-B")
	a.PairWith(b)
	restore := suppressSyncOnTrack(a, b)
	for i, s := range aSaves {
		a.WriteSave("slot1.sav", s)
		if i == 0 {
			gameID = a.TrackGame(name)
		}
		a.Daemon.WaitForTracking()
		time.Sleep(syncSettleWindow) // each its own snapshot: A's history
	}
	b.WriteSave("slot1.sav", bSave)
	stamp(t, b, "slot1.sav", bAt)
	if id := b.TrackGame(name); id != gameID {
		t.Fatalf("B tracked %q as %s, A as %s", name, id, gameID)
	}
	restore()
	time.Sleep(syncSettleWindow)
	syncTo(b, gameID, a.NodeID())
	return a, b, gameID
}

// A new device joins holding an old copy of the save (from a backup, an old
// install, a copied folder) stamped today, while the other device has moved
// on since. The old copy is one A has in its own history.
func TestGaps_NoAgreedState_AnOldCopyStampedToday(t *testing.T) {
	a, b, gameID := freshPair(t, "GapOldCopy", []string{"v1", "v2"}, "v1", time.Now().Add(time.Minute))
	outcome(t, gameID, "slot1.sav", []string{"v2"}, a, b)
}

// Two devices with unrelated saves and no shared history, the later one made
// on a device whose clock runs behind. Nothing can order these; the user must
// be asked or the save made later must win.
func TestGaps_NoAgreedState_UnrelatedSavesWithASlowClock(t *testing.T) {
	a, b, gameID := freshPair(t, "GapUnrelated", []string{"a-run"}, "b-run", time.Now().Add(-30*time.Minute))
	outcome(t, gameID, "slot1.sav", []string{"b-run"}, a, b)
}
