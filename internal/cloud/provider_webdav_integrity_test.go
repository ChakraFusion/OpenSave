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
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/opensave/opensave/internal/store"
	xwebdav "golang.org/x/net/webdav"
)

type webDAVRequestRecord struct {
	method string
	name   string
	header http.Header
}

// faultWebDAV is deliberately less trustworthy than a real WebDAV server. It
// can acknowledge bytes it did not keep, alter bytes without changing their
// length, fail verification reads, race another publisher into the final name,
// and lose the MOVE response before or after committing it.
type faultWebDAV struct {
	t *testing.T

	mu      sync.Mutex
	objects map[string][]byte
	records []webDAVRequestRecord
	moves   int

	truncatePUT     bool
	corruptPUT      bool
	extraGETByte    bool
	tempGETStatus   int
	finalGETStatus  int
	reconcileStatus int
	interruptGET    bool
	timeoutGET      bool
	moveStatus      int
	moveAttempted   bool
	corruptMOVE     bool
	disconnectMOVE  string // "before" or "after" the commit
	racingFinal     []byte
	racingFinalSet  bool
	ignoreOverwrite bool
	deleteStatus    int
}

func newFaultWebDAV(t *testing.T) (*faultWebDAV, webDAV, func()) {
	t.Helper()
	d := &faultWebDAV{t: t, objects: make(map[string][]byte)}
	srv := httptest.NewServer(http.HandlerFunc(d.serveHTTP))
	p := webDAV{
		s: &Service{},
		cfg: store.CloudConfig{
			Enabled:     true,
			Provider:    "webdav",
			URL:         srv.URL + "/dav/",
			Username:    "test-user",
			Password:    "test-secret",
			HeadersJSON: `{"X-OpenSave-Test":"verified"}`,
		},
	}
	return d, p, srv.Close
}

