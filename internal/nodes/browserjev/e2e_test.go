package browserjev

// Opt-in end-to-end run against a real headless browser and the real Jev
// API. Skipped unless JEV_E2E_BROWSER names a Chromium-family binary and
// TYPESAFE_API_KEY is set; JEV_E2E_TEXT_RUNTIME=claude also exercises the
// monomind text helper (otherwise a canned writer supplies field values).
//
//	JEV_E2E_BROWSER=/usr/bin/chromium go test -run E2E -v ./internal/nodes/browserjev/
//
// The browser is private to the test (own profile, own debug port) and
// never touches the extension bridge.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/monoes/mono-agent/internal/jevpick"
	"github.com/monoes/mono-agent/internal/workflow"
)

const e2eFixture = `<!doctype html><html><head><title>Railfare</title>
<style>body{font:15px sans-serif;margin:40px} label{display:block;margin:12px 0}</style></head>
<body><h1>Railfare · Book a train</h1>
<form id="f">
<label>From <input name="from" type="text"></label>
<label>To <input name="to" type="text"></label>
<label>Class <select name="cls"><option value="2">Second</option><option value="1">First</option></select></label>
<label><input type="checkbox" name="bike"> Bring a bicycle</label>
<button type="submit">Search trains</button>
</form>
<script>
document.getElementById('f').addEventListener('submit', e => {
  e.preventDefault();
  const d = new FormData(e.target);
  document.body.innerHTML = '<h1>Results</h1><p id="r">' +
    ['from','to','cls'].map(k => k + '=' + d.get(k)).join(' ') + ' bike=' + (d.get('bike') ? 'yes' : 'no') +
    '</p><p>3 trains found</p>';
  history.pushState({}, '', '/results');
});
</script></body></html>`

func TestE2EJevBookingForm(t *testing.T) {
	bin := os.Getenv("JEV_E2E_BROWSER")
	if bin == "" || os.Getenv("TYPESAFE_API_KEY") == "" {
		t.Skip("set JEV_E2E_BROWSER and TYPESAFE_API_KEY to run against a real browser and Jev")
	}
	site := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(e2eFixture))
	}))
	defer site.Close()

	page := launchBrowser(t, bin)
	var writer textWriter = func(_ context.Context, f map[string]any) (string, error) {
		label := strings.ToLower(fmt.Sprint(f["field"].(map[string]any)["label"]))
		switch {
		case strings.Contains(label, "from"):
			return "Zurich", nil
		case strings.Contains(label, "to"):
			return "Basel", nil
		}
		return "", errNoValue
	}
	if rt := os.Getenv("JEV_E2E_TEXT_RUNTIME"); rt != "" {
		writer = monomindWriter(rt, os.Getenv("JEV_E2E_TEXT_MODEL"))
	}
	res := runE2E(t, page, writer, map[string]interface{}{
		"url":  site.URL,
		"goal": "Search for first class train tickets from Zurich to Basel, bringing a bicycle. Stop when results are shown.",
	})
	// Independent verification of the outcome, not the model's DONE.
	text := fmt.Sprint(res["page_text"])
	if !strings.Contains(text, "from=Zurich to=Basel cls=1 bike=yes") {
		t.Fatalf("final page does not show the requested search: %q", text)
	}
}

// TestE2EJevBookingFormValues supplies From/To as `values`: the run must
// finish without a single text-writer turn.
func TestE2EJevBookingFormValues(t *testing.T) {
	bin := os.Getenv("JEV_E2E_BROWSER")
	if bin == "" || os.Getenv("TYPESAFE_API_KEY") == "" {
		t.Skip("set JEV_E2E_BROWSER and TYPESAFE_API_KEY to run against a real browser and Jev")
	}
	site := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(e2eFixture))
	}))
	defer site.Close()

	page := launchBrowser(t, bin)
	writer := func(_ context.Context, f map[string]any) (string, error) {
		t.Errorf("text writer called for %v", f["field"])
		return "", errNoValue
	}
	res := runE2E(t, page, writer, map[string]interface{}{
		"url":    site.URL,
		"goal":   "Search for first class train tickets from Zurich to Basel, bringing a bicycle. Stop when results are shown.",
		"values": map[string]interface{}{"From": "Zurich", "To": "Basel"},
	})
	text := fmt.Sprint(res["page_text"])
	if !strings.Contains(text, "from=Zurich to=Basel cls=1 bike=yes") {
		t.Fatalf("final page does not show the requested search: %q", text)
	}
	if res["text_turns"] != 0 {
		t.Errorf("text_turns = %v, want 0", res["text_turns"])
	}
}

