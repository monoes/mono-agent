package extension

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// fakeRelay answers /monoagent/relay after delay with resp, and records the
// timeout_ms it was asked for.
func fakeRelay(t *testing.T, delay time.Duration, resp Response) (*httptest.Server, *string) {
	t.Helper()
	var asked string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		asked = r.URL.Query().Get("timeout_ms")
		var cmd Command
		_ = json.NewDecoder(r.Body).Decode(&cmd)
		select {
		case <-time.After(delay):
		case <-r.Context().Done():
			return
		}
		resp.ID = cmd.ID
		_ = json.NewEncoder(w).Encode(resp)
	}))
	t.Cleanup(srv.Close)
	return srv, &asked
}

func TestRemoteSenderHasNoFixedClientCap(t *testing.T) {
	if c := NewRemoteSender("http://127.0.0.1:1").client; c.Timeout != 0 {
		t.Fatalf("relay client has a fixed %s cap", c.Timeout)
	}
}

// A relayed command that legitimately takes longer than any fixed cap
// would allow: here the relay answers after 300ms, well past a simulated
// 100ms "cap", inside the command's own 1s timeout.
func TestRemoteSenderWaitsForTheCommandsOwnTimeout(t *testing.T) {
	relay, asked := fakeRelay(t, 300*time.Millisecond, Response{Success: true, Data: map[string]any{"ok": true}})
	r := &RemoteSender{baseURL: relay.URL, client: &http.Client{}, slack: 100 * time.Millisecond}
	resp, err := r.SendCommand(&Command{Type: CmdPickElement}, time.Second)
	if err != nil || !resp.Success {
		t.Fatalf("slow relay failed: %v", err)
	}
	if *asked != "1000" {
		t.Fatalf("relay asked for timeout_ms=%s", *asked)
	}
}

func TestRelayedPickTimeoutIsATimeoutNotADeadBridge(t *testing.T) {
	relay, _ := fakeRelay(t, time.Hour, Response{})
	r := &RemoteSender{baseURL: relay.URL, client: &http.Client{}, slack: 50 * time.Millisecond}
	start := time.Now()
	_, _, err := NewExtensionPage(r, 3).PickElement(context.Background(), "x", 100*time.Millisecond)
	if !errors.Is(err, ErrPickTimeout) {
		t.Fatalf("err = %v, want ErrPickTimeout", err)
	}
	// pick's own slack (5s) + the relay's (50ms) past the 100ms timeout.
	if took := time.Since(start); took < pickSlack || took > pickSlack+5*time.Second {
		t.Fatalf("gave up after %s, want timeout + pick slack + relay slack", took)
	}
	// And a plain command reports a timeout in Server.SendCommand's words.
	_, err = r.SendCommand(&Command{Type: CmdWaitLoad}, 50*time.Millisecond)
	if err == nil || !strings.Contains(err.Error(), "command wait_load timed out") {
		t.Fatalf("err = %v", err)
	}
}

func TestRelayedPickErrorsFromTheOwningBridge(t *testing.T) {
	for msg, want := range map[string]error{
		"cancelled":              ErrPickCancelled,
		"timeout":                ErrPickTimeout,
		"no extension connected": ErrBridgeNotConnected,
		"command pick_element timed out after 3m5s": ErrPickTimeout,
	} {
		relay, _ := fakeRelay(t, 0, Response{Success: false, Error: msg})
		r := &RemoteSender{baseURL: relay.URL, client: &http.Client{}}
		if _, _, err := NewExtensionPage(r, 3).PickElement(context.Background(), "x", time.Second); !errors.Is(err, want) {
			t.Errorf("relay error %q → %v, want %v", msg, err, want)
		}
	}
}

func TestRelayUnreachableIsNotConnected(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	l.Close() // nothing listens here now: connection refused
	r := &RemoteSender{baseURL: "http://" + addr, client: &http.Client{}}
	if _, _, err := NewExtensionPage(r, 3).PickElement(context.Background(), "x", time.Second); !errors.Is(err, ErrBridgeNotConnected) {
		t.Fatalf("err = %v, want ErrBridgeNotConnected", err)
	}
}
