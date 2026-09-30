package syncengine

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

// denyListing makes dir a folder that is there, with its files, but cannot
// be listed — its permissions refuse it. Undone when the test ends.
func denyListing(t *testing.T, dir string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		user := os.Getenv("USERNAME")
		if out, err := exec.Command("icacls", dir, "/deny", user+":(RD)").CombinedOutput(); err != nil {
			t.Skipf("cannot deny listing a folder here: %v %s", err, out)
		}
		t.Cleanup(func() { _ = exec.Command("icacls", dir, "/remove:d", user).Run() })
	} else {
		if err := os.Chmod(dir, 0o300); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.Chmod(dir, 0o777) })
	}
	if _, err := os.ReadDir(dir); err == nil {
		t.Skip("listing the folder was not refused (running as root?)")
	}
}

// A save folder that cannot be read is not an empty one. It read as one: an
// empty manifest, and no error. So a device still holding its save held it
// back as emptied and stopped syncing it — found as a device refusing to sync
// a game whose folder had a file in it the whole time — and a sync of it told
// the other device every file had been deleted there.
func TestAnUnreadableSaveFolderIsNotAnEmptyOne(t *testing.T) {
	env := setupEngine(t)
	write(t, env.localDir, "a.sav", "mine")
	write(t, env.remoteDir, "a.sav", "mine")
	if err := env.store.SetSyncState("game1", env.peer.ID, []string{"a.sav"}, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := env.engine.Snapshots.Create("game1", "", true); err != nil {
		t.Fatal(err)
	}
	denyListing(t, env.localDir)

	if held, _ := env.engine.CheckHold("game1", false); held {
		t.Error("a save that is still there was held back as emptied")
	}
	if _, has, _ := env.store.GetDeletionHold("game1"); has {
		t.Error("a hold was recorded for it")
	}

	if _, err := env.engine.SyncWithPeer(context.Background(), "game1", env.peer); err == nil {
		t.Error("a sync of a folder that could not be read went ahead")
	}
	if _, err := os.Stat(filepath.Join(env.remoteDir, "a.sav")); err != nil {
		t.Errorf("the other device's copy was deleted: %v", err)
	}
}