func (d *faultWebDAV) serveHTTP(w http.ResponseWriter, r *http.Request) {
	if user, pass, ok := r.BasicAuth(); !ok || user != "test-user" || pass != "test-secret" {
		d.t.Errorf("%s %s missing WebDAV credentials", r.Method, r.URL.Path)
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	if r.Header.Get("X-OpenSave-Test") != "verified" {
		d.t.Errorf("%s %s missing custom header", r.Method, r.URL.Path)
		w.WriteHeader(http.StatusForbidden)
		return
	}

	name := path.Base(r.URL.Path)
	d.mu.Lock()
	d.records = append(d.records, webDAVRequestRecord{method: r.Method, name: name, header: r.Header.Clone()})

	switch r.Method {
	case http.MethodPut:
		if !strings.HasPrefix(name, ".opensave-") || !strings.HasSuffix(name, partialSuffix) {
			d.t.Errorf("PUT used public or unowned staging name %q", name)
		}
		if r.Header.Get("If-None-Match") != "*" {
			d.t.Errorf("PUT If-None-Match = %q", r.Header.Get("If-None-Match"))
		}
		if r.Header.Get("Content-Type") != "application/zip" {
			d.t.Errorf("PUT Content-Type = %q", r.Header.Get("Content-Type"))
		}
		if _, exists := d.objects[name]; exists {
			d.mu.Unlock()
			w.WriteHeader(http.StatusPreconditionFailed)
			return
		}
		d.mu.Unlock()
		body, err := io.ReadAll(r.Body)
		if err != nil {
			d.t.Errorf("read PUT body: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		d.mu.Lock()
		if d.truncatePUT && len(body) > 0 {
			body = body[:len(body)/2]
			d.truncatePUT = false
		}
		if d.corruptPUT && len(body) > 0 {
			body[0] ^= 0x5a
			d.corruptPUT = false
		}
		d.objects[name] = bytes.Clone(body)
		d.mu.Unlock()
		w.WriteHeader(http.StatusCreated)
		return

	case http.MethodGet:
		body, exists := d.objects[name]
		isTemp := strings.HasSuffix(name, partialSuffix)
		status := d.tempGETStatus
		if !isTemp {
			status = d.finalGETStatus
			if d.moveAttempted && d.reconcileStatus != 0 {
				status = d.reconcileStatus
			}
		}
		interrupt := d.interruptGET && isTemp
		timeout := d.timeoutGET && isTemp
		extra := d.extraGETByte && isTemp
		d.mu.Unlock()
		if isTemp && status != 0 {
			w.WriteHeader(status)
			return
		}
		if !isTemp && status != 0 {
			w.WriteHeader(status)
			return
		}
		if !exists {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if timeout {
			<-r.Context().Done()
			return
		}
		if interrupt {
			hijacker, ok := w.(http.Hijacker)
			if !ok {
				d.t.Error("test server cannot interrupt GET")
				return
			}
			conn, rw, err := hijacker.Hijack()
			if err != nil {
				d.t.Errorf("hijack GET: %v", err)
				return
			}
			fmt.Fprintf(rw, "HTTP/1.1 200 OK\r\nContent-Length: %d\r\n\r\n", len(body)+10)
			_, _ = rw.Write(body[:len(body)/2])
			_ = rw.Flush()
			_ = conn.Close()
			return
		}
		if extra {
			body = append(bytes.Clone(body), 0x7f)
		}
		w.Header().Set("Content-Length", fmt.Sprint(len(body)))
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(body)
		return

	case "MOVE":
		d.moveAttempted = true
		if r.Header.Get("Overwrite") != "F" {
			d.t.Errorf("MOVE Overwrite = %q", r.Header.Get("Overwrite"))
		}
		destination, err := url.Parse(r.Header.Get("Destination"))
		if err != nil || !destination.IsAbs() {
			d.t.Errorf("MOVE Destination = %q, err=%v", r.Header.Get("Destination"), err)
			d.mu.Unlock()
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		dst := path.Base(destination.Path)
		if d.racingFinalSet {
			d.objects[dst] = bytes.Clone(d.racingFinal)
			d.racingFinalSet = false
		}
		if d.moveStatus != 0 {
			status := d.moveStatus
			d.mu.Unlock()
			w.WriteHeader(status)
			return
		}
		if d.disconnectMOVE == "before" {
			d.mu.Unlock()
			d.disconnect(w)
			return
		}
		if _, exists := d.objects[dst]; exists && !d.ignoreOverwrite {
			d.mu.Unlock()
			w.WriteHeader(http.StatusPreconditionFailed)
			return
		}
		body, exists := d.objects[name]
		if !exists {
			d.mu.Unlock()
			w.WriteHeader(http.StatusNotFound)
			return
		}
		d.objects[dst] = bytes.Clone(body)
		if d.corruptMOVE && len(d.objects[dst]) > 0 {
			d.objects[dst][0] ^= 0x33
		}
		delete(d.objects, name)
		d.moves++
		disconnect := d.disconnectMOVE == "after"
		d.mu.Unlock()
		if disconnect {
			d.disconnect(w)
			return
		}
		w.WriteHeader(http.StatusCreated)
		return

	case http.MethodDelete:
		status := d.deleteStatus
		if status == 0 {
			delete(d.objects, name)
			status = http.StatusNoContent
		}
		d.mu.Unlock()
		w.WriteHeader(status)
		return

	case http.MethodHead:
		d.mu.Unlock()
		d.t.Error("HEAD is not part of verified WebDAV publication")
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	d.mu.Unlock()
	w.WriteHeader(http.StatusMethodNotAllowed)
}

func (d *faultWebDAV) disconnect(w http.ResponseWriter) {
	hijacker, ok := w.(http.Hijacker)
	if !ok {
		d.t.Error("test server cannot disconnect MOVE")
		return
	}
	conn, _, err := hijacker.Hijack()
	if err != nil {
		d.t.Errorf("hijack MOVE: %v", err)
		return
	}
	_ = conn.Close()
}

func (d *faultWebDAV) object(name string) ([]byte, bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	b, ok := d.objects[name]
	return bytes.Clone(b), ok
}

func (d *faultWebDAV) temporaryCount() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	n := 0
	for name := range d.objects {
		if strings.HasPrefix(name, ".opensave-") && strings.HasSuffix(name, partialSuffix) {
			n++
		}
	}
	return n
}

func uploadThroughWebDAV(t *testing.T, p webDAV, name string, content []byte) error {
	t.Helper()
	fileName := filepath.Join(t.TempDir(), "snapshot.zip")
	if err := os.WriteFile(fileName, content, 0o600); err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(fileName)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	return p.upload(f, int64(len(content)), name)
}

func TestWebDAVVerifiedPublicationUsesStagingGETAndNonOverwritingMOVE(t *testing.T) {
	d, p, closeServer := newFaultWebDAV(t)
	defer closeServer()

	name := "game__main__snap_100.zip"
	content := bytes.Repeat([]byte("save bytes\x00"), 256)
	if err := uploadThroughWebDAV(t, p, name, content); err != nil {
		t.Fatal(err)
	}
	if got, ok := d.object(name); !ok || !bytes.Equal(got, content) {
		t.Fatalf("published bytes differ: present=%t size=%d", ok, len(got))
	}
	if d.temporaryCount() != 0 || d.moves != 1 {
		t.Fatalf("publication left staging data or skipped MOVE: temporary=%d moves=%d", d.temporaryCount(), d.moves)
	}

	d.mu.Lock()
	records := append([]webDAVRequestRecord(nil), d.records...)
	d.mu.Unlock()
	methods := make([]string, 0, len(records))
	for _, record := range records {
		methods = append(methods, record.method)
		if record.method == http.MethodGet && record.header.Get("Accept-Encoding") != "identity" {
			t.Errorf("GET %s Accept-Encoding = %q", record.name, record.header.Get("Accept-Encoding"))
		}
	}
	if got := strings.Join(methods, ","); got != "GET,PUT,GET,MOVE" {
		t.Fatalf("request sequence = %s, want GET,PUT,GET,MOVE", got)
	}
}

func TestWebDAVStagedVerificationFailsClosed(t *testing.T) {
	tests := []struct {
		name      string
		configure func(*faultWebDAV)
	}{
		{"truncated bytes", func(d *faultWebDAV) { d.truncatePUT = true }},
		{"same length corruption", func(d *faultWebDAV) { d.corruptPUT = true }},
		{"oversized response", func(d *faultWebDAV) { d.extraGETByte = true }},
		{"verification 403", func(d *faultWebDAV) { d.tempGETStatus = http.StatusForbidden }},
		{"verification 404", func(d *faultWebDAV) { d.tempGETStatus = http.StatusNotFound }},
		{"verification 500", func(d *faultWebDAV) { d.tempGETStatus = http.StatusInternalServerError }},
		{"verification 503", func(d *faultWebDAV) { d.tempGETStatus = http.StatusServiceUnavailable }},
		{"interrupted response", func(d *faultWebDAV) { d.interruptGET = true }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			d, p, closeServer := newFaultWebDAV(t)
			defer closeServer()
			test.configure(d)
			name := "game__main__snap_101.zip"
			if err := uploadThroughWebDAV(t, p, name, bytes.Repeat([]byte("payload"), 128)); err == nil {
				t.Fatal("faulty staged object was reported as published")
			}
			if _, ok := d.object(name); ok || d.moves != 0 || d.temporaryCount() != 0 {
				t.Fatalf("failed verification exposed data: final=%t moves=%d temporary=%d", ok, d.moves, d.temporaryCount())
			}
		})
	}
}

func TestWebDAVStagedVerificationTimeoutFailsClosed(t *testing.T) {
	oldTimeout := perRequestTransferTimeout
	perRequestTransferTimeout = 40 * time.Millisecond
	defer func() { perRequestTransferTimeout = oldTimeout }()

	d, p, closeServer := newFaultWebDAV(t)
	defer closeServer()
	d.timeoutGET = true
	name := "game__main__snap_102.zip"
	if err := uploadThroughWebDAV(t, p, name, []byte("save")); err == nil {
		t.Fatal("timed-out verification was reported as published")
	}
	if _, ok := d.object(name); ok || d.moves != 0 || d.temporaryCount() != 0 {
		t.Fatalf("timeout exposed data: final=%t moves=%d temporary=%d", ok, d.moves, d.temporaryCount())
	}
}

func TestWebDAVDestinationPreflightFailsClosed(t *testing.T) {
	for _, status := range []int{http.StatusForbidden, http.StatusInternalServerError, http.StatusServiceUnavailable} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			d, p, closeServer := newFaultWebDAV(t)
			defer closeServer()
			d.finalGETStatus = status
			name := "game__main__snap_102b.zip"
			if err := uploadThroughWebDAV(t, p, name, []byte("save")); err == nil {
				t.Fatal("unverifiable destination was treated as absent")
			}
			if _, ok := d.object(name); ok || d.moves != 0 || d.temporaryCount() != 0 {
				t.Fatalf("preflight failure changed remote state: final=%t moves=%d temporary=%d", ok, d.moves, d.temporaryCount())
			}
			d.mu.Lock()
			records := append([]webDAVRequestRecord(nil), d.records...)
			d.mu.Unlock()
			if len(records) != 1 || records[0].method != http.MethodGet {
				t.Fatalf("preflight failure made extra requests: %+v", records)
			}
		})
	}
}

func TestWebDAVExistingArchiveIsImmutableAndIdenticalRetryIsAccepted(t *testing.T) {
	d, p, closeServer := newFaultWebDAV(t)
	defer closeServer()
	name := "game__main__snap_103.zip"
	original := bytes.Repeat([]byte("original"), 64)
	d.objects[name] = bytes.Clone(original)
	d.ignoreOverwrite = true // preflight still protects an ordinary retry

	if err := uploadThroughWebDAV(t, p, name, original); err != nil {
		t.Fatalf("identical retry: %v", err)
	}
	if err := uploadThroughWebDAV(t, p, name, bytes.Repeat([]byte("different"), 64)); err == nil {
		t.Fatal("same-length different retry replaced an immutable archive")
	}
	if err := uploadThroughWebDAV(t, p, name, []byte("short")); err == nil {
		t.Fatal("different-length retry replaced an immutable archive")
	}
	if got, _ := d.object(name); !bytes.Equal(got, original) || d.moves != 0 || d.temporaryCount() != 0 {
		t.Fatalf("existing archive changed: bytes_equal=%t moves=%d temporary=%d", bytes.Equal(got, original), d.moves, d.temporaryCount())
	}
}

func TestWebDAVConcurrentDestinationRaceIsReconciled(t *testing.T) {
	tests := []struct {
		name      string
		racing    []byte
		wantError bool
	}{
		{"identical publisher wins", []byte("new save"), false},
		{"different publisher wins", []byte("other save"), true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			d, p, closeServer := newFaultWebDAV(t)
			defer closeServer()
			d.racingFinal = bytes.Clone(test.racing)
			d.racingFinalSet = true
			name := "game__main__snap_104.zip"
			err := uploadThroughWebDAV(t, p, name, []byte("new save"))
			if (err != nil) != test.wantError {
				t.Fatalf("upload error = %v, wantError=%t", err, test.wantError)
			}
			if got, _ := d.object(name); !bytes.Equal(got, test.racing) {
				t.Fatalf("racing publisher's bytes changed: %q", got)
			}
			if d.moves != 0 || d.temporaryCount() != 0 {
				t.Fatalf("race overwrote final or stranded staging: moves=%d temporary=%d", d.moves, d.temporaryCount())
			}
		})
	}
}

