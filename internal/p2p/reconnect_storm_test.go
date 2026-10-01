package p2p

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/opensave/opensave/internal/e2ee"
)

// A device coming back online syncs everything once, not once per request.
//
// Reported in GitHub #15: a device that was offline sends several requests at
// once when it comes back, and each of them read the stored "offline" before
// any had written "online" — so each one started a sync of every game. The
// log showed some twenty "connected; triggering auto-sync for all games"
// within a second for one device.
func TestAPeerComingBackOnlineStartsOneSyncOfEverything(t *testing.T) {
	// A device paired with keys, as every 2.4 pairing is, and offline. Its
	// requests are signed: an unsigned one is no longer served at all.
	f := newAuthFixture(t, true)
	s := f.store
	peer := f.peer
	peer.Address, peer.Status = "192.0.2.10", "offline"
	if err := s.UpdatePeer(peer); err != nil {
		t.Fatal(err)
	}

	var triggered atomic.Int32
	e := f.engine
	e.Log = func(level, msg string) {
		if strings.Contains(msg, "triggering auto-sync for all games") {
			triggered.Add(1)
		}
	}
	handler := e.requirePairedPeer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	const requests = 20
	var ready, done sync.WaitGroup
	start := make(chan struct{})
	ready.Add(requests)
	done.Add(requests)
	for i := 0; i < requests; i++ {
		req := f.lanRequest(t, http.MethodGet, "/manifest/g1", nil)
		req.Header.Set(lanAuthNonceHeader, fmt.Sprintf("storm-%d", i))
		local, _ := s.GetSettings()
		at, _ := strconv.ParseInt(req.Header.Get(lanAuthTimeHeader), 10, 64)
		req.Header.Set(lanAuthHeader, e2ee.RequestMAC(f.authKey, peer.ID, local.NodeID, req.URL.RequestURI(), http.MethodGet, nil, fmt.Sprintf("storm-%d", i), at))
		req.RemoteAddr = "192.0.2.10:40000"
		go func() {
			defer done.Done()
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
	if p, _ := s.GetPeer(peer.ID); p.Status != "online" {
		t.Errorf("the peer is %q after its requests, want online", p.Status)
	}
}
