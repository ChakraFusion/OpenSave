package cloud

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/opensave/opensave/internal/store"
)

// A fresh install has backup on, with the local folder provider and no folder
// chosen. That is not ready, and an upload then says nothing about uploading:
// it used to announce every snapshot as "uploading" and then fail in silence.
func TestAFreshInstallIsNotReadyAndSaysNothing(t *testing.T) {
	svc, s := newTestService(t)
	var logged []string
	svc.Log = func(level, msg string) { logged = append(logged, msg) }
	cfg, err := s.GetCloudConfig()
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.Enabled || cfg.Provider != "local" || cfg.URL != "" {
		t.Fatalf("a fresh install's cloud config changed: %+v — this test assumes backup on, local, no folder", cfg)
	}
	if Ready(cfg) {
		t.Fatal("backup on with no folder chosen counts as ready")
	}

	zip := filepath.Join(t.TempDir(), "g__main__snap_1.zip")
	if err := os.WriteFile(zip, []byte("zip"), 0o644); err != nil {
		t.Fatal(err)
	}
	err = svc.Upload(zip, filepath.Base(zip))
	if !IsNotConfigured(err) {
		t.Fatalf("upload with nowhere to go: err = %v, want a not-configured error", err)
	}
	for _, m := range logged {
		if strings.Contains(m, "uploading") {
			t.Fatalf("an upload with nowhere to go announced itself: %q", m)
		}
	}
}

func TestReady(t *testing.T) {
	for _, c := range []struct {
		name string
		cfg  store.CloudConfig
		want bool
	}{
		{"off", store.CloudConfig{Enabled: false, Provider: "local", URL: "D:/backups"}, false},
		{"local with a folder", store.CloudConfig{Enabled: true, Provider: "local", URL: "D:/backups"}, true},
		{"local without one", store.CloudConfig{Enabled: true, Provider: "local"}, false},
		{"webdav with an address", store.CloudConfig{Enabled: true, Provider: "webdav", URL: "https://dav.example"}, true},
		{"webhook without one", store.CloudConfig{Enabled: true, Provider: "webhook", URL: "  "}, false},
		{"signed in", store.CloudConfig{Enabled: true, Provider: "google_drive", RefreshToken: "r"}, true},
		{"not signed in", store.CloudConfig{Enabled: true, Provider: "dropbox"}, false},
		{"unknown provider", store.CloudConfig{Enabled: true, Provider: "ftp", URL: "x"}, false},
	} {
		if got := Ready(c.cfg); got != c.want {
			t.Errorf("%s: Ready = %v, want %v", c.name, got, c.want)
		}
	}
}
