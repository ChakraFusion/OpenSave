package api

import (
	"context"
	"fmt"
	"html"
	"net"
	"net/http"
	"sync"
	"time"
)

// The OAuth apps are registered with redirect URI http://localhost/callback.
// The original Electron app intercepted that navigation inside its popup
// window; Wails has no popup, so instead we briefly bind localhost:80 and
// catch the redirect ourselves, completing sign-in with zero copy/paste.
// If port 80 can't be bound (IIS, Skype, a Linux system, …) the UI asks for
// the code, or the whole address the browser landed on, to be pasted.

// authCallbackTimeout is how long the listener waits for the redirect. A
// variable so a test can shorten it.
var authCallbackTimeout = 5 * time.Minute

// authListener manages the temporary loopback servers for one sign-in flow.
type authListener struct {
	sync.Mutex
	servers []*http.Server
	// gen counts sign-ins started. A listener's timeout stops it only if no
	// newer sign-in has started since: each attempt used to start a timer that
	// stopped whatever listener was running when it fired, so trying again
	// within five minutes of an abandoned attempt had the old timer close the
	// new listener part-way through, and the browser came back to "unable to
	// connect" (GitHub #39).
	gen uint64
}

var activeAuthListener = &authListener{}

// startAuthCallback binds localhost:80 (v4 and v6 loopback) and completes
// the pending PKCE flow when the provider redirects back. Returns false if
// no loopback listener could be bound.
func (s *Server) startAuthCallback() bool {
	// A fresh sign-in supersedes any listener from a previous attempt.
	activeAuthListener.stop()

	mux := http.NewServeMux()
	mux.HandleFunc("/callback", s.handleBrowserCallback)

	var servers []*http.Server
	var failures []string
	for _, addr := range []string{"127.0.0.1:80", "[::1]:80"} {
		ln, err := net.Listen("tcp", addr)
		if err != nil {
			failures = append(failures, err.Error())
			continue
		}
		srv := &http.Server{Handler: mux, ReadHeaderTimeout: 10 * time.Second}
		go srv.Serve(ln)
		servers = append(servers, srv)
	}
	if len(servers) == 0 {
		s.Daemon.Log.Log("info", fmt.Sprintf("cloud sign-in: could not listen on port 80 to catch the browser's return (%v) — "+
			"paste the code, or the whole address the browser lands on, into OpenSave instead", failures))
		return false
	}

	activeAuthListener.Lock()
	activeAuthListener.gen++
	gen := activeAuthListener.gen
	activeAuthListener.servers = servers
	activeAuthListener.Unlock()
	s.Daemon.Log.Log("info", fmt.Sprintf("cloud sign-in: waiting on http://localhost/callback for %s", authCallbackTimeout))

	// Don't hold port 80 forever if the user abandons the sign-in — but only
	// this attempt's listener: a newer sign-in has its own timer.
	go func() {
		time.Sleep(authCallbackTimeout)
		activeAuthListener.stopIf(gen)
	}()
	return true
}

func (l *authListener) stop() {
	l.Lock()
	servers := l.servers
	l.servers = nil
	l.Unlock()
	for _, srv := range servers {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		_ = srv.Shutdown(ctx)
		cancel()
	}
}

// stopIf stops the listener only while it is still the one sign-in gen
// started.
func (l *authListener) stopIf(gen uint64) {
	l.Lock()
	current := l.gen == gen
	l.Unlock()
	if current {
		l.stop()
	}
}

// handleBrowserCallback receives the provider redirect in the user's
// browser, finishes the token exchange, and tells the app UI over WS.
//
// The listener stays up until a sign-in succeeds or the timeout passes. It
// used to close after the first request of any kind, and a request with no
// code in it also used up the pending sign-in: a reload, a second tab, or a
// stray request, and the real redirect found nothing listening, or an
// expired flow (GitHub #39).
func (s *Server) handleBrowserCallback(w http.ResponseWriter, r *http.Request) {
	code := r.URL.Query().Get("code")
	oauthErr := r.URL.Query().Get("error")

	fail := func(msg string) {
		s.Hub.Broadcast("cloud-auth", map[string]any{"success": false, "error": msg})
		writeCallbackPage(w, false, msg)
	}
	if code == "" && oauthErr == "" {
		// Not the provider's answer: says nothing, and uses nothing up.
		writeCallbackPage(w, false, "This page is where your browser comes back after signing in; there is nothing in this address to finish with.")
		return
	}

	pendingPKCE.Lock()
	provider, verifier := pendingPKCE.provider, pendingPKCE.verifier
	pendingPKCE.provider, pendingPKCE.verifier = "", ""
	pendingPKCE.Unlock()

	if provider == "" {
		fail("This sign-in link has already been used or expired — start again from OpenSave.")
		return
	}
	if oauthErr != "" {
		fail("Sign-in was cancelled or denied (" + oauthErr + ").")
		return
	}
	s.Daemon.Log.Log("info", "cloud sign-in: the browser came back with a code; finishing sign-in")

	if err := s.Daemon.Cloud.ExchangeAuthCode(provider, code, verifier); err != nil {
		fail(err.Error())
		return
	}

	cfg, _ := s.Daemon.Store.GetCloudConfig()
	s.Hub.Broadcast("cloud-auth", map[string]any{"success": true, "userEmail": cfg.UserEmail})
	s.Daemon.Log.Log("success", "cloud: connected as "+cfg.UserEmail)
	writeCallbackPage(w, true, cfg.UserEmail)
	go activeAuthListener.stop()
}

// writeCallbackPage renders the little page the user's browser lands on.
func writeCallbackPage(w http.ResponseWriter, ok bool, detail string) {
	// The detail can come from the address itself (a provider's error), so it
	// is text, never markup.
	detail = html.EscapeString(detail)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	icon, title, sub := "✅", "Connected to OpenSave", "Signed in as <strong>"+detail+"</strong>. You can close this tab and return to the app."
	if !ok {
		icon, title, sub = "⚠️", "Sign-in didn't complete", detail+" You can close this tab and try again from OpenSave."
	}
	fmt.Fprintf(w, `<!doctype html><html><head><meta charset="utf-8"><title>%s</title></head>
<body style="margin:0;display:flex;align-items:center;justify-content:center;height:100vh;background:#0c0c0d;color:#e8e8ea;font-family:'Segoe UI',system-ui,sans-serif;">
<div style="text-align:center;max-width:420px;padding:40px;background:#17171a;border:1px solid #303038;border-radius:16px;">
<div style="font-size:3rem;margin-bottom:12px;">%s</div>
<h2 style="margin:0 0 10px;">%s</h2>
<p style="color:#9a9aa3;font-size:0.95rem;line-height:1.5;">%s</p>
</div></body></html>`, title, icon, title, sub)
}
