package extension

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

func TestProfileBridgePinsItsBrowser(t *testing.T) {
	srv, _, wsURL, _ := startStatusTestServer(t)
	a := dialBrowser(t, wsURL, instA, "p-a", "")
	waitBrowsers(t, srv, 1)

	pb := (&ServerBridge{Server: srv}).ForProfile("p-a").(*ProfileBridge)
	if !pb.IsConnected() {
		t.Fatal("p-a's browser is connected")
	}
	tabc := make(chan int, 1)
	go func() {
		id, err := pb.CreateTab("https://example.com")
		if err != nil {
			t.Errorf("CreateTab: %v", err)
		}
		tabc <- id
	}()
	answerNext(t, a, 0, map[string]any{"tabId": 5})
	tab := <-tabc

	// The user rebinds A mid-run. The run's tab is still in A, so its
	// commands must keep going there.
	_ = a.WriteJSON(map[string]string{"kind": KindBinding, "profile": "p-other"})
	time.Sleep(50 * time.Millisecond)

	page := pb.NewPage(tab)
	errc := make(chan error, 1)
	go func() { errc <- page.Navigate("https://example.com/next") }()
	cmd := answerNext(t, a, 0, map[string]any{})
	if cmd == nil || cmd.Type != CmdNavigate || cmd.TabID != 5 {
		t.Fatalf("A got %+v, want navigate on tab 5", cmd)
	}
	if err := <-errc; err != nil {
		t.Fatal(err)
	}
}

func TestProfileBridgeSaysWhyItIsNotConnected(t *testing.T) {
	srv, _, wsURL, _ := startStatusTestServer(t)
	dialBrowser(t, wsURL, instA, "p-a", "")
	waitBrowsers(t, srv, 1)
	pb := (&ServerBridge{Server: srv}).ForProfile("p-b").(*ProfileBridge)
	if pb.IsConnected() {
		t.Fatal("no browser may run p-b")
	}
	_, err := pb.Route()
	var nb *NoBrowserError
	if !errors.As(err, &nb) || nb.Profile != "p-b" {
		t.Fatalf("Route err = %v", err)
	}
	if _, err := pb.CreateTab("https://example.com"); !errors.As(err, &nb) {
		t.Fatalf("CreateTab err = %v, want the same explanation", err)
	}
}

// oldBridge is a relay from before routing: /monoagent/resolve is a 404,
// the relay ignores target parameters.
type oldBridge struct {
	mu      sync.Mutex
	queries []string
}

func (o *oldBridge) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case "/monoagent/health":
		_ = json.NewEncoder(w).Encode(Status{Service: ServiceName, Status: StatusConnected, Connected: true})
	case "/monoagent/relay":
		o.mu.Lock()
		o.queries = append(o.queries, r.URL.RawQuery)
		o.mu.Unlock()
		var cmd Command
		_ = json.NewDecoder(r.Body).Decode(&cmd)
		_ = json.NewEncoder(w).Encode(Response{ID: cmd.ID, Success: true, Data: map[string]any{"tabId": 3}})
	default:
		http.NotFound(w, r)
	}
}

func TestProfileBridgeFallsBackOnAnOldBridge(t *testing.T) {
	old := &oldBridge{}
	ts := httptest.NewServer(old)
	t.Cleanup(ts.Close)
	pb := (&RemoteBridge{Sender: &RemoteSender{baseURL: ts.URL, client: &http.Client{}}}).ForProfile("p-a").(*ProfileBridge)
	if !pb.IsConnected() {
		t.Fatal("an old bridge with its one browser attached counts as connected")
	}
	id, err := pb.CreateTab("https://example.com")
	if err != nil || id != 3 {
		t.Fatalf("CreateTab = %d, %v", id, err)
	}
}
