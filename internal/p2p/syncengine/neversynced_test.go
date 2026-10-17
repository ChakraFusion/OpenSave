package syncengine

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/opensave/opensave/internal/delta"
)

// Steam's remotecache.vdf is each device's own bookkeeping: never taken from
// a peer, never pushed, never deleted because the peer lacks it.
func TestNeverSynced_LeftAloneOnBothSides(t *testing.T) {
	env := setupEngine(t)
	write(t, env.localDir, "save.dat", "progress")
	write(t, env.localDir, "remotecache.vdf", "this device's steam")
	write(t, env.remoteDir, "save.dat", "progress")
	write(t, env.remoteDir, "remotecache.vdf", "the other device's steam")

	res, err := env.engine.SyncWithPeer(context.Background(), "game1", env.peer)
	if err != nil {
		t.Fatalf("SyncWithPeer error = %v", err)
	}
	if res.Status != "in_sync" {
		t.Fatalf("status = %q, want in_sync: the saves are the same", res.Status)
	}
	got, _ := os.ReadFile(filepath.Join(env.localDir, "remotecache.vdf"))
	if string(got) != "this device's steam" {
		t.Errorf("this device's remotecache.vdf became %q", got)
	}
	if got, _ := os.ReadFile(filepath.Join(env.remoteDir, "remotecache.vdf")); string(got) != "the other device's steam" {
		t.Errorf("the peer's remotecache.vdf became %q", got)
	}
}

// The served manifest keeps listing it: a device on an older build would read
// the gap as the file deleted, and delete its own.
func TestNeverSynced_StillServed(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "remotecache.vdf", "steam")
	m, err := delta.BuildManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := m.Files["remotecache.vdf"]; !ok {
		t.Fatal("the manifest a device serves left remotecache.vdf out")
	}
}

// Held only by the peer: not taken. Held only here: not pushed, and not
// deleted because the peer lacks it.
func TestNeverSynced_OneSidedIsNotADifference(t *testing.T) {
	env := setupEngine(t)
	write(t, env.localDir, "save.dat", "progress")
	write(t, env.remoteDir, "save.dat", "progress")
	write(t, env.remoteDir, "RemoteCache.VDF", "only there")

	res, err := env.engine.SyncWithPeer(context.Background(), "game1", env.peer)
	if err != nil || res.Status != "in_sync" {
		t.Fatalf("status = %q, %v; want in_sync", res.Status, err)
	}
	if _, err := os.Stat(filepath.Join(env.localDir, "RemoteCache.VDF")); err == nil {
		t.Error("the peer's remotecache.vdf was pulled")
	}

	os.Remove(filepath.Join(env.remoteDir, "RemoteCache.VDF"))
	write(t, env.localDir, "remotecache.vdf", "only here")
	delta.InvalidateRoot(env.localDir)
	res, err = env.engine.SyncWithPeer(context.Background(), "game1", env.peer)
	if err != nil || res.Status != "in_sync" {
		t.Fatalf("status = %q, %v; want in_sync", res.Status, err)
	}
	if _, err := os.Stat(filepath.Join(env.remoteDir, "remotecache.vdf")); err == nil {
		t.Error("this device's remotecache.vdf was pushed")
	}
	if _, err := os.Stat(filepath.Join(env.localDir, "remotecache.vdf")); err != nil {
		t.Error("this device's remotecache.vdf was deleted")
	}
}
