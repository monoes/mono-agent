package extension

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeRelayState is what a fakeRelay saw. Handlers run on their own
// goroutines, so it is guarded.
type fakeRelayState struct {
	mu       sync.Mutex
	asked    string // timeout_ms of the latest request
	requests int
}

func (st *fakeRelayState) snapshot() (string, int) {
	st.mu.Lock()
	defer st.mu.Unlock()
	return st.asked, st.requests
}

// fakeRelay answers /monoagent/relay after delay with resp, recording the
// timeout_ms it was asked for and how many requests arrived.
func fakeRelay(t *testing.T, delay time.Duration, resp Response) (*httptest.Server, *fakeRelayState) {
	t.Helper()
	st := &fakeRelayState{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		st.mu.Lock()
		st.asked = r.URL.Query().Get("timeout_ms")
		st.requests++
		st.mu.Unlock()
		var cmd Command
		_ = json.NewDecoder(r.Body).Decode(&cmd)
		select {
		case <-time.After(delay):
		case <-r.Context().Done():
			return
		}
		out := resp
		out.ID = cmd.ID
		_ = json.NewEncoder(w).Encode(out)
	}))
	t.Cleanup(srv.Close)
	return srv, st
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
	relay, st := fakeRelay(t, 300*time.Millisecond, Response{Success: true, Data: map[string]any{"ok": true}})
	r := &RemoteSender{baseURL: relay.URL, client: &http.Client{}, slack: 100 * time.Millisecond}
	resp, err := r.SendCommand(&Command{Type: CmdPickElement}, time.Second)
	if err != nil || !resp.Success {
		t.Fatalf("slow relay failed: %v", err)
	}
	if asked, n := st.snapshot(); asked != "1000" || n != 1 {
		t.Fatalf("relay saw timeout_ms=%s in %d request(s), want 1000 in 1", asked, n)
	}
}

func TestRelayedPickTimeoutIsATimeoutNotADeadBridge(t *testing.T) {
	relay, st := fakeRelay(t, time.Hour, Response{})
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
	if _, n := st.snapshot(); n != 1 {
		t.Fatalf("pick sent %d relay requests, want 1 (no retry)", n)
	}
	// And a plain command reports a timeout in Server.SendCommand's words.
	// This is the second request the relay sees, by design.
	_, err = r.SendCommand(&Command{Type: CmdWaitLoad}, 50*time.Millisecond)
	if err == nil || !strings.Contains(err.Error(), "command wait_load timed out") {
		t.Fatalf("err = %v", err)
	}
	if _, n := st.snapshot(); n != 2 {
		t.Fatalf("relay saw %d requests, want 2", n)
	}
}

func TestRelayedPickErrorsFromTheOwningBridge(t *testing.T) {
	for msg, want := range map[string]error{
		"cancelled":              ErrPickCancelled,
		"timeout":                ErrPickTimeout,
		"no extension connected": ErrBridgeNotConnected,
		"command pick_element timed out after 3m5s": ErrPickTimeout,
	} {
		relay, st := fakeRelay(t, 0, Response{Success: false, Error: msg})
		r := &RemoteSender{baseURL: relay.URL, client: &http.Client{}}
		if _, _, err := NewExtensionPage(r, 3).PickElement(context.Background(), "x", time.Second); !errors.Is(err, want) {
			t.Errorf("relay error %q → %v, want %v", msg, err, want)
		}
		if _, n := st.snapshot(); n != 1 {
			t.Errorf("relay error %q: %d requests, want 1 (no retry)", msg, n)
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
