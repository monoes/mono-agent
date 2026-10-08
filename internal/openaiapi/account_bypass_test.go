package openaiapi

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	keyring "github.com/zalando/go-keyring"

	"github.com/monoes/mono-agent/internal/account/accounttest"
	"github.com/monoes/mono-agent/internal/httpapi"
	"github.com/monoes/mono-agent/internal/monomind"
)

// rawRequest writes one request verbatim on a fresh TCP connection and returns
// the status code. It is the way to send what net/http's client would normalise
// away: a double slash, an absolute-URI target, an HTTP/1.0 request line, an
// Upgrade header.
func rawRequest(t *testing.T, addr, request string) int {
	t.Helper()
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err := conn.Write([]byte(request)); err != nil {
		t.Fatal(err)
	}
	resp, err := http.ReadResponse(bufio.NewReader(conn), nil)
	if err != nil {
		t.Fatalf("%q: %v", request, err)
	}
	resp.Body.Close()
	return resp.StatusCode
}

// Account gate B3b, Task 9 (issue #368): no spelling of a request reaches work on
// either listener while the account is locked. Every request below carries a
// valid /v1 key, so the account is the only thing standing in the way; a path or
// header trick that the door misjudges but the mux routes would show as a started
// turn or an org delivery, or as a 2xx.
func TestNoSpellingOfARequestReachesWorkWhileLocked(t *testing.T) {
	keyring.MockInit()
	var turns, org atomic.Int32
	h := newHarness(t, func(ctx context.Context, o monomind.ExecOptions, on func(monomind.Event)) (*monomind.TurnResult, error) {
		turns.Add(1)
		return okTurn("x")(ctx, o, on)
	})
	key := h.key(t, "default", "app", false)
	srv, err := httpapi.NewServer(httpapi.Options{
		DB: h.db, Profile: "default", Version: "bypass-test", AllowMutations: true,
		ExtraRoutes: func(mux *http.ServeMux) {
			mux.HandleFunc("POST /org-endpoint/{id}", func(w http.ResponseWriter, _ *http.Request) { org.Add(1) })
			h.g.Mount(mux, anyPolicy)
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()
	main := httptest.NewServer(srv.Handler())
	defer main.Close()
	dedicated := httptest.NewServer(h.g.Handler(anyPolicy))
	defer dedicated.Close()

	hdr := "Host: x\r\nAuthorization: Bearer " + key + "\r\nContent-Length: " + fmt.Sprint(len(chatBody)) + "\r\nConnection: close\r\n"
	req := func(method, target, extra string) string {
		return method + " " + target + " HTTP/1.1\r\n" + hdr + extra + "\r\n" + chatBody
	}
	chat, org1 := "/v1/chat/completions", "/org-endpoint/x"
	attempts := []struct{ name, raw string }{
		{"plain", req("POST", chat, "")},
		{"trailing slash", req("POST", chat+"/", "")},
		{"double slash", req("POST", "/"+chat, "")},
		{"double slash inside", req("POST", "/v1//chat/completions", "")},
		{"dot segment", req("POST", "/v1/./chat/completions", "")},
		{"dot-dot out of v1", req("POST", "/v1/../org-endpoint/x", "")},
		{"encoded dot-dot", req("POST", "/v1/%2e%2e/org-endpoint/x", "")},
		{"encoded slash", req("POST", "/v1%2Fchat/completions", "")},
		{"encoded letter", req("POST", "/v%31/chat/completions", "")},
		{"upper case", req("POST", "/V1/CHAT/COMPLETIONS", "")},
		{"query string", req("POST", chat+"?x=/health", "")},
		{"encoded fragment", req("POST", chat+"%23/health", "")},
		{"absolute-URI", req("POST", "http://x"+chat, "")},
		{"absolute-URI, health in the query", req("POST", "http://x/org-endpoint/x?/health", "")},
		{"health dot-dot", req("POST", "/health/../org-endpoint/x", "")},
		{"POST on the health path", req("POST", "/health", "")},
		{"HEAD", req("HEAD", chat, "")},
		{"HEAD org", req("HEAD", org1, "")},
		{"OPTIONS", req("OPTIONS", chat, "")},
		{"method override header", req("GET", "/v1/models", "X-HTTP-Method-Override: POST\r\n")},
		{"rewrite headers", req("GET", "/health", "X-Original-URL: /org-endpoint/x\r\nX-Rewrite-URL: /org-endpoint/x\r\n")},
		{"h2c upgrade", req("POST", chat, "Connection: Upgrade, HTTP2-Settings\r\nUpgrade: h2c\r\nHTTP2-Settings: AAMAAABkAAQCAAAAAAIAAAAA\r\n")},
		{"websocket upgrade", req("GET", chat, "Connection: Upgrade\r\nUpgrade: websocket\r\nSec-WebSocket-Version: 13\r\nSec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==\r\n")},
		{"expect continue", req("POST", chat, "Expect: 100-continue\r\n")},
		{"HTTP/1.0", strings.Replace(req("POST", chat, ""), "HTTP/1.1", "HTTP/1.0", 1)},
	}

	for _, c := range []struct {
		name string
		mode accounttest.Mode
	}{{"locked, no login", accounttest.LockedNoLogin}, {"locked, refused", accounttest.LockedRefused}} {
		t.Run(c.name, func(t *testing.T) {
			accounttest.Install(t, c.mode)
			for _, l := range []struct{ name, addr string }{{"main", main.Listener.Addr().String()}, {"dedicated", dedicated.Listener.Addr().String()}} {
				for _, a := range attempts {
					if code := rawRequest(t, l.addr, a.raw); code >= 200 && code < 300 && a.name != "rewrite headers" { // GET /health is open
						t.Errorf("%s listener, %s: answered %d while locked", l.name, a.name, code)
					}
				}
			}
			if turns.Load() != 0 || org.Load() != 0 {
				t.Fatalf("work started while locked: %d turns, %d org deliveries", turns.Load(), org.Load())
			}
		})
	}

	// The same requests are not dead ones: signed in, the plain spellings reach
	// their handlers, so the zero above is the door's doing.
	accounttest.Install(t, accounttest.SignedIn)
	if code := rawRequest(t, main.Listener.Addr().String(), req("POST", chat, "")); code != http.StatusOK {
		t.Fatalf("signed in, POST %s = %d", chat, code)
	}
	rawRequest(t, main.Listener.Addr().String(), req("POST", org1, ""))
	if turns.Load() == 0 || org.Load() == 0 {
		t.Fatalf("signed in, the probes were not reached (%d turns, %d deliveries): the test proves nothing", turns.Load(), org.Load())
	}
}
