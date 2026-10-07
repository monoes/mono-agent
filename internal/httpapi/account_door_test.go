package httpapi

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/monoes/mono-agent/internal/account/accounttest"
	"github.com/monoes/mono-agent/internal/accountdoor/doortest"
	"github.com/monoes/mono-agent/internal/secrets"
)

// wantLoginRequired fails unless rec is exactly the refusal of index §3.4 item 5:
// 401, {"error":"login_required","login_required":true,"account":{"state","reason"}}.
func wantLoginRequired(t *testing.T, rec *httptest.ResponseRecorder, state, reason string) {
	t.Helper()
	var body map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	acct, _ := body["account"].(map[string]any)
	if rec.Code != http.StatusUnauthorized || rec.Header().Get("WWW-Authenticate") != "" ||
		body["error"] != "login_required" || body["login_required"] != true ||
		acct["state"] != state || acct["reason"] != reason || len(body) != 3 || len(acct) != 2 {
		t.Errorf("got %d %s (WWW-Authenticate %q), want the login_required 401 for %s/%s",
			rec.Code, rec.Body, rec.Header().Get("WWW-Authenticate"), state, reason)
	}
}

// A locked account is refused whatever bearer the request carries; in any other
// state the bearer rules apply as before.
func TestAuthenticatedRoutesAreRefusedWhileLocked(t *testing.T) {
	s := newTestServer(t, false)
	good := testToken(t, s)
	for _, c := range doortest.Modes {
		t.Run(c.Name, func(t *testing.T) {
			accounttest.Install(t, c.Mode)
			for _, bearer := range []string{good, "wrong", ""} {
				rec := doReq(t, s, http.MethodGet, "/workflows", bearer, nil)
				switch {
				case c.Refused:
					wantLoginRequired(t, rec, c.State, c.Reason)
				case bearer == good && rec.Code != http.StatusOK:
					t.Errorf("good bearer: %d, want 200", rec.Code)
				case bearer != good && (rec.Code != http.StatusUnauthorized || rec.Header().Get("WWW-Authenticate") == ""):
					t.Errorf("bearer %q: %d, want the bearer's own 401", bearer, rec.Code)
				}
			}
		})
	}
}