func TestWebDAVAmbiguousMOVEIsReconciled(t *testing.T) {
	t.Run("server committed before response was lost", func(t *testing.T) {
		d, p, closeServer := newFaultWebDAV(t)
		defer closeServer()
		d.disconnectMOVE = "after"
		name := "game__main__snap_105.zip"
		content := []byte("committed save")
		if err := uploadThroughWebDAV(t, p, name, content); err != nil {
			t.Fatalf("committed MOVE was not reconciled: %v", err)
		}
		if got, _ := d.object(name); !bytes.Equal(got, content) || d.temporaryCount() != 0 {
			t.Fatalf("committed result differs or staging remains: %q temporary=%d", got, d.temporaryCount())
		}
	})

	t.Run("committed object differs", func(t *testing.T) {
		d, p, closeServer := newFaultWebDAV(t)
		defer closeServer()
		d.disconnectMOVE = "after"
		d.corruptMOVE = true
		name := "game__main__snap_105b.zip"
		if err := uploadThroughWebDAV(t, p, name, []byte("committed then changed")); err == nil {
			t.Fatal("different final bytes reconciled as success")
		}
		if _, ok := d.object(name); !ok || d.temporaryCount() != 0 {
			t.Fatalf("fixture did not retain uncertain final: present=%t temporary=%d", ok, d.temporaryCount())
		}
	})

	t.Run("committed object cannot be read back", func(t *testing.T) {
		d, p, closeServer := newFaultWebDAV(t)
		defer closeServer()
		d.disconnectMOVE = "after"
		d.reconcileStatus = http.StatusServiceUnavailable
		name := "game__main__snap_105c.zip"
		if err := uploadThroughWebDAV(t, p, name, []byte("committed but unverifiable")); err == nil {
			t.Fatal("unverifiable MOVE outcome was reported as success")
		}
		if _, ok := d.object(name); !ok || d.temporaryCount() != 0 {
			t.Fatalf("fixture did not retain uncertain final: present=%t temporary=%d", ok, d.temporaryCount())
		}
	})

	t.Run("server disconnected before commit", func(t *testing.T) {
		d, p, closeServer := newFaultWebDAV(t)
		defer closeServer()
		d.disconnectMOVE = "before"
		name := "game__main__snap_106.zip"
		if err := uploadThroughWebDAV(t, p, name, []byte("not committed")); err == nil {
			t.Fatal("uncommitted MOVE was reported as success")
		}
		if _, ok := d.object(name); ok || d.temporaryCount() != 0 {
			t.Fatalf("uncommitted MOVE exposed final=%t temporary=%d", ok, d.temporaryCount())
		}
	})
}

