package p2p

import (
	"context"
	"testing"
	"time"
)

// A request waits wanRequestTimeout for its reply, however long the caller's
// own deadline: a sync runs under 30 minutes, and a request or reply the relay
// dropped kept the sync waiting all of that, where it should have failed and
// been retried.
func TestReplyWait_NotTheCallersWholeDeadline(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	if got := replyWait(ctx); got != wanRequestTimeout {
		t.Errorf("under a 30-minute deadline a reply is waited for %v, want %v", got, wanRequestTimeout)
	}
	if got := replyWait(context.Background()); got != wanRequestTimeout {
		t.Errorf("with no deadline a reply is waited for %v, want %v", got, wanRequestTimeout)
	}
}

// A request whose answer legitimately takes longer asks for it, and gets it;
// asking for less than the floor changes nothing.
func TestReplyWait_LongerWhenAskedFor(t *testing.T) {
	if got := replyWait(withReplyWait(context.Background(), 3*time.Minute)); got != 3*time.Minute {
		t.Errorf("asked for 3m, waits %v", got)
	}
	if got := replyWait(withReplyWait(context.Background(), time.Second)); got != wanRequestTimeout {
		t.Errorf("asked for 1s, waits %v; want the floor %v", got, wanRequestTimeout)
	}
}

// Every frame from a paired peer is presence; only a change, or a last-seen
// time going stale, is written. A write per frame held up the one goroutine
// that reads the relay behind the disk.
func TestTrackPresence_WritesOnlyWhenDue(t *testing.T) {
	f := newRaceFixture(t, false)
	peer, err := f.s.GetPeer(f.peer.ID)
	if err != nil {
		t.Fatal(err)
	}
	peer.Status = "online"
	peer.Address = "relay"
	seen := time.Now().Add(-time.Second).UnixMilli()
	peer.LastSeenMs = seen
	if err := f.s.UpdatePeer(peer); err != nil {
		t.Fatal(err)
	}
	w := &WanClient{engine: f.e, discovered: map[string]WanPeer{}}
	ping := RelayMessage{Type: "ping", From: f.peer.ID, Port: peer.Port}

	w.trackPresence(ping)
	if got, _ := f.s.GetPeer(f.peer.ID); got.LastSeenMs != seen {
		t.Errorf("a heartbeat right after the last write was written again (last seen %d → %d)", seen, got.LastSeenMs)
	}

	stale := time.Now().Add(-wanPresenceWriteEvery - time.Second).UnixMilli()
	peer.LastSeenMs = stale
	if err := f.s.UpdatePeer(peer); err != nil {
		t.Fatal(err)
	}
	w.trackPresence(ping)
	if got, _ := f.s.GetPeer(f.peer.ID); got.LastSeenMs <= stale {
		t.Errorf("a stale last-seen time was not refreshed (%d)", got.LastSeenMs)
	}

	w.trackPresence(RelayMessage{Type: "ping", From: f.peer.ID, Port: peer.Port + 1})
	if got, _ := f.s.GetPeer(f.peer.ID); got.Port != peer.Port+1 {
		t.Errorf("a new port was not written at once: %d, want %d", got.Port, peer.Port+1)
	}
}
