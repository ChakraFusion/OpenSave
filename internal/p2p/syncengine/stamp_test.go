package syncengine

import (
	"context"
	"testing"
	"time"
)

// The time a peer's report confirms the two devices the same is the time the
// comparison began, not the time it finished.
//
// A location with no merge base yet counts only the edits made after that
// time as new. RefreshLineage stamped it once it had read both sides and
// written the lineage — seconds later on a slow disk — so an edit made on both
// devices while it ran read as neither having changed, and one replaced the
// other with no conflict raised. CI: TestMultiRootScenario_ALateReportDoesNotHideADivergence
// on Windows, "A=\"B-version\" B=\"B-version\"".
func TestRefreshLineage_StampsWhenTheComparisonBegan(t *testing.T) {
	env := setupEngine(t)
	write(t, env.localDir, "save.dat", "same")
	write(t, env.remoteDir, "save.dat", "same")

	var fetchedAt time.Time
	env.transport.onFetchManifest = func() {
		fetchedAt = time.Now()
		// The rest of the comparison taking a while, as it does on a busy
		// machine: an edit made now is after the peer's side was read.
		time.Sleep(100 * time.Millisecond)
	}
	env.engine.RefreshLineage(context.Background(), "game1", env.peer)

	p, err := env.store.GetPeer(env.peer.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !p.LastSynced.Valid {
		t.Fatal("identical sides were not stamped as synced")
	}
	stamped, err := time.Parse("2006-01-02T15:04:05.000Z", p.LastSynced.String)
	if err != nil {
		t.Fatal(err)
	}
	if fetchedAt.IsZero() {
		t.Fatal("the peer's manifest was never fetched")
	}
	if stamped.After(fetchedAt) {
		t.Errorf("stamped synced at %s, %v after the peer's side was read: an edit made in between would not count as new",
			stamped.Format(time.RFC3339Nano), stamped.Sub(fetchedAt))
	}
}
