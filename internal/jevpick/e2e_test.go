package jevpick

// Opt-in end-to-end check of Pick/Mark/Unmark against a real headless
// browser and the real Jev API. Skipped unless JEV_E2E_BROWSER names a
// Chromium-family binary and TYPESAFE_API_KEY is set:
//
//	JEV_E2E_BROWSER=/usr/bin/chromium go test -count=1 -run E2E -v ./internal/jevpick/
//
// The browser is private to the test (own profile, own debug port) and never
// touches the extension bridge. devtoolsPage/launchBrowser are copied from
// internal/nodes/browserjev/e2e_test.go (test helpers cannot be imported).

import (
	"context"
	"errors"
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
	"github.com/monoes/mono-agent/internal/jev"
)

const pickFixture = `<!doctype html><html><head><title>Pixelgram</title>
<style>body{font:15px sans-serif;margin:30px} .post,.dm{border:1px solid #ccc;padding:12px;margin:12px 0}</style></head>
<body><nav><a href="#home">Home</a> <a href="#explore">Explore</a> <input type="search" placeholder="Search"></nav>
<div class="post">
  <p><b>marta</b> Sunset over the lake</p>
  <div class="bar">
    <button aria-label="Like">&#9825;</button>
    <button aria-label="Comment">&#128172;</button>
    <button aria-label="Share">&#10148;</button>
    <button aria-label="Save">&#128278;</button>
  </div>
  <ul>
    <li><b>ann</b> Gorgeous! <button aria-label="Like comment">&#9825;</button> <button>Reply</button></li>
    <li><b>bo</b> Where is this? <button aria-label="Like comment">&#9825;</button> <button>Reply</button></li>
  </ul>
  <textarea aria-label="Add a comment…" placeholder="Add a comment…"></textarea>
  <div role="button" tabindex="0">Post</div>
</div>
<div class="dm"><h3>Messages · marta</h3>
  <div contenteditable="true" role="textbox" aria-label="Message"></div>
  <button>Send</button>
</div>
</body></html>`

func TestE2EPickMarkUnmark(t *testing.T) {
	bin := os.Getenv("JEV_E2E_BROWSER")
	key := os.Getenv("TYPESAFE_API_KEY")
	if bin == "" || key == "" {
		t.Skip("set JEV_E2E_BROWSER and TYPESAFE_API_KEY to run against a real browser and Jev")
	}
	site := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(pickFixture))
	}))
	defer site.Close()

	page := launchBrowser(t, bin)
	if _, err := page.CDP("Page.navigate", map[string]interface{}{"url": site.URL}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(500 * time.Millisecond)
	c, err := jev.NewClient(key, "")
	if err != nil {
		t.Fatal(err)
	}
	// Reads the marked element's describing text by the same attribute
	// selector the action fallback hands to the page driver.
	marked := func(marker string) string {
		var out string
		if err := NewBrowser(page).Evaluate(fmt.Sprintf(
			`(() => { const e=document.querySelector("[%s='%s']"); return e ? (e.getAttribute('aria-label') || e.textContent.trim() || e.tagName) : ""; })()`,
			MarkerAttr, marker), false, &out); err != nil {
			t.Fatal(err)
		}
		return out
	}

	cases := []struct {
		target Target
		want   string
	}{
		{Target{Intent: "the post's own Like heart button (not a comment's Like)", Kind: "click"}, "Like"},
		{Target{Intent: "the Post button that publishes the comment typed under the post", Kind: "click"}, "Post"},
		{Target{Intent: "the post's Add a comment text box", Kind: "fill"}, "Add a comment…"},
		{Target{Intent: "the message text box of the open direct message conversation", Kind: "any"}, "Message"},
		{Target{Intent: "the Send button of the open direct message conversation", Kind: "click"}, "Send"},
	}
	for _, tc := range cases {
		start := time.Now()
		got, err := Pick(context.Background(), c, page, tc.target, 0.5)
		if err != nil {
			t.Fatalf("%q: %v", tc.target.Intent, err)
		}
		t.Logf("%-70q → %q role=%s p=%.3f conf=%.3f (%v)", tc.target.Intent, got.Label, got.Role,
			got.Probability, got.Confidence, time.Since(start).Round(time.Millisecond))
		if m := marked(got.Marker); m != tc.want {
			t.Errorf("%q: marked element is %q, want %q", tc.target.Intent, m, tc.want)
		}
		if err := Unmark(page, got.Marker); err != nil {
			t.Fatal(err)
		}
		if m := marked(got.Marker); m != "" {
			t.Errorf("%q: Unmark left the marker on %q", tc.target.Intent, m)
		}
	}

	// Nothing on the page matches: NONE, and nothing is marked.
	_, err = Pick(context.Background(), c, page, Target{Intent: "the Checkout button of a shopping cart", Kind: "click"}, 0.5)
	if !errors.Is(err, ErrNoMatch) {
		t.Errorf("absent target: err = %v, want ErrNoMatch", err)
	}
	var n int
	if err := NewBrowser(page).Evaluate(fmt.Sprintf(`document.querySelectorAll("[%s]").length`, MarkerAttr), false, &n); err != nil || n != 0 {
		t.Errorf("markers left on the page: %d (%v)", n, err)
	}
}

// devtoolsPage is a Page over a direct DevTools WebSocket (flat session).
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
	dir, err := os.MkdirTemp("", "jevpick-e2e-*")
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
