package daemon

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path"
	"path/filepath"
	"testing"
	"time"

	"github.com/opensave/opensave/internal/cloud"
	"github.com/opensave/opensave/internal/store"
)

// A cloud copy that fails to go up — the network down, a name that will not
// resolve, a connection that stalls — is sent again once the cloud can be
// reached, instead of being left out of the backup for good. It used to be
// logged and dropped: only "Upload local snapshots", pressed by hand, ever
// sent it.
//
// The cloud here is a local folder. It starts as a file, so every upload into
// it fails, and becomes a folder: the network coming back.
func TestAFailedCloudUploadIsSentAgainWhenTheCloudIsBack(t *testing.T) {
	cloudDir := filepath.Join(t.TempDir(), "cloud")
	if err := os.WriteFile(cloudDir, []byte("not a folder yet"), 0o666); err != nil {
		t.Fatal(err)
	}
	dev := newCloudDevice(t, "Desktop", cloudDir)

	snap := dev.play("progress made while offline")
	name := cloudSnapshotName(cloudGame, "main", snap.ID)
	pending, err := dev.d.Store.CloudRetries()
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 1 || pending[0].RemoteName != name {
		t.Fatalf("the failed upload was not kept to try again: %+v", pending)
	}

	// The cloud comes back.
	if err := os.Remove(cloudDir); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(cloudDir, 0o777); err != nil {
		t.Fatal(err)
	}
	if err := cloud.MarkLocalFolder(cloudDir); err != nil { // the share, back with its marker
		t.Fatal(err)
	}
	dev.check()

	if _, err := os.Stat(filepath.Join(cloudDir, name)); err != nil {
		t.Fatalf("the snapshot never reached the cloud: %v", err)
	}
	if pending, _ := dev.d.Store.CloudRetries(); len(pending) != 0 {
		t.Errorf("still waiting to send what has been sent: %+v", pending)
	}
}

// One that no longer exists here — pruned while it waited — is forgotten, not
// tried for ever.
func TestAFailedUploadOfASnapshotSinceRemovedIsForgotten(t *testing.T) {
	cloudDir := filepath.Join(t.TempDir(), "cloud")
	if err := os.WriteFile(cloudDir, []byte("not a folder yet"), 0o666); err != nil {
		t.Fatal(err)
	}
	dev := newCloudDevice(t, "Desktop", cloudDir)
	snap := dev.play("soon gone")
	if _, err := dev.d.Snapshots.DeleteSnapshot(cloudGame, snap.ID); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(cloudDir); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(cloudDir, 0o777); err != nil {
		t.Fatal(err)
	}
	if err := cloud.MarkLocalFolder(cloudDir); err != nil { // the share, back with its marker
		t.Fatal(err)
	}
	dev.check()
	if pending, _ := dev.d.Store.CloudRetries(); len(pending) != 0 {
		t.Errorf("a snapshot that is gone is still waiting to be sent: %+v", pending)
	}
	if entries, _ := os.ReadDir(cloudDir); len(entries) != 1 { // the backup folder's marker, and nothing sent
		t.Errorf("something was sent for a snapshot that is gone: %v", entries)
	}
}

// A WebDAV MOVE response can be lost after the server committed it. A later
// listing therefore proves only that the name exists, not that the bytes are
// the ones this failed upload meant to publish. Re-entering upload compares
// them: an identical object completes the retry; a different one remains a
// visible failed upload and is never overwritten.
func TestWebDAVListedNameDoesNotSilentlyCompleteFailedUpload(t *testing.T) {
	for _, test := range []struct {
		name          string
		changeRemote  bool
		wantRemaining bool
	}{
		{"identical remote completes retry", false, false},
		{"different remote keeps retry", true, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			initialCloud := t.TempDir()
			dev := newCloudDevice(t, "Desktop", initialCloud)
			snap := dev.play("save waiting for confirmation")
			remoteName := cloudSnapshotName(cloudGame, "main", snap.ID)
			archive, err := os.ReadFile(filepath.Join(initialCloud, remoteName))
			if err != nil {
				t.Fatal(err)
			}
			remote := bytes.Clone(archive)
			if test.changeRemote {
				remote[len(remote)/2] ^= 0x5a
			}

			objects := map[string][]byte{remoteName: remote}
			getCount := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				name := path.Base(r.URL.Path)
				switch r.Method {
				case http.MethodGet:
					getCount++
					body, ok := objects[name]
					if !ok {
						w.WriteHeader(http.StatusNotFound)
						return
					}
					w.Header().Set("Content-Length", fmt.Sprint(len(body)))
					_, _ = w.Write(body)
				case http.MethodPut:
					objects[name], _ = io.ReadAll(r.Body)
					w.WriteHeader(http.StatusCreated)
				case http.MethodDelete:
					delete(objects, name)
					w.WriteHeader(http.StatusNoContent)
				default:
					w.WriteHeader(http.StatusMethodNotAllowed)
				}
			}))
			defer server.Close()

			cfg, err := dev.d.Store.GetCloudConfig()
			if err != nil {
				t.Fatal(err)
			}
			cfg.Provider = "webdav"
			cfg.URL = server.URL + "/dav/"
			if err := dev.d.Store.UpdateCloudConfig(cfg); err != nil {
				t.Fatal(err)
			}
			game, err := dev.d.Store.GetGame(cloudGame)
			if err != nil {
				t.Fatal(err)
			}
			game.MaxSnapshots = -1 // keep this test out of retention/listing
			if err := dev.d.Store.UpdateGame(game); err != nil {
				t.Fatal(err)
			}
			if err := dev.d.Store.SetCloudHead(store.CloudHead{GameID: cloudGame}); err != nil {
				t.Fatal(err)
			}
			if err := dev.d.Store.NoteCloudUploadFailed(cloudGame, remoteName, snap.ZipPath, time.Now().UnixMilli()); err != nil {
				t.Fatal(err)
			}

			dev.d.retryFailedUploads([]cloud.CloudFile{{Name: remoteName}})
			dev.d.uploads.Wait(0)
			pending, err := dev.d.Store.CloudRetries()
			if err != nil {
				t.Fatal(err)
			}
			if (len(pending) != 0) != test.wantRemaining {
				t.Fatalf("pending = %+v, wantRemaining=%t", pending, test.wantRemaining)
			}
			if getCount != 1 {
				t.Fatalf("listed WebDAV object bypassed byte verification: GET count=%d", getCount)
			}
			if !bytes.Equal(objects[remoteName], remote) {
				t.Fatal("retry replaced the listed WebDAV object")
			}
		})
	}
}
