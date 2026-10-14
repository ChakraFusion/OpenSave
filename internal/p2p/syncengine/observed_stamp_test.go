package syncengine

import (
	"context"
	"testing"
	"time"
)

// slowPeer is a peer that answers like a busy device: its manifest is read,
// and the answer takes a while to arrive. readAt is when it was read.
type slowPeer struct {
	*fakeTransport
	readAt time.Time
}

func (s *slowPeer) FetchManifest(ctx context.Context, peer Peer, gameID string, q ManifestQuery) (ManifestResponse, error) {
	resp, err := s.fakeTransport.FetchManifest(ctx, peer, gameID, q)
	s.readAt = time.Now()
	time.Sleep(300 * time.Millisecond)
	return resp, err
}

// A report that the two devices agree is stamped with when that was seen, not
// when it was written down.
//
// The stamp is what a save location with no merge base is judged against: an
// edit made after it is new. Taken once the reads were done, it landed after
// any edit made while they were — seconds, on a busy device — and both
// devices' edits then read as older than the sync. One replaced the other with
// no conflict raised (TestMultiRootScenario_ALateReportDoesNotHideADivergence
// on Windows CI, where the report took that long).
func TestRefreshLineage_StampsWhenTheAgreementWasSeen(t *testing.T) {
	env := setupEngine(t)
	write(t, env.localDir, "save.dat", "same")
	write(t, env.remoteDir, "save.dat", "same")
	slow := &slowPeer{fakeTransport: env.transport}
	env.engine.Transport = slow

	env.engine.RefreshLineage(context.Background(), "game1", env.peer)

	p, err := env.store.GetPeer(env.peer.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !p.LastSynced.Valid {
		t.Fatal("two identical saves were not stamped as synced")
	}
	stamped, err := time.Parse("2006-01-02T15:04:05.000Z", p.LastSynced.String)
	if err != nil {
		t.Fatal(err)
	}
	if stamped.After(slow.readAt) {
		t.Errorf("stamped %s, after the peer's save was read at %s: an edit made in between would read as older than the sync",
			stamped.Format(time.RFC3339Nano), slow.readAt.UTC().Format(time.RFC3339Nano))
	}
}
