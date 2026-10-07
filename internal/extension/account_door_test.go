package extension

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/monoes/mono-agent/internal/account/accounttest"
	"github.com/monoes/mono-agent/internal/accountdoor/doortest"
	"github.com/monoes/mono-agent/internal/capture"
)

const loginRequiredText = "Log in to monoes.me first: monoagentcli account login"

// settledByID reads n settling replies and indexes them by request id: the
// handlers run on goroutines, so the replies do not come back in order.
func (f *fakeExtension) settledByID(n int) map[string]*Reply {
	f.t.Helper()
	out := map[string]*Reply{}
	for i := 0; i < n; i++ {
		r := f.settled()
		out[r.ID] = r
	}
	return out
}

// wantLocked fails unless r is the refusal of a locked bridge.
func wantLocked(t *testing.T, what string, r *Reply) {
	t.Helper()
	if r.OK || r.Code != CodeAccountLocked || r.Error != loginRequiredText {
		t.Errorf("%s = ok %v code %q error %q, want the account_locked refusal", what, r.OK, r.Code, r.Error)
	}
}

// Every extension request but ping is refused while locked, with the code
// account_locked and the sentence to act on, before its handler is looked up (an
// unknown method gets the same answer). ping answers, and says why; it is matched
// exactly, so another spelling of ping is just another method.
func TestRequestsAreRefusedWhileLocked(t *testing.T) {
	for _, c := range doortest.Modes {
		t.Run(c.Name, func(t *testing.T) {
			srv, ext, _ := startCaptureServer(t)
			var ran atomic.Int32
			srv.HandleRequest("echo", func(context.Context, *Request, ProgressFunc) (any, error) {
				ran.Add(1)
				return "hi", nil
			})
			accounttest.Install(t, c.Mode)

			ids := map[string]string{"echo": "r-echo", MethodPing: "r-ping", "nope.unknown": "r-unknown", "Ping": "r-case", "ping ": "r-space"}
			if c.Refused { // the built-ins shell out to monomind when they run: sent only where they are refused at once
				ids[MethodDocLookup], ids["record.list"] = "r-lookup", "r-record"
			}
			for method, id := range ids {
				ext.ask(id, method, nil)
			}
			replies := ext.settledByID(len(ids))

			data, _ := replies["r-ping"].Data.(map[string]any)
			acct, _ := data["account"].(map[string]any)
			b, _ := json.Marshal(data)
			if !replies["r-ping"].OK || data["pong"] != true || acct["state"] == nil || strings.Contains(string(b), "email") || strings.Contains(string(b), `"user"`) {
				t.Fatalf("ping = %s, want pong and an account that does not name the user", b)
			}
			if c.State != "" && (acct["state"] != c.State || acct["reason"] != c.Reason) {
				t.Errorf("ping account = %v, want %s/%s", acct, c.State, c.Reason)
			}
			if acct["enforced"] != c.Enforced {
				t.Errorf("ping account = %v, want enforced = %v", acct, c.Enforced)
			}
			for method, id := range ids {
				switch {
				case method == MethodPing:
				case c.Refused:
					wantLocked(t, method, replies[id])
				case replies[id].Code == CodeAccountLocked:
					t.Errorf("%q was refused for the account in state %s", method, c.Name)
				}
			}
			want := int32(1)
			if c.Refused {
				want = 0
			}
			if ran.Load() != want {
				t.Errorf("the handler ran %d times, want %d", ran.Load(), want)
			}
		})
	}
}

// A person who signs in expects the open side panel to work at once: the same
// socket, no reconnect.
func TestABridgeSocketRecoversWhenTheAccountDoes(t *testing.T) {
	srv, ext, _ := startCaptureServer(t)
	srv.HandleRequest("echo", func(context.Context, *Request, ProgressFunc) (any, error) { return "hi", nil })

	t.Run("locked", func(t *testing.T) {
		accounttest.Install(t, accounttest.LockedRefused)
		ext.ask("r1", "echo", nil)
		wantLocked(t, "echo while locked", ext.settled())
	})
	t.Run("the sign-in lands", func(t *testing.T) {
		accounttest.Install(t, accounttest.SignedIn)
		ext.ask("r2", "echo", nil)
		if r := ext.settled(); !r.OK || r.Data != "hi" {
			t.Fatalf("echo after the sign-in = %+v, want ok", r)
		}
	})
}

