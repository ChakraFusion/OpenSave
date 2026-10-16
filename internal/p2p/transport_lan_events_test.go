package p2p

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/opensave/opensave/internal/p2p/syncengine"
)

// Reports reach a peer in the order they were made. A quick sync's "started"
// arriving after its "finished" left the peer showing a sync that was over.
func TestSyncEventReportsArriveInOrder(t *testing.T) {
	var mu sync.Mutex
	var got []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			EventType string `json:"eventType"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		// A little uneven, the way a real network is, so that reports sent
		// concurrently would overtake each other.
		if n, _ := strconv.Atoi(body.EventType[1:]); n%3 == 0 {
			time.Sleep(3 * time.Millisecond)
		}
		mu.Lock()
		got = append(got, body.EventType)
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	host, portStr, _ := net.SplitHostPort(srv.Listener.Addr().String())
	port, _ := strconv.Atoi(portStr)
	peer := syncengine.Peer{ID: "peer1", Name: "Deck", Address: host, Port: port}

	tr := &lanTransport{}
	const n = 40
	for i := 0; i < n; i++ {
		tr.ReportSyncEvent(peer, "hades", fmt.Sprintf("e%d", i), nil)
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		mu.Lock()
		done := len(got) == n
		mu.Unlock()
		if done || time.Now().After(deadline) {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(got) != n {
		t.Fatalf("received %d of %d reports", len(got), n)
	}
	for i, e := range got {
		if e != fmt.Sprintf("e%d", i) {
			t.Fatalf("report %d arrived as %s: %v", i, e, got)
		}
	}
}

// A report that carries state is not lost to one slow answer. "sync-complete"
// tells the other device what both now hold; sent once, with a short limit, a
// device busy for a moment missed it, kept a base a round behind, and read
// the next save as both sides having changed (the Journey test's false
// conflict, upstream CI #385). It is sent again, with the same ID, until it is
// answered; a progress report is still sent once.
func TestSyncEventReportsThatCarryStateAreSentAgain(t *testing.T) {
	short(t, &stateReportTimeout, 200*time.Millisecond)
	short(t, &stateReportBackoff, 50*time.Millisecond)

	var mu sync.Mutex
	var got []string
	ids := map[string]bool{}
	slowFirst := map[string]bool{"sync-complete": true, "sync-progress": true}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			EventType string         `json:"eventType"`
			Data      map[string]any `json:"data"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		mu.Lock()
		slow := slowFirst[body.EventType]
		delete(slowFirst, body.EventType)
		got = append(got, body.EventType)
		if id, _ := body.Data["reportId"].(string); id != "" {
			ids[id] = true
		}
		mu.Unlock()
		if slow {
			time.Sleep(2500 * time.Millisecond) // busy: answered after any limit used
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	host, portStr, _ := net.SplitHostPort(srv.Listener.Addr().String())
	port, _ := strconv.Atoi(portStr)
	peer := syncengine.Peer{ID: "peer1", Name: "Deck", Address: host, Port: port}

	tr := &lanTransport{}
	tr.ReportSyncEvent(peer, "hades", "sync-progress", nil)
	tr.ReportSyncEvent(peer, "hades", "sync-complete", map[string]any{"pulledFiles": []string{"a.sav"}})
	tr.ReportSyncEvent(peer, "hades", "e-after", nil)

	deadline := time.Now().Add(10 * time.Second)
	for {
		mu.Lock()
		n := len(got)
		mu.Unlock()
		if n >= 4 || time.Now().After(deadline) {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	mu.Lock()
	defer mu.Unlock()
	want := []string{"sync-progress", "sync-complete", "sync-complete", "e-after"}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("received %v, want %v: the slow sync-complete sent again, in order; the progress report once", got, want)
	}
	if len(ids) != 1 {
		t.Errorf("the two sends of one report carried %d IDs, want the same one", len(ids))
	}
}

// short lowers a timing for one test.
func short(t *testing.T, v *time.Duration, d time.Duration) {
	t.Helper()
	prev := *v
	*v = d
	t.Cleanup(func() { *v = prev })
}