func TestWebDAVRequiresMOVEAndCleanupDoesNotMaskFailure(t *testing.T) {
	for _, status := range []int{http.StatusMethodNotAllowed, http.StatusNotImplemented} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			d, p, closeServer := newFaultWebDAV(t)
			defer closeServer()
			d.moveStatus = status
			d.deleteStatus = http.StatusInternalServerError
			name := "game__main__snap_107.zip"
			err := uploadThroughWebDAV(t, p, name, []byte("save"))
			if err == nil || !strings.Contains(err.Error(), "requires MOVE") {
				t.Fatalf("MOVE status %d error = %v", status, err)
			}
			if _, ok := d.object(name); ok {
				t.Fatal("unsupported MOVE exposed a final archive")
			}
			if d.temporaryCount() != 1 {
				t.Fatalf("fixture did not preserve the cleanup failure: temporary=%d", d.temporaryCount())
			}
		})
	}
}

func TestWebDAVListHidesOwnedStagingObjects(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "PROPFIND" {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		w.WriteHeader(http.StatusMultiStatus)
		fmt.Fprint(w, `<?xml version="1.0"?>
<D:multistatus xmlns:D="DAV:">
  <D:response><D:href>/dav/</D:href></D:response>
  <D:response><D:href>/dav/.opensave-0123456789abcdef0123456789abcdef.part</D:href><D:propstat><D:prop><D:getcontentlength>10</D:getcontentlength></D:prop></D:propstat></D:response>
  <D:response><D:href>/dav/game__main__snap_108.zip</D:href><D:propstat><D:prop><D:getcontentlength>20</D:getcontentlength></D:prop></D:propstat></D:response>
</D:multistatus>`)
	}))
	defer server.Close()
	p := webDAV{s: &Service{}, cfg: store.CloudConfig{URL: server.URL + "/dav/"}}
	files, err := p.list()
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 || files[0].Name != "game__main__snap_108.zip" {
		t.Fatalf("WebDAV listing exposed staging object: %+v", files)
	}
}

