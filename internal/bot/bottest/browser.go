package bottest

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// Browser is a private headless Chromium reached over one DevTools
// WebSocket. Command replies are matched by id; events are fanned out to the
// page (session) they belong to through an unbounded queue, so nothing is
// dropped while a handler is busy.
type Browser struct {
	conn *websocket.Conn
	cmd  *exec.Cmd
	dir  string

	wmu sync.Mutex // serialises writes

	mu       sync.Mutex
	next     int
	waiters  map[int]chan cdpMessage
	sessions map[string]*eventQueue
	closed   bool
	done     chan struct{}
}

type cdpError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    string `json:"data,omitempty"`
}

type cdpMessage struct {
	ID        int             `json:"id,omitempty"`
	Method    string          `json:"method,omitempty"`
	Params    json.RawMessage `json:"params,omitempty"`
	Result    json.RawMessage `json:"result,omitempty"`
	Error     *cdpError       `json:"error,omitempty"`
	SessionID string          `json:"sessionId,omitempty"`
}

// browserBinary returns the configured browser, or "" when none is.
func browserBinary() string {
	for _, env := range []string{"BOTTEST_BROWSER", "JEV_E2E_BROWSER"} {
		if v := strings.TrimSpace(os.Getenv(env)); v != "" {
			return v
		}
	}
	return ""
}

// Launch starts a private headless browser for the test and registers its
// shutdown with t.Cleanup. The binary comes from $BOTTEST_BROWSER (or
// $JEV_E2E_BROWSER); when neither is set the test is skipped. The browser
// gets its own profile under $TMPDIR and a random debugging port — it never
// touches the user's browser, profile, or port 9222 — and is started with
// background networking disabled and every hostname unresolvable, so the
// only content a page can load is what Page.Serve fulfils.
func Launch(t testing.TB) *Browser {
	t.Helper()
	bin := browserBinary()
	if bin == "" {
		t.Skip("set BOTTEST_BROWSER (or JEV_E2E_BROWSER) to a Chromium binary to run browser tests")
	}
	// Not t.TempDir: browser helper processes can still be writing the
	// profile at cleanup time, which fails TempDir's strict removal.
	dir, err := os.MkdirTemp("", "bottest-*")
	if err != nil {
		t.Fatalf("bottest: profile dir: %v", err)
	}
	cmd := exec.Command(bin,
		"--headless=new",
		"--remote-debugging-port=0",
		"--user-data-dir="+dir,
		"--no-first-run",
		"--no-default-browser-check",
		"--disable-background-networking",
		"--disable-component-update",
		"--disable-default-apps",
		"--disable-sync",
		"--disable-domain-reliability",
		"--disable-client-side-phishing-detection",
		"--disable-features=Translate,OptimizationHints,MediaRouter,AutofillServerCommunication,InterestFeedContentSuggestions",
		"--metrics-recording-only",
		"--no-pings",
		"--host-resolver-rules=MAP * ~NOTFOUND",
		"--proxy-server=127.0.0.1:9",
		"--proxy-bypass-list=<-loopback>",
		"--mute-audio",
		"--hide-scrollbars",
		"--window-size=1280,900",
		"about:blank",
	)
	if err := cmd.Start(); err != nil {
		_ = os.RemoveAll(dir)
		t.Fatalf("bottest: start %s: %v", bin, err)
	}
	b := &Browser{
		cmd:      cmd,
		dir:      dir,
		waiters:  map[int]chan cdpMessage{},
		sessions: map[string]*eventQueue{},
		done:     make(chan struct{}),
	}
	t.Cleanup(b.shutdown)

	var wsURL string
	for deadline := time.Now().Add(20 * time.Second); time.Now().Before(deadline); time.Sleep(50 * time.Millisecond) {
		raw, err := os.ReadFile(filepath.Join(dir, "DevToolsActivePort"))
		if lines := strings.Split(string(raw), "\n"); err == nil && len(lines) >= 2 && strings.TrimSpace(lines[1]) != "" {
			wsURL = "ws://127.0.0.1:" + strings.TrimSpace(lines[0]) + strings.TrimSpace(lines[1])
			break
		}
	}
	if wsURL == "" {
		t.Fatalf("bottest: %s did not publish DevToolsActivePort", bin)
	}
	dialer := *websocket.DefaultDialer
	dialer.Proxy = nil
	conn, _, err := dialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("bottest: dial devtools: %v", err)
	}
	conn.SetReadLimit(256 << 20)
	b.conn = conn
	go b.readLoop()
	return b
}

