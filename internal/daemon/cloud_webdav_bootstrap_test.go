package daemon

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// Regression: a pending WebDAV retry with a listed but unverified archive
// must not cause CheckCloud to announce a head for that archive.
func TestWebDAVPendingRetryBlocksCloudHeadBootstrap(t *testing.T) {
	cloudDir := t.TempDir()
	dev := newCloudDevice(t, "Desktop", cloudDir)
	snap := dev.play("progress awaiting WebDAV verification")
	remoteName := cloudSnapshotName(cloudGame, "main", snap.ID)

	var mu sync.Mutex
	headPUTs := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := path.Base(r.URL.Path)
		switch r.Method {
		case "PROPFIND":
			w.WriteHeader(http.StatusMultiStatus)
			_, _ = fmt.Fprintf(w, `<?xml version="1.0"?><D:multistatus xmlns:D="DAV:"><D:response><D:href>/dav/</D:href></D:response><D:response><D:href>/dav/%s</D:href><D:propstat><D:prop><D:getcontentlength>4</D:getcontentlength></D:prop></D:propstat></D:response></D:multistatus>`, remoteName)
		case http.MethodGet:
			_, _ = w.Write([]byte("bad!"))
		case http.MethodPut:
			if strings.Contains(name, "__head-") {
				mu.Lock()
				headPUTs++
				mu.Unlock()
			}
			w.WriteHeader(http.StatusServiceUnavailable)
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
	rec, ok, err := dev.d.Store.GetCloudHead(cloudGame)
	if err != nil || !ok {
		t.Fatalf("head: %+v %v", rec, err)
	}
	rec.Published = ""
	if err := dev.d.Store.SetCloudHead(rec); err != nil {
		t.Fatal(err)
	}
	if err := dev.d.Store.NoteCloudUploadFailed(cloudGame, remoteName, snap.ZipPath, time.Now().UnixMilli()); err != nil {
		t.Fatal(err)
	}

	dev.check()
	rec, _, err = dev.d.Store.GetCloudHead(cloudGame)
	if err != nil {
		t.Fatal(err)
	}
	if rec.Published == snap.ID {
		t.Fatal("CheckCloud published unverified WebDAV archive")
	}
	pending, err := dev.d.Store.CloudRetries()
	if err != nil || len(pending) != 1 || pending[0].RemoteName != remoteName {
		t.Fatalf("pending=%+v err=%v", pending, err)
	}
	mu.Lock()
	defer mu.Unlock()
	if headPUTs != 0 {
		t.Fatalf("unverified WebDAV head was uploaded %d times", headPUTs)
	}
}

// An older OpenSave release can leave a bad final-named ZIP without a retry
// row. WebDAV bootstrap must verify the local archive before announcing it.
func TestWebDAVLegacyHeadBootstrapVerifiesRemoteBytes(t *testing.T) {
	for _, tc := range []struct {
		name    string
		corrupt bool
	}{
		{"identical existing object is safe to announce", false},
		{"corrupt legacy object must not get a head", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cloudDir := t.TempDir()
			dev := newCloudDevice(t, "Desktop", cloudDir)
			snap := dev.play("the original correct game progress")
			name := cloudSnapshotName(cloudGame, "main", snap.ID)
			original, err := os.ReadFile(filepath.Join(cloudDir, name))
			if err != nil {
				t.Fatal(err)
			}
			remote := bytes.Clone(original)
			if tc.corrupt {
				remote[len(remote)/2] ^= 0x4f
			}
			var mu sync.Mutex
			objects := map[string][]byte{name: remote}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				defer mu.Unlock()
				key := path.Base(r.URL.Path)
				switch r.Method {
				case "PROPFIND":
					w.WriteHeader(http.StatusMultiStatus)
					_, _ = fmt.Fprint(w, `<?xml version="1.0"?><D:multistatus xmlns:D="DAV:"><D:response><D:href>/dav/</D:href></D:response>`)
					for k, b := range objects {
						_, _ = fmt.Fprintf(w, `<D:response><D:href>/dav/%s</D:href><D:propstat><D:prop><D:getcontentlength>%d</D:getcontentlength></D:prop></D:propstat></D:response>`, k, len(b))
					}
					_, _ = fmt.Fprint(w, `</D:multistatus>`)
				case http.MethodGet:
					b, ok := objects[key]
					if !ok {
						w.WriteHeader(404)
						return
					}
					w.Header().Set("Content-Length", fmt.Sprint(len(b)))
					_, _ = w.Write(b)
				case http.MethodPut:
					b, _ := io.ReadAll(r.Body)
					objects[key] = b
					w.WriteHeader(http.StatusCreated)
				case "MOVE":
					parsed, err := url.Parse(r.Header.Get("Destination"))
					if err != nil {
						w.WriteHeader(400)
						return
					}
					dest := path.Base(parsed.Path)
					if _, exists := objects[dest]; exists {
						w.WriteHeader(412)
						return
					}
					objects[dest] = objects[key]
					delete(objects, key)
					w.WriteHeader(http.StatusCreated)
				case http.MethodDelete:
					delete(objects, key)
					w.WriteHeader(http.StatusNoContent)
				default:
					w.WriteHeader(http.StatusMethodNotAllowed)
				}
			}))
			defer server.Close()
			cfg, _ := dev.d.Store.GetCloudConfig()
			cfg.Provider = "webdav"
			cfg.URL = server.URL + "/dav/"
			if err := dev.d.Store.UpdateCloudConfig(cfg); err != nil {
				t.Fatal(err)
			}
			rec, _, err := dev.d.Store.GetCloudHead(cloudGame)
			if err != nil {
				t.Fatal(err)
			}
			rec.Published = ""
			if err := dev.d.Store.SetCloudHead(rec); err != nil {
				t.Fatal(err)
			}
			dev.check()
			rec, _, err = dev.d.Store.GetCloudHead(cloudGame)
			if err != nil {
				t.Fatal(err)
			}
			if published := rec.Published == snap.ID; published == tc.corrupt {
				t.Fatalf("head published=%t corrupt=%t", published, tc.corrupt)
			}
			pending, err := dev.d.Store.CloudRetries()
			if err != nil {
				t.Fatal(err)
			}
			if tc.corrupt && (len(pending) != 1 || pending[0].RemoteName != name) {
				t.Fatalf("missing corrupt legacy retry: %+v", pending)
			}
			mu.Lock()
			got := bytes.Clone(objects[name])
			mu.Unlock()
			if !bytes.Equal(got, remote) {
				t.Fatal("legacy final object overwritten")
			}
		})
	}
}
