package cloud

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path"
	"strings"
	"sync"
	"testing"

	"github.com/opensave/opensave/internal/store"
)

// Separate from the PR's larger fault server: this isolates the returned MOVE
// code, cleanup warning, and acknowledged-but-corrupting MOVE contract.
func TestWebDAVMOVESuccessAndCleanup(t *testing.T) {
	for _, tc := range []struct {
		name           string
		moveStatus     int
		moveCorruption bool
		stageCorrupt   bool
		deleteStatus   int
		wantError      bool
		wantWarn       bool
		wantCorrupt    bool
	}{
		{name: "201-created", moveStatus: 201},
		{name: "204-success-reuses-staging-verification", moveStatus: 204},
		{name: "500-staging-delete-is-reported", stageCorrupt: true, deleteStatus: 500, wantError: true, wantWarn: true},
		// Explicit limitation: no final GET on an unambiguous 201. A server
		// silently corrupting its own MOVE is beyond the selected 2N contract.
		{name: "201-silent-corrupting-MOVE-is-known-limitation", moveStatus: 201, moveCorruption: true, wantCorrupt: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var mu sync.Mutex
			objects := map[string][]byte{}
			var warnings []string
			var finalGETs, stageGETs int
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				defer mu.Unlock()
				name := path.Base(r.URL.Path)
				switch r.Method {
				case http.MethodGet:
					if strings.HasSuffix(name, ".part") {
						stageGETs++
					} else {
						finalGETs++
					}
					b, ok := objects[name]
					if !ok {
						w.WriteHeader(http.StatusNotFound)
						return
					}
					w.Header().Set("Content-Length", fmt.Sprint(len(b)))
					_, _ = w.Write(b)
				case http.MethodPut:
					b, _ := io.ReadAll(r.Body)
					if tc.stageCorrupt && len(b) > 0 {
						b[0] ^= 0xff
					}
					objects[name] = b
					w.WriteHeader(http.StatusCreated)
				case "MOVE":
					dst, _ := url.Parse(r.Header.Get("Destination"))
					if r.Header.Get("Overwrite") != "F" {
						t.Errorf("MOVE lacks Overwrite: F")
					}
					b := bytes.Clone(objects[name])
					if tc.moveCorruption && len(b) > 0 {
						b[0] ^= 0xff
					}
					objects[path.Base(dst.Path)] = b
					delete(objects, name)
					w.WriteHeader(tc.moveStatus)
				case http.MethodDelete:
					if tc.deleteStatus != 0 {
						w.WriteHeader(tc.deleteStatus)
						return
					}
					delete(objects, name)
					w.WriteHeader(http.StatusNoContent)
				default:
					w.WriteHeader(http.StatusMethodNotAllowed)
				}
			}))
			defer srv.Close()
			p := webDAV{s: &Service{Log: func(level, msg string) {
				if level == "warn" {
					mu.Lock()
					warnings = append(warnings, msg)
					mu.Unlock()
				}
			}}, cfg: store.CloudConfig{URL: srv.URL + "/dav/"}}
			f, err := os.CreateTemp(t.TempDir(), "webdav-*.zip")
			if err != nil {
				t.Fatal(err)
			}
			defer f.Close()
			content := []byte("synthetic ZIP archive with nonzero bytes")
			if _, err = f.Write(content); err != nil {
				t.Fatal(err)
			}
			err = p.upload(f, int64(len(content)), "game__main__snap_789.zip")
			if (err != nil) != tc.wantError {
				t.Fatalf("upload err=%v wantError=%v", err, tc.wantError)
			}
			mu.Lock()
			got := bytes.Clone(objects["game__main__snap_789.zip"])
			gotWarn := len(warnings) > 0
			logs := strings.Join(warnings, "; ")
			finalReads, stagedReads := finalGETs, stageGETs
			mu.Unlock()
			if gotWarn != tc.wantWarn {
				t.Fatalf("warnings=%v wantWarning=%v", logs, tc.wantWarn)
			}
			// The first GET tests whether the final name already exists. A
			// successful 201 or 204 must *not* trigger an additional final GET.
			if !tc.wantError && (finalReads != 1 || stagedReads != 1) {
				t.Fatalf("GET counts: final=%d (want 1 preflight), stage=%d (want 1 integrity readback)", finalReads, stagedReads)
			}
			if !tc.wantError && !tc.wantCorrupt && !bytes.Equal(got, content) {
				t.Fatalf("published archive does not match source for HTTP %d", tc.moveStatus)
			}
			if tc.wantCorrupt && bytes.Equal(got, content) {
				t.Fatal("corrupting fixture failed to mutate published bytes")
			}
			if tc.wantCorrupt && err != nil {
				t.Fatal("documented no-final-GET limit should return success for 201")
			}
		})
	}
}
