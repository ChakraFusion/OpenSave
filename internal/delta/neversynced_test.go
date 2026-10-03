package delta

import (
	"os"
	"path/filepath"
	"testing"
)

// Steam rewrites remotecache.vdf in a game's userdata folder on every start,
// and that folder can sit under Program Files where it cannot be written. It
// is the launcher's file, not the save: it must never show up as a change.
func TestBuildManifestLeavesOutLauncherFiles(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "remote"), 0o755); err != nil {
		t.Fatal(err)
	}
	for name, data := range map[string]string{
		"remote/save.sav":     "save",
		"remotecache.vdf":     "steam",
		"sub/RemoteCache.VDF": "steam",
	} {
		p := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(data), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	m, err := BuildManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := m.Files["remote/save.sav"]; !ok {
		t.Fatalf("the save is missing: %v", m.Files)
	}
	for p := range m.Files {
		if NeverSynced(p) {
			t.Errorf("%s is in the manifest", p)
		}
	}
	before := m.ManifestHash()
	InvalidateRoot(dir)
	if err := os.WriteFile(filepath.Join(dir, "remotecache.vdf"), []byte("steam started again"), 0o644); err != nil {
		t.Fatal(err)
	}
	m2, err := BuildManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	if m2.ManifestHash() != before {
		t.Error("Steam rewriting remotecache.vdf changed the save's hash")
	}
}
