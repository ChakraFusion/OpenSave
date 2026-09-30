package api

import (
	"fmt"
	"net"
	"net/http"
	"strings"
	"testing"

	"github.com/opensave/opensave/internal/daemon"
)

// Another program listening on 127.0.0.1 at OpenSave's port. On Windows the
// daemon's own listen on 0.0.0.0 still succeeds — the more specific address
// wins — so the app, the CLI and the port check all dialled 127.0.0.1 and
// reached that other program. Its web page came back where settings, games
// and scan results should have been, and read as all of them empty: a blank
// device name, no games, first-run settings, and "i is not iterable" from the
// scan. Reported after a BIOS update, which installs vendor utilities that
// serve on localhost.
//
// Starting must notice that 127.0.0.1 is not answered by this daemon, and not
// claim the port.
func TestStartRefusesAPortAnotherProgramAnswersOnLoopback(t *testing.T) {
	other, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	go http.Serve(other, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "<html>some vendor utility</html>")
	}))
	port := other.Addr().(*net.TCPAddr).Port

	d, err := daemon.New(daemon.Options{HomeOverride: t.TempDir(), DisableDiscovery: true})
	if err != nil {
		t.Fatalf("daemon.New: %v", err)
	}
	defer d.Stop()
	if err := d.Start(); err != nil {
		t.Fatalf("daemon.Start: %v", err)
	}

	srv := New(d)
	addr, err := srv.Start(port)
	if err == nil {
		srv.Stop()
		t.Fatalf("started on %s, though 127.0.0.1:%d is another program's — everything local would reach it instead", addr, port)
	}

	// And it falls back as the app does, to a port that is its own.
	addr, err = srv.Start(0)
	if err != nil {
		t.Fatalf("fallback: %v", err)
	}
	defer srv.Stop()
	_, p, _ := net.SplitHostPort(addr)
	resp, err := http.Get("http://127.0.0.1:" + p + "/api/status")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK || !strings.Contains(resp.Header.Get("Content-Type"), "json") {
		t.Errorf("the fallback port does not answer as OpenSave: %d %s", resp.StatusCode, resp.Header.Get("Content-Type"))
	}
}
