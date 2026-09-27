package extension

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// dialBrowser connects one fake extension that reports an instance id, a
// bound profile and a label — what extension ≥1.5 sends — and completes
// the auth handshake with the on-disk token.
func dialBrowser(t *testing.T, wsURL, instance, profile, label string) *websocket.Conn {
	t.Helper()
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	tok, err := CurrentToken()
	if err != nil {
		t.Fatalf("CurrentToken: %v", err)
	}
	if err := conn.WriteJSON(authFrame{Type: "auth", Token: tok, Instance: instance, Profile: profile, Label: label}); err != nil {
		t.Fatalf("write auth frame: %v", err)
	}
	return conn
}

// waitBrowsers polls until exactly n browsers are connected.
func waitBrowsers(t *testing.T, srv *Server, n int) []ConnInfo {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		got := srv.Browsers()
		if len(got) == n {
			return got
		}
		if time.Now().After(deadline) {
			t.Fatalf("browsers = %d (%+v), want %d", len(got), got, n)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

const (
	instA = "aaaaaaaa-0000-4000-8000-000000000001"
	instB = "bbbbbbbb-0000-4000-8000-000000000002"
	instC = "cccccccc-0000-4000-8000-000000000003"
)

func TestTwoBrowsersStayConnected(t *testing.T) {
	srv, base, wsURL, _ := startStatusTestServer(t)
	dialBrowser(t, wsURL, instA, "p-a", "Edge Work")
	waitBrowsers(t, srv, 1)
	dialBrowser(t, wsURL, instB, "p-b", "Chrome Home")
	got := waitBrowsers(t, srv, 2)

	// Newest first.
	if got[0].Instance != instB || got[1].Instance != instA {
		t.Fatalf("order = %s, %s; want B then A", got[0].Instance, got[1].Instance)
	}
	if got[1].Profile != "p-a" || got[1].Label != "Edge Work" {
		t.Fatalf("A = %+v", got[1])
	}
	if st := fetchHealth(t, base); !st.Connected || st.Browsers != 2 {
		t.Fatalf("health = %+v, want connected with 2 browsers", st)
	}
}

func TestSameInstanceReplacesItself(t *testing.T) {
	srv, _, wsURL, _ := startStatusTestServer(t)
	first := dialBrowser(t, wsURL, instA, "p-a", "")
	waitBrowsers(t, srv, 1)
	dialBrowser(t, wsURL, instA, "p-a", "")
	// The old socket is closed by the server once its replacement is in.
	_ = first.SetReadDeadline(time.Now().Add(3 * time.Second))
	for {
		if _, _, err := first.ReadMessage(); err != nil {
			break
		}
	}
	waitBrowsers(t, srv, 1)
}

func TestLegacyExtensionsStillReplaceEachOther(t *testing.T) {
	srv, _, wsURL, _ := startStatusTestServer(t)
	dialAndAuthenticate(t, wsURL)
	waitBrowsers(t, srv, 1)
	dialAndAuthenticate(t, wsURL)
	time.Sleep(100 * time.Millisecond)
	got := waitBrowsers(t, srv, 1)
	if !got[0].Legacy || got[0].Instance != legacyInstanceID {
		t.Fatalf("legacy browser = %+v", got[0])
	}
}

func TestInvalidBindingIsDropped(t *testing.T) {
	srv, _, wsURL, _ := startStatusTestServer(t)
	dialBrowser(t, wsURL, instA, "../etc", "x")
	got := waitBrowsers(t, srv, 1)
	if got[0].Profile != "" {
		t.Fatalf("profile = %q, want it dropped", got[0].Profile)
	}
}

func TestShortInstanceIDCountsAsLegacy(t *testing.T) {
	srv, _, wsURL, _ := startStatusTestServer(t)
	dialBrowser(t, wsURL, "abc", "p-a", "")
	got := waitBrowsers(t, srv, 1)
	if !got[0].Legacy || got[0].Profile != "p-a" {
		t.Fatalf("got %+v, want legacy slot keeping its profile", got[0])
	}
}

func TestResolveRules(t *testing.T) {
	srv, _, wsURL, _ := startStatusTestServer(t)
	dialBrowser(t, wsURL, instA, "p-a", "")
	waitBrowsers(t, srv, 1)
	b := dialBrowser(t, wsURL, instB, "", "")
	waitBrowsers(t, srv, 2)
	dialBrowser(t, wsURL, instC, "p-c", "")
	waitBrowsers(t, srv, 3)

	cases := []struct {
		name string
		t    Target
		want string
	}{
		{"bound profile", Target{Profile: "p-a"}, instA},
		{"unbound fallback", Target{Profile: "p-b"}, instB},
		{"no target prefers unbound", Target{}, instB},
		{"instance", Target{Instance: instC}, instC},
		{"instance beats profile", Target{Profile: "p-a", Instance: instC}, instC},
	}
	for _, tc := range cases {
		info, err := srv.ResolveTarget(tc.t)
		if err != nil || info.Instance != tc.want {
			t.Errorf("%s: got %q, %v; want %q", tc.name, info.Instance, err, tc.want)
		}
	}

	if _, err := srv.ResolveTarget(Target{Instance: "dddddddd-0000-4000-8000-000000000004"}); !errors.Is(err, ErrBrowserGone) {
		t.Errorf("missing instance: err = %v, want ErrBrowserGone", err)
	}

	_ = b.Close()
	waitBrowsers(t, srv, 2)
	_, err := srv.ResolveTarget(Target{Profile: "p-b"})
	var nb *NoBrowserError
	if !errors.As(err, &nb) || nb.Profile != "p-b" || len(nb.BoundTo) != 2 || nb.BoundTo[0] != "p-a" || nb.BoundTo[1] != "p-c" {
		t.Fatalf("no unbound browser left: err = %v", err)
	}
	// With no unbound browser, an untargeted command takes the newest one.
	if info, err := srv.ResolveTarget(Target{}); err != nil || info.Instance != instC {
		t.Fatalf("untargeted = %q, %v; want newest (C)", info.Instance, err)
	}
}

func TestResolveWithNothingConnected(t *testing.T) {
	srv, _, _, _ := startStatusTestServer(t)
	if _, err := srv.ResolveTarget(Target{Profile: "p-a"}); !errors.Is(err, ErrNoExtension) {
		t.Fatalf("err = %v, want ErrNoExtension", err)
	}
}

func TestConflictIsFlagged(t *testing.T) {
	srv, _, wsURL, _ := startStatusTestServer(t)
	dialBrowser(t, wsURL, instA, "p-a", "")
	waitBrowsers(t, srv, 1)
	dialBrowser(t, wsURL, instB, "p-a", "")
	got := waitBrowsers(t, srv, 2)
	if !got[0].Conflict || !got[1].Conflict {
		t.Fatalf("both should be flagged: %+v", got)
	}
	if info, _ := srv.ResolveTarget(Target{Profile: "p-a"}); info.Instance != instB {
		t.Fatalf("conflict resolves to %q, want the newest (B)", info.Instance)
	}
}

func TestRequestReplyGoesBackToTheAskingBrowser(t *testing.T) {
	srv, _, wsURL, _ := startStatusTestServer(t)
	srv.HandleRequest("test.origin", func(_ context.Context, req *Request, _ ProgressFunc) (any, error) {
		return map[string]string{"asker": req.Origin.Instance, "profile": req.Origin.Profile}, nil
	})
	a := dialBrowser(t, wsURL, instA, "p-a", "")
	waitBrowsers(t, srv, 1)
	b := dialBrowser(t, wsURL, instB, "p-b", "")
	waitBrowsers(t, srv, 2)

	if err := b.WriteJSON(map[string]any{"kind": "request", "id": "r1", "method": "test.origin"}); err != nil {
		t.Fatal(err)
	}
	_ = b.SetReadDeadline(time.Now().Add(3 * time.Second))
	_, msg, err := b.ReadMessage()
	if err != nil {
		t.Fatalf("B read reply: %v", err)
	}
	var reply struct {
		ID   string            `json:"id"`
		OK   bool              `json:"ok"`
		Data map[string]string `json:"data"`
	}
	if err := json.Unmarshal(msg, &reply); err != nil || reply.ID != "r1" || !reply.OK ||
		reply.Data["asker"] != instB || reply.Data["profile"] != "p-b" {
		t.Fatalf("reply = %s (%v)", msg, err)
	}
	_ = a.SetReadDeadline(time.Now().Add(300 * time.Millisecond))
	if _, msg, err := a.ReadMessage(); err == nil {
		t.Fatalf("A received %s; the reply belongs to B", msg)
	}
}
