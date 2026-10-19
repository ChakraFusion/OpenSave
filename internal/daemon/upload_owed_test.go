package daemon

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/opensave/opensave/internal/syncpause"
)

// A cloud copy is owed from the moment its snapshot is taken until it is up.
// One the process ended before sending — an installer stopping the app to
// update it, a crash, syncing paused until the app restarts — is sent at the
// next cloud check. It used to be kept only in memory, or not at all, and was
// then never sent.
//
// Pausing until restart holds the copy back without sending it; the restart is
// a fresh pause state, with nothing held in memory.
func TestAnUploadOwedWhenTheAppEndedIsSentAtTheNextCheck(t *testing.T) {
	cloudDir := filepath.Join(t.TempDir(), "cloud")
	if err := os.MkdirAll(cloudDir, 0o777); err != nil {
		t.Fatal(err)
	}
	dev := newCloudDevice(t, "Desktop", cloudDir)
	dev.d.PauseSync(0)
	snap := dev.play("taken just before the app was stopped")
	name := cloudSnapshotName(cloudGame, "main", snap.ID)
	if _, err := os.Stat(filepath.Join(cloudDir, name)); err == nil {
		t.Fatal("setup: the copy went up while syncing was paused")
	}

	// The app ends, and starts again.
	dev.d.heldMu.Lock()
	dev.d.held = nil
	dev.d.heldMu.Unlock()
	dev.d.P2P.Pause = syncpause.New()

	dev.check()
	if _, err := os.Stat(filepath.Join(cloudDir, name)); err != nil {
		t.Fatalf("the copy owed from before was never sent: %v", err)
	}
	if pending, _ := dev.d.Store.CloudRetries(); len(pending) != 0 {
		t.Errorf("still owed after it went up: %+v", pending)
	}
}

// Sent straight away, nothing stays owed.
func TestASentUploadIsNotLeftOwed(t *testing.T) {
	cloudDir := filepath.Join(t.TempDir(), "cloud")
	if err := os.MkdirAll(cloudDir, 0o777); err != nil {
		t.Fatal(err)
	}
	dev := newCloudDevice(t, "Desktop", cloudDir)
	snap := dev.play("progress")
	if _, err := os.Stat(filepath.Join(cloudDir, cloudSnapshotName(cloudGame, "main", snap.ID))); err != nil {
		t.Fatal(err)
	}
	if pending, _ := dev.d.Store.CloudRetries(); len(pending) != 0 {
		t.Errorf("owed after it went up: %+v", pending)
	}
}

// With no cloud set up, nothing is kept owed: no retry could ever send it.
func TestNoUploadIsOwedWithoutACloud(t *testing.T) {
	dev := newCloudDevice(t, "Desktop", filepath.Join(t.TempDir(), "cloud"))
	cfg, err := dev.d.Store.GetCloudConfig()
	if err != nil {
		t.Fatal(err)
	}
	cfg.URL = ""
	if err := dev.d.Store.UpdateCloudConfig(cfg); err != nil {
		t.Fatal(err)
	}
	dev.play("progress")
	if pending, _ := dev.d.Store.CloudRetries(); len(pending) != 0 {
		t.Errorf("owed with nowhere to send it: %+v", pending)
	}
}

// A copy on its way up is not sent a second time by a retry alongside it.
func TestARetryLeavesAnUploadUnderWayAlone(t *testing.T) {
	cloudDir := filepath.Join(t.TempDir(), "cloud")
	if err := os.MkdirAll(cloudDir, 0o777); err != nil {
		t.Fatal(err)
	}
	dev := newCloudDevice(t, "Desktop", cloudDir)
	dev.d.PauseSync(0)
	snap := dev.play("held")
	name := cloudSnapshotName(cloudGame, "main", snap.ID)
	dev.d.P2P.Pause = syncpause.New()

	if !dev.d.startSending(name) {
		t.Fatal("setup: already being sent")
	}
	dev.d.retryFailedUploads(nil)
	dev.d.uploads.Wait(0)
	if _, err := os.Stat(filepath.Join(cloudDir, name)); err == nil {
		t.Error("a retry sent a copy that was already on its way up")
	}
	dev.d.doneSending(name)
}
