package presets

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// A downloaded manifest arrives with its index already built, so the next
// caller that wants the index reads it instead of parsing the YAML.
//
// The index used to be built by whichever caller first found the manifest
// newer than its index - after a background download, usually a snapshot
// reading the game's device-settings exclusions - and parsing the 17.7 MB
// YAML held that snapshot up for some twenty seconds.
func TestDownloadedManifestArrivesWithItsIndex(t *testing.T) {
	const fixture = `
Hollow Game:
  files:
    <winAppData>/MoonStudio/HollowGame:
      tags: [save]
      when:
        - os: windows
  steam:
    id: 4242
`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(fixture))
	}))
	defer srv.Close()

	dir := t.TempDir()
	sc := &Scanner{CacheFile: filepath.Join(dir, "steam-app-cache.json"), GOOS: "windows", ManifestURL: srv.URL}
	yamlPath, indexPath := sc.manifestPaths()
	sc.downloadManifest(yamlPath)

	yamlInfo, err := os.Stat(yamlPath)
	if err != nil {
		t.Fatalf("the manifest was not downloaded: %v", err)
	}
	idxInfo, err := os.Stat(indexPath)
	if err != nil {
		t.Fatalf("the manifest arrived without its index: %v", err)
	}
	if !idxInfo.ModTime().After(yamlInfo.ModTime()) {
		t.Errorf("the index (%v) is not newer than the manifest (%v): the next caller would parse the YAML again",
			idxInfo.ModTime(), yamlInfo.ModTime())
	}

	// Made unreadable, the manifest proves the next caller takes the index:
	// parsed, it would yield nothing of Hollow Game.
	if err := os.WriteFile(yamlPath, []byte(":\tnot yaml"), 0o666); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(yamlPath, time.Now(), yamlInfo.ModTime()); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, g := range sc.loadManifestIndex() {
		if g.Name == "Hollow Game" {
			found = true
		}
	}
	if !found {
		t.Error("the index did not come from the downloaded manifest")
	}
}

// The index is read from disk once while the file stays the same, not on
// every snapshot (some 120 ms for the real one), and read again once it
// changes.
func TestManifestIndexIsReadOnceWhileUnchanged(t *testing.T) {
	sc := manifestScanner(t, `
Hollow Game:
  files:
    <winAppData>/MoonStudio/HollowGame:
      tags: [save]
      when:
        - os: windows
`)
	if len(sc.loadManifestIndex()) == 0 {
		t.Fatal("setup: no index built from the manifest")
	}
	_, indexPath := sc.manifestPaths()
	info, err := os.Stat(indexPath)
	if err != nil {
		t.Fatal(err)
	}

	// Same size and time, other content: still the index read before.
	garbage := make([]byte, info.Size())
	for i := range garbage {
		garbage[i] = ' '
	}
	if err := os.WriteFile(indexPath, garbage, 0o666); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(indexPath, info.ModTime(), info.ModTime()); err != nil {
		t.Fatal(err)
	}
	if len(sc.loadManifestIndex()) == 0 {
		t.Error("the index was read from disk again although the file had not changed")
	}

	// Changed: read again, and the unreadable file rebuilt from the manifest.
	later := info.ModTime().Add(time.Second)
	if err := os.Chtimes(indexPath, later, later); err != nil {
		t.Fatal(err)
	}
	if games := sc.loadManifestIndex(); len(games) == 0 || games[0].Name != "Hollow Game" {
		t.Errorf("after the index changed: %d games", len(games))
	}
}