func runE2E(t *testing.T, page *devtoolsPage, writer textWriter, config map[string]interface{}) map[string]interface{} {
	t.Helper()
	n := &Node{writer: writer, open: func(ctx context.Context, url string) (driver, func(), error) {
		if _, err := page.CDP("Page.navigate", map[string]interface{}{"url": url}); err != nil {
			return nil, nil, err
		}
		time.Sleep(500 * time.Millisecond)
		b := jevpick.NewBrowser(page)
		return extDriver{b}, func() {}, b.Setup(1120, 780)
	}}
	out, err := n.Execute(context.Background(), nodeInputNone, config)
	if err != nil {
		t.Fatal(err)
	}
	res := out[0].Items[0].JSON
	steps, _ := json.MarshalIndent(res["steps"], "", "  ")
	t.Logf("status=%v reason=%v decisions=%v value_requests=%v text_turns=%v low_confidence_steps=%v tokens=%v elapsed=%vms\nsteps=%s",
		res["status"], res["reason"], res["decisions"], res["value_requests"], res["text_turns"],
		res["low_confidence_steps"], res["jev_input_tokens"], res["elapsed_ms"], steps)
	return res
}

// devtoolsPage is a jevpick.Page over a direct DevTools WebSocket (flat session).
type devtoolsPage struct {
	mu      sync.Mutex
	conn    *websocket.Conn
	session string
	next    int
	waiters map[int]chan map[string]interface{}
}

func (p *devtoolsPage) send(method string, params map[string]interface{}, session string) (map[string]interface{}, error) {
	p.mu.Lock()
	p.next++
	id := p.next
	ch := make(chan map[string]interface{}, 1)
	p.waiters[id] = ch
	msg := map[string]interface{}{"id": id, "method": method, "params": params}
	if session != "" {
		msg["sessionId"] = session
	}
	err := p.conn.WriteJSON(msg)
	p.mu.Unlock()
	if err != nil {
		return nil, err
	}
	select {
	case res := <-ch:
		if e, ok := res["error"]; ok {
			return nil, fmt.Errorf("%s: %v", method, e)
		}
		r, _ := res["result"].(map[string]interface{})
		return r, nil
	case <-time.After(30 * time.Second):
		return nil, fmt.Errorf("%s timed out", method)
	}
}

func (p *devtoolsPage) CDP(method string, params map[string]interface{}) (map[string]interface{}, error) {
	return p.send(method, params, p.session)
}

func launchBrowser(t *testing.T, bin string) *devtoolsPage {
	t.Helper()
	// Not t.TempDir: the browser's helper processes can still be writing
	// the profile when the test ends, which fails TempDir's strict cleanup.
	dir, err := os.MkdirTemp("", "jev-e2e-*")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	cmd := exec.Command(bin, "--headless=new", "--remote-debugging-port=0", "--user-data-dir="+dir,
		"--no-first-run", "--no-default-browser-check", "--window-size=1120,780", "about:blank")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })

	var wsURL string
	for deadline := time.Now().Add(15 * time.Second); time.Now().Before(deadline); time.Sleep(100 * time.Millisecond) {
		raw, err := os.ReadFile(filepath.Join(dir, "DevToolsActivePort"))
		if lines := strings.Split(string(raw), "\n"); err == nil && len(lines) >= 2 {
			wsURL = "ws://127.0.0.1:" + strings.TrimSpace(lines[0]) + strings.TrimSpace(lines[1])
			break
		}
	}
	if wsURL == "" {
		t.Fatal("browser did not publish DevToolsActivePort")
	}
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	p := &devtoolsPage{conn: conn, waiters: map[int]chan map[string]interface{}{}}
	go func() {
		for {
			var msg map[string]interface{}
			if err := conn.ReadJSON(&msg); err != nil {
				return
			}
			id, ok := msg["id"].(float64)
			if !ok {
				continue // event
			}
			p.mu.Lock()
			ch := p.waiters[int(id)]
			delete(p.waiters, int(id))
			p.mu.Unlock()
			if ch != nil {
				ch <- msg
			}
		}
	}()
	target, err := p.send("Target.createTarget", map[string]interface{}{"url": "about:blank"}, "")
	if err != nil {
		t.Fatal(err)
	}
	att, err := p.send("Target.attachToTarget", map[string]interface{}{"targetId": target["targetId"], "flatten": true}, "")
	if err != nil {
		t.Fatal(err)
	}
	p.session, _ = att["sessionId"].(string)
	return p
}

var nodeInputNone = workflow.NodeInput{}