func (b *Browser) shutdown() {
	b.mu.Lock()
	already := b.closed
	b.closed = true
	b.mu.Unlock()
	if !already {
		exited := make(chan struct{})
		if b.cmd != nil && b.cmd.Process != nil {
			go func() { _ = b.cmd.Wait(); close(exited) }()
		} else {
			close(exited)
		}
		// Ask for a clean exit first so helper processes stop writing the
		// profile; kill if that does not happen promptly.
		if b.conn != nil {
			b.wmu.Lock()
			_ = b.conn.WriteJSON(map[string]interface{}{"id": 1 << 30, "method": "Browser.close"})
			b.wmu.Unlock()
		}
		select {
		case <-exited:
		case <-time.After(5 * time.Second):
			_ = b.cmd.Process.Kill()
			<-exited
		}
		if b.conn != nil {
			_ = b.conn.Close()
		}
	}
	// Best effort: late helper writes can race the first removal.
	for i := 0; i < 10; i++ {
		if os.RemoveAll(b.dir) == nil {
			if _, err := os.Stat(b.dir); os.IsNotExist(err) {
				return
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func (b *Browser) readLoop() {
	defer func() {
		b.mu.Lock()
		b.closed = true
		for id, ch := range b.waiters {
			close(ch)
			delete(b.waiters, id)
		}
		for _, q := range b.sessions {
			q.close()
		}
		b.mu.Unlock()
		close(b.done)
	}()
	for {
		_, data, err := b.conn.ReadMessage()
		if err != nil {
			return
		}
		var msg cdpMessage
		if json.Unmarshal(data, &msg) != nil {
			continue
		}
		if msg.ID != 0 {
			b.mu.Lock()
			ch := b.waiters[msg.ID]
			delete(b.waiters, msg.ID)
			b.mu.Unlock()
			if ch != nil {
				ch <- msg
			}
			continue
		}
		if msg.SessionID == "" {
			continue
		}
		b.mu.Lock()
		q := b.sessions[msg.SessionID]
		b.mu.Unlock()
		if q != nil {
			q.push(msg)
		}
	}
}

// send issues one DevTools command (on a session when session != "") and
// waits up to timeout for its reply.
func (b *Browser) send(session, method string, params interface{}, timeout time.Duration) (json.RawMessage, error) {
	if params == nil {
		params = map[string]interface{}{}
	}
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		return nil, errors.New("bottest: browser connection closed")
	}
	b.next++
	id := b.next
	ch := make(chan cdpMessage, 1)
	b.waiters[id] = ch
	b.mu.Unlock()

	msg := map[string]interface{}{"id": id, "method": method, "params": params}
	if session != "" {
		msg["sessionId"] = session
	}
	b.wmu.Lock()
	err := b.conn.WriteJSON(msg)
	b.wmu.Unlock()
	if err != nil {
		b.mu.Lock()
		delete(b.waiters, id)
		b.mu.Unlock()
		return nil, fmt.Errorf("%s: %w", method, err)
	}
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case res, ok := <-ch:
		if !ok {
			return nil, fmt.Errorf("%s: browser connection closed", method)
		}
		if res.Error != nil {
			if res.Error.Data != "" {
				return nil, fmt.Errorf("%s: %s (%s)", method, res.Error.Message, res.Error.Data)
			}
			return nil, fmt.Errorf("%s: %s", method, res.Error.Message)
		}
		return res.Result, nil
	case <-timer.C:
		b.mu.Lock()
		delete(b.waiters, id)
		b.mu.Unlock()
		return nil, fmt.Errorf("%s: timed out after %v", method, timeout)
	}
}

func (b *Browser) registerSession(id string) *eventQueue {
	q := newEventQueue()
	b.mu.Lock()
	b.sessions[id] = q
	b.mu.Unlock()
	return q
}

// eventQueue is an unbounded FIFO of events for one session.
type eventQueue struct {
	mu     sync.Mutex
	items  []cdpMessage
	signal chan struct{}
	done   bool
}

func newEventQueue() *eventQueue {
	return &eventQueue{signal: make(chan struct{}, 1)}
}

func (q *eventQueue) push(m cdpMessage) {
	q.mu.Lock()
	q.items = append(q.items, m)
	q.mu.Unlock()
	select {
	case q.signal <- struct{}{}:
	default:
	}
}

func (q *eventQueue) close() {
	q.mu.Lock()
	q.done = true
	q.mu.Unlock()
	select {
	case q.signal <- struct{}{}:
	default:
	}
}

// pop blocks for the next event; ok is false once the queue is closed and
// drained.
func (q *eventQueue) pop() (cdpMessage, bool) {
	for {
		q.mu.Lock()
		if len(q.items) > 0 {
			m := q.items[0]
			q.items = q.items[1:]
			q.mu.Unlock()
			return m, true
		}
		done := q.done
		q.mu.Unlock()
		if done {
			return cdpMessage{}, false
		}
		<-q.signal
	}
}