// /health answers 200 in every state, to a caller with no credential, and says
// what the account is: the state and why, never who.
func TestHealthIsOpenInEveryStateAndReportsTheAccount(t *testing.T) {
	s := newTestServer(t, false)
	for _, c := range doortest.Modes {
		t.Run(c.Name, func(t *testing.T) {
			accounttest.Install(t, c.Mode)
			if rec := doReq(t, s, http.MethodHead, "/health", "", nil); rec.Code != http.StatusOK {
				t.Fatalf("HEAD /health = %d, want 200", rec.Code)
			}
			rec := doReq(t, s, http.MethodGet, "/health", "", nil)
			var body struct {
				Status, Version string
				Account         map[string]any
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil || rec.Code != http.StatusOK ||
				body.Status != "ok" || body.Version != "test" || body.Account["state"] == nil {
				t.Fatalf("GET /health = %d %s, want 200 with the old fields and an account", rec.Code, rec.Body)
			}
			if c.State != "" && (body.Account["state"] != c.State || body.Account["reason"] != c.Reason) {
				t.Errorf("account = %v, want %s/%s", body.Account, c.State, c.Reason)
			}
			if body.Account["enforced"] != c.Enforced {
				t.Errorf("account = %v, want enforced = %v", body.Account, c.Enforced)
			}
			if strings.Contains(rec.Body.String(), `"user"`) || strings.Contains(rec.Body.String(), "example.test") {
				t.Errorf("health names the user: %s", rec.Body)
			}
		})
	}
}

// Nothing but /health is open on a locked server. A path the API does not have
// (or a mutating one that is not registered) is refused like any other: a 404
// would tell a caller which routes exist. Odd spellings do not slip past, HEAD
// and OPTIONS are no way round, and the /v1 prefix is none either: the mux only
// redirects a dot-segment after it, and takes an encoded slash (v1%2F) for part of
// one segment, which is no path under /v1.
func TestLockedServerRefusesEveryPathButHealth(t *testing.T) {
	paths := []struct{ method, path string }{
		{"GET", "/workflows"}, {"POST", "/workflows/x/run"}, {"POST", "/hil/x/approve"}, {"GET", "/nodes/x/schema"},
		{"POST", "/org-endpoint/x"}, {"GET", "/nope"}, {"POST", "/health"}, {"GET", "/health/"},
		{"GET", "//workflows"}, {"GET", "/./workflows"}, {"GET", "/WORKFLOWS"}, {"GET", "/v1x"},
		{"HEAD", "/nope"}, {"OPTIONS", "/workflows"}, {"OPTIONS", "/health"},
		{"GET", "/v1%2Fmodels"}, {"GET", "/v1%2F..%2Fworkflows"},
	}
	for _, server := range []struct {
		name      string
		mutations bool
	}{{"read-only", false}, {"mutations allowed", true}} {
		t.Run(server.name, func(t *testing.T) {
			s := newTestServer(t, server.mutations)
			tok := testToken(t, s)
			accounttest.Install(t, accounttest.LockedNoLogin)
			for _, p := range paths {
				wantLoginRequired(t, doReq(t, s, p.method, p.path, tok, nil), "locked", "not_logged_in")
			}
			rec := doReq(t, s, http.MethodGet, "/v1/../workflows", tok, nil)
			if loc := rec.Header().Get("Location"); rec.Code == http.StatusOK || loc == "" {
				t.Fatalf("GET /v1/../workflows = %d, want a redirect", rec.Code)
			} else {
				wantLoginRequired(t, doReq(t, s, http.MethodGet, loc, tok, nil), "locked", "not_logged_in")
			}
		})
	}
}

// A route registered through ExtraRoutes brings its own authentication, so auth
// cannot wrap it: one that forgot a door is still refused by the gate that
// NewServer puts in front of the mux. HEAD matches a GET pattern, so it is called
// too.
func TestARouteMountedThroughExtraRoutesWithNoDoorIsRefused(t *testing.T) {
	var reached int
	s, err := NewServer(Options{
		DBPath:       filepath.Join(t.TempDir(), "httpapi-test.db"),
		Profile:      "default",
		WorkflowsDir: filepath.Join(t.TempDir(), "workflows"),
		Addr:         "127.0.0.1:0",
		Version:      "test",
		ExtraRoutes: func(mux *http.ServeMux) {
			mux.HandleFunc("GET /extra", func(http.ResponseWriter, *http.Request) { reached++ })
		},
	})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	t.Cleanup(s.Close)
	for _, c := range doortest.Modes {
		t.Run(c.Name, func(t *testing.T) {
			reached = 0
			accounttest.Install(t, c.Mode)
			rec := doReq(t, s, http.MethodGet, "/extra", "", nil)
			head := doReq(t, s, http.MethodHead, "/extra", "", nil)
			want := 2
			if c.Refused {
				wantLoginRequired(t, rec, c.State, c.Reason)
				wantLoginRequired(t, head, c.State, c.Reason)
				want = 0
			}
			if reached != want {
				t.Fatalf("the extra route ran %d times, want %d", reached, want)
			}
		})
	}
}

// auth carries its own check, so a handler it wraps never relies on the gate: a
// locked account is refused before the vault is opened (no bearer token is
// minted for a locked server) and the handler does not run.
func TestAuthRefusesALockedAccountBeforeTheVault(t *testing.T) {
	s := newTestServer(t, false)
	accounttest.Install(t, accounttest.LockedNoLogin)

	var ran bool
	rec := httptest.NewRecorder()
	s.auth(func(http.ResponseWriter, *http.Request) { ran = true }).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/workflows", nil))

	wantLoginRequired(t, rec, "locked", "not_logged_in")
	entries, err := secrets.List(context.Background(), s.rt.db.DB, s.rt.profileID)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.Name == tokenSecretName {
			t.Fatal("a locked server minted its bearer token: the vault was opened for a refused request")
		}
	}
	if ran {
		t.Fatal("the wrapped handler ran for a locked account")
	}
}

// Serve is what runs in production: it must serve the gated handler, not the
// bare mux. A path the API does not have tells them apart: only the gate refuses it.
func TestServeServesTheGatedHandler(t *testing.T) {
	s := newTestServer(t, false)
	accounttest.Install(t, accounttest.LockedNoLogin)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- s.Serve(ctx, ln) }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(15 * time.Second):
			t.Error("Serve did not stop")
		}
	})

	client := &http.Client{Timeout: 10 * time.Second} // a refusal that never came must fail the test, not hang it
	resp, err := client.Get("http://" + ln.Addr().String() + "/nope")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var body map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&body)
	if resp.StatusCode != http.StatusUnauthorized || body["error"] != "login_required" {
		t.Errorf("GET /nope over the listener = %d %v, want the login_required 401", resp.StatusCode, body)
	}
}
