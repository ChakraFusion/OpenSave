package presets

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

// The three answers an App ID lookup can give, and why they must stay apart.
//
// Someone typing an App ID and getting no cover cannot tell a typo from a
// network that blocks Steam from a game Steam simply has no art for, and each
// calls for different advice. This is the function that tells them apart; if
// it ever collapses two of them, the interface goes back to shrugging.

func stubSteam(t *testing.T, handler http.HandlerFunc) {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	previous := steamStoreAPI
	steamStoreAPI = srv.URL
	t.Cleanup(func() { steamStoreAPI = previous })
}

func TestLookupSteamApp_ReturnsTheStoreTitle(t *testing.T) {
	stubSteam(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("appids") != "1091500" {
			t.Errorf("asked about %q, want 1091500", r.URL.Query().Get("appids"))
		}
		w.Write([]byte(`{"1091500":{"success":true,"data":{"name":"Cyberpunk 2077"}}}`))
	})
	name, err := LookupSteamApp("1091500")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if name != "Cyberpunk 2077" {
		t.Errorf("name = %q, want the store title", name)
	}
}

func TestLookupSteamApp_UnknownIDIsNotAnOutage(t *testing.T) {
	// This is exactly what Steam returns for a number that is not an app.
	stubSteam(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"999999999":{"success":false}}`))
	})
	_, err := LookupSteamApp("999999999")
	if !errors.Is(err, ErrSteamAppUnknown) {
		t.Errorf("a number Steam does not know returned %v; want ErrSteamAppUnknown, so the "+
			"person is told to check the number rather than their network", err)
	}
}

func TestLookupSteamApp_UnreachableIsNotAnUnknownID(t *testing.T) {
	// A server that is not there: the closest stand-in for a blocked network.
	srv := httptest.NewServer(http.NotFoundHandler())
	url := srv.URL
	srv.Close()
	previous := steamStoreAPI
	steamStoreAPI = url
	t.Cleanup(func() { steamStoreAPI = previous })

	_, err := LookupSteamApp("1091500")
	if err == nil {
		t.Fatal("no error from a server that is not running")
	}
	if errors.Is(err, ErrSteamAppUnknown) {
		t.Error("a network failure was reported as an unknown App ID; the person would be " +
			"told to check a number that was right all along")
	}
}

// The scanner's name resolution rides on the same call and must keep its
// "empty on any failure" contract, or a scan on a blocked network would
// start showing errors in place of folder names.
func TestFetchSteamAppName_StillEmptyOnFailure(t *testing.T) {
	stubSteam(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	})
	if got := fetchSteamAppName("1091500"); got != "" {
		t.Errorf("fetchSteamAppName returned %q on a failure; want empty", got)
	}
}

// Steam answers some App IDs under another key: asked about 1245620 (Elden
// Ring), it keys the answer "2855530", an edition of it, and says in the
// answer that it is steam_appid 1245620. Read only under the id asked about,
// that answer said "no Steam game has this App ID" of a game everyone knows.
func TestLookupSteamApp_AnAnswerKeyedByAnotherIDStillCounts(t *testing.T) {
	stubSteam(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"2855530":{"success":true,"data":{"type":"game","name":"ELDEN RING","steam_appid":1245620}}}`))
	})
	name, err := LookupSteamApp("1245620")
	if err != nil {
		t.Fatalf("a real game reported as %v", err)
	}
	if name != "ELDEN RING" {
		t.Errorf("name = %q", name)
	}
}

// But an answer about some other app is not an answer about this one.
func TestLookupSteamApp_AnAnswerAboutAnotherAppIsNot(t *testing.T) {
	stubSteam(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"2855530":{"success":true,"data":{"type":"game","name":"Something Else","steam_appid":2855530}}}`))
	})
	if _, err := LookupSteamApp("1245620"); !errors.Is(err, ErrSteamAppUnknown) {
		t.Errorf("err = %v, want ErrSteamAppUnknown", err)
	}
}