// bridgeAddr is where a started server listens, and the token its clients use.
func bridgeAddr(t *testing.T, srv *Server) (addr, token string) {
	t.Helper()
	addr, ok := srv.Addr()
	if !ok {
		t.Fatal("the server has no address")
	}
	token, err := CurrentToken()
	if err != nil {
		t.Fatal(err)
	}
	return addr, token
}

// relayCreateTab relays a command no extension answers: the short timeout ends
// an allowed one with the relay's own error.
func relayCreateTab(t *testing.T, addr, token string) (int, Response) {
	t.Helper()
	cmd, _ := json.Marshal(&Command{ID: "c1", Type: CmdCreateTab})
	req, _ := http.NewRequest(http.MethodPost, "http://"+addr+"/monoagent/relay?timeout_ms=200", bytes.NewReader(cmd))
	req.Header.Set(tokenHeader, token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out Response
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out
}

// The relay lets another process drive the browser through this one: refused
// while locked, after the token (a caller without it learns nothing of the
// account). A 503, not a 401: the relay client reads a 401 as a pairing-token
// mismatch and would tell the user to re-pair.
func TestRelayIsRefusedWhileLocked(t *testing.T) {
	for _, c := range doortest.Modes {
		t.Run(c.Name, func(t *testing.T) {
			srv, _, _ := startCaptureServer(t)
			addr, tok := bridgeAddr(t, srv)
			accounttest.Install(t, c.Mode)

			code, got := relayCreateTab(t, addr, tok)
			if refused := code == http.StatusServiceUnavailable && got.Error == loginRequiredText; refused != c.Refused {
				t.Errorf("relay = %d %+v, want refused = %v (503 with the login-required sentence)", code, got, c.Refused)
			}
			if code, _ := relayCreateTab(t, addr, "not-the-token"); code != http.StatusUnauthorized {
				t.Errorf("relay without the token = %d, want 401 and nothing about the account", code)
			}
		})
	}
}

// The CDP socket reaches the signed-in tabs through chrome.debugger. A locked
// bridge refuses the upgrade with a plain 503; a socket opened before the lock is
// refused command by command (the extension never sees them) and works again, on
// the same socket, once a login lands.
func TestCDPIsRefusedWhileLocked(t *testing.T) {
	rig := startCdpRig(t)
	envelope := func(id string) map[string]any {
		return map[string]any{"id": id, "type": CmdCdp, "tabId": 42, "params": map[string]any{"method": "Page.navigate"}}
	}
	var client *websocket.Conn

	t.Run("a locked bridge refuses the upgrade", func(st *testing.T) {
		accounttest.Install(st, accounttest.LockedNoLogin)
		h := http.Header{}
		h.Set(tokenHeader, rig.token(st))
		conn, resp, err := websocket.DefaultDialer.Dial("ws://127.0.0.1:"+strconv.Itoa(rig.port)+"/monoagent/cdp", h)
		if err == nil || resp == nil || resp.StatusCode != http.StatusServiceUnavailable {
			st.Fatalf("cdp upgrade: err %v resp %v, want 503", err, resp)
		}
		if conn != nil {
			conn.Close()
		}
	})
	t.Run("signed in: the socket opens", func(st *testing.T) {
		accounttest.Install(st, accounttest.SignedIn)
		client = rig.dialCdp(t, rig.token(t)) // t, not st: the socket outlives this subtest
	})
	t.Run("the account locks under the open socket", func(st *testing.T) {
		accounttest.Install(st, accounttest.LockedRefused)
		_ = client.WriteJSON(envelope("cdp-1"))
		var refused struct{ ID, Error string }
		readJSON(st, client, &refused)
		if refused.ID != "cdp-1" || refused.Error != loginRequiredText {
			st.Fatalf("command on an open socket = %+v, want the login-required refusal", refused)
		}
		if n := rig.srv.inFlightCommands(); n != 0 {
			st.Fatalf("%d commands were sent to the extension for a refused one", n)
		}
	})
	t.Run("the sign-in lands", func(st *testing.T) {
		accounttest.Install(st, accounttest.SignedIn)
		_ = client.WriteJSON(envelope("cdp-2"))
		if cmd := rig.answerExtension(st, map[string]any{"ok": true}, ""); cmd.Type != CmdCdp {
			st.Fatalf("the extension saw %q after the sign-in, want the cdp command", cmd.Type)
		}
		var ok struct {
			ID      string
			Success bool
		}
		readJSON(st, client, &ok)
		if ok.ID != "cdp-2" || !ok.Success {
			st.Fatalf("after the sign-in = %+v, want the command answered", ok)
		}
	})
}

// The extension pushes a capture it queued, and the frames of a recording, with
// no answer, and drops its queued copy once the socket takes the frame
// (chrome-extension/capture_bridge.js:111-117): refusing a push would lose it
// without a trace, and installed extensions cannot be updated centrally. So a
// locked bridge keeps accepting them; refusing pushes later needs an extension
// release first.
func TestPushedCapturesAndRecordingsAreAcceptedWhileLocked(t *testing.T) {
	srv, ext, inbox := startCaptureServer(t)
	landed := make(chan struct{}, 1)
	srv.OnCapture(func(_ *capture.Result, err error) {
		if err == nil {
			landed <- struct{}{}
		}
	})
	accounttest.Install(t, accounttest.LockedNoLogin)

	ext.sendFinal("ext-pushed-1", sampleMeta(), b64Artifact(capture.ArtifactReadable, "# A Post"))
	select {
	case <-landed:
	case <-time.After(5 * time.Second):
		t.Fatal("a capture pushed to a locked bridge was dropped")
	}
	if entries, err := capture.List(inbox); err != nil || len(entries) != 1 {
		t.Fatalf("inbox = %+v, %v, want the pushed capture", entries, err)
	}

	ext.send(recFrame("rec-1", "start", map[string]any{"url": "https://example.com/x", "title": "X", "goal": "g", "tabId": 3}))
	if ack := ext.nextAck("rec-1"); !ack.Success {
		t.Fatalf("a recording frame sent to a locked bridge was refused: %s", ack.Error)
	}
}

// The endpoints that stay open while locked, pinned: a new one is added here on
// purpose. The handshake is open so that ping can reach the browser, the probes
// because other processes use them to find this bridge, and the two read-only
// routes (which browsers are connected, which one a profile resolves to) behind
// the token.
func TestTheOpenEndpointsOfALockedBridge(t *testing.T) {
	srv, _, _ := startCaptureServer(t)
	addr, tok := bridgeAddr(t, srv)
	accounttest.Install(t, accounttest.LockedNoLogin)

	for path, want := range map[string]int{
		"/monoagent/health": http.StatusOK, "/monoagent/auth": http.StatusNoContent,
		"/monoagent/browsers": http.StatusOK, "/monoagent/resolve": http.StatusOK,
		"/monoagent/pair": http.StatusBadRequest, "/monoagent/pair/exchange?n=nope": http.StatusNotFound, // the pages' own checks
	} {
		req, _ := http.NewRequest(http.MethodGet, "http://"+addr+path, nil)
		req.Header.Set(tokenHeader, tok)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("GET %s: %v", path, err)
		}
		resp.Body.Close()
		if resp.StatusCode != want {
			t.Errorf("GET %s = %d, want %d (open while locked)", path, resp.StatusCode, want)
		}
	}
	if !srv.IsConnected() {
		t.Error("the extension is not connected to a locked bridge: the socket must stay open for ping")
	}
}
