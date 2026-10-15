package presets

import (
	"path/filepath"
	"testing"
)

// OPENSAVE_MANIFEST_URL set empty turns the download off, for a CLI the
// e2e tests run as a separate program.
func TestManifestURLFromTheEnvironment(t *testing.T) {
	t.Setenv("OPENSAVE_MANIFEST_URL", "")
	if got := NewScanner(filepath.Join(t.TempDir(), "c.json")).ManifestURL; got != "" {
		t.Errorf("ManifestURL = %q with OPENSAVE_MANIFEST_URL empty, want none", got)
	}
}
