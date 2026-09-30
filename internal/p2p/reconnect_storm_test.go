package p2p

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/opensave/opensave/internal/store"
)

// A device coming back online syncs everything once, not once per request.
//
// Reported in GitHub #15: a device that was offline sends several requests at
// once when it comes back, and each of them read the stored "offline" before
// any had written "online" — so each one started a sync of every game. The
// log showed some twenty "connected; triggering auto-sync for all games"
// within a second for one device.
func TestAPeerComingBackOnlineStartsOneSyncOfEverything(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "opensave.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	if err := s.EnsureDefaultSettings(t.TempDir(), t.TempDir()); err != nil {
		t.Fatal(err)
	}
	// Paired before keys were exchanged, so matched by address.
	if err := s.UpsertPeer(store.Peer{ID: "peer-back", Name: "Back Again", Address: "192.0.2.10", Status: "offline"}); err != nil {
		t.Fatal(err)
	}

	var triggered atomic.Int32
	e := &Engine{Store: s, Log: func(level, msg string) {
		if strings.Contains(msg, "triggering auto-sync for all games") {
			triggered.Add(1)
		}
	}}
	handler := e.requirePairedPeer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	const requests = 20
	var ready, done sync.WaitGroup
	start := make(chan struct{})
	ready.Add(requests)
	done.Add(requests)
	for i := 0; i < requests; i++ {
		go func() {
			defer done.Done()
			req := httptest.NewRequest(http.MethodGet, "/manifest/g1", nil)
			req.RemoteAddr = "192.0.2.10:40000"
			rec := httptest.NewRecorder()
			ready.Done()
			<-start
			handler.ServeHTTP(rec, req)
			if rec.Code != http.StatusOK {
				t.Errorf("request refused with %d: %s", rec.Code, rec.Body.String())
			}
		}()
	}
	ready.Wait()
	close(start)
	done.Wait()

	if got := triggered.Load(); got != 1 {
		t.Errorf("coming back online started %d syncs of every game, want 1", got)
	}
	if p, _ := s.GetPeer("peer-back"); p.Status != "online" {
		t.Errorf("the peer is %q after its requests, want online", p.Status)
	}
}
