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
// newer than its index - after every weekly download, the next scan or the
// next game tracked - and parsing the 17.7 MB YAML held that caller up for
// some thirty seconds.
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