func TestWebDAVVerifiedPublicationAgainstXNetWebDAV(t *testing.T) {
	handler := &xwebdav.Handler{
		Prefix:     "/dav/",
		FileSystem: xwebdav.NewMemFS(),
		LockSystem: xwebdav.NewMemLS(),
	}
	server := httptest.NewServer(handler)
	defer server.Close()
	p := webDAV{s: &Service{}, cfg: store.CloudConfig{URL: server.URL + "/dav/"}}
	name := "game__main__snap_109.zip"
	content := bytes.Repeat([]byte("portable WebDAV bytes"), 128)
	if err := uploadThroughWebDAV(t, p, name, content); err != nil {
		t.Fatalf("first publication: %v", err)
	}
	if err := uploadThroughWebDAV(t, p, name, content); err != nil {
		t.Fatalf("identical retry: %v", err)
	}
	if err := uploadThroughWebDAV(t, p, name, bytes.Repeat([]byte("different WebDAV data"), 128)); err == nil {
		t.Fatal("different retry replaced the published archive")
	}
	resp, err := http.Get(server.URL + "/dav/" + name)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	got, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK || !bytes.Equal(got, content) {
		t.Fatalf("stored response: status=%d bytes_equal=%t", resp.StatusCode, bytes.Equal(got, content))
	}
}
