package bottest

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-rod/rod/lib/proto"

	"github.com/monoes/mono-agent/internal/browser"
)

const defaultTimeout = 30 * time.Second

// Page is one browser tab. It implements browser.PageInterface with the
// semantics of *extension.ExtensionPage (see the package doc), plus the
// extension page's CDP extras: CDP, EvalCDP, TypeCDP, TypeCDPOnElement,
// InsertTextOnElement.
type Page struct {
	st      *pageState
	timeout time.Duration
	strict  bool
}

var _ browser.PageInterface = (*Page)(nil)

type pageState struct {
	b        *Browser
	t        testing.TB
	session  string
	targetID string

	mu           sync.Mutex
	loads        int
	loadCh       chan struct{}
	routes       []Route
	csp          string
	elementsWait time.Duration

	rec *Recorder
}

// NewPage opens a new tab with request interception already on (every
// request is blocked until Page.Serve adds a matching Route). The tab closes
// with the browser at test cleanup.
func (b *Browser) NewPage(t testing.TB) *Page {
	t.Helper()
	res, err := b.send("", "Target.createTarget", map[string]interface{}{"url": "about:blank"}, 0)
	if err != nil {
		t.Fatalf("bottest: %v", err)
	}
	var target struct {
		TargetID string `json:"targetId"`
	}
	_ = json.Unmarshal(res, &target)
	res, err = b.send("", "Target.attachToTarget", map[string]interface{}{"targetId": target.TargetID, "flatten": true}, 0)
	if err != nil {
		t.Fatalf("bottest: %v", err)
	}
	var att struct {
		SessionID string `json:"sessionId"`
	}
	_ = json.Unmarshal(res, &att)
	st := &pageState{
		b: b, t: t, session: att.SessionID, targetID: target.TargetID,
		loadCh: make(chan struct{}), elementsWait: 5 * time.Second, rec: newRecorder(),
	}
	q := b.registerSession(att.SessionID)
	go st.eventLoop(q)
	for _, c := range []struct {
		method string
		params map[string]interface{}
	}{
		{"Page.enable", nil},
		{"Runtime.enable", nil},
		{"Fetch.enable", map[string]interface{}{"patterns": []map[string]interface{}{{"urlPattern": "*", "requestStage": "Request"}}}},
		// A background tab never "has focus", which suppresses focus events.
		{"Emulation.setFocusEmulationEnabled", map[string]interface{}{"enabled": true}},
	} {
		if _, err := b.send(att.SessionID, c.method, c.params, 0); err != nil {
			t.Fatalf("bottest: %v", err)
		}
	}
	return &Page{st: st}
}

func (st *pageState) eventLoop(q *eventQueue) {
	for {
		ev, ok := q.pop()
		if !ok {
			return
		}
		switch ev.Method {
		case "Fetch.requestPaused":
			go st.handleRequestPaused(ev.Params)
		case "Page.loadEventFired":
			st.mu.Lock()
			st.loads++
			close(st.loadCh)
			st.loadCh = make(chan struct{})
			st.mu.Unlock()
		}
	}
}

func (p *Page) effectiveTimeout() time.Duration {
	if p.timeout > 0 {
		return p.timeout
	}
	return defaultTimeout
}

// Strict returns a view of the same tab whose Eval reports errors (CSP
// blocks, exceptions, {__monoagent_error}) instead of swallowing them the way
// ExtensionPage.Eval does.
func (p *Page) Strict() *Page {
	return &Page{st: p.st, timeout: p.timeout, strict: true}
}

// SetElementsWait changes how long Elements polls for a non-empty result
// (ExtensionPage: 5s). Zero means a single query.
func (p *Page) SetElementsWait(d time.Duration) {
	p.st.mu.Lock()
	p.st.elementsWait = d
	p.st.mu.Unlock()
}

// CDP sends one raw DevTools command to this tab and returns its result.
func (p *Page) CDP(method string, params map[string]interface{}) (map[string]interface{}, error) {
	raw, err := p.st.b.send(p.st.session, method, params, p.effectiveTimeout())
	if err != nil {
		return nil, fmt.Errorf("cdp %s: %w", method, err)
	}
	out := map[string]interface{}{}
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &out)
	}
	return out, nil
}

type evalResult struct {
	Result struct {
		Type  string          `json:"type"`
		Value json.RawMessage `json:"value"`
	} `json:"result"`
	ExceptionDetails *struct {
		Text      string `json:"text"`
		Exception *struct {
			Description string `json:"description"`
		} `json:"exception"`
	} `json:"exceptionDetails"`
}

// evaluate runs Runtime.evaluate (awaiting promises, by value) and returns
// the decoded value. unsafeEvalAllowed=false makes eval/new Function inside
// the expression obey the page's CSP.
func (st *pageState) evaluate(expr string, unsafeEvalAllowed bool, timeout time.Duration) (interface{}, error) {
	params := map[string]interface{}{
		"expression":    expr,
		"returnByValue": true,
		"awaitPromise":  true,
	}
	if !unsafeEvalAllowed {
		params["allowUnsafeEvalBlockedByCSP"] = false
	}
	raw, err := st.b.send(st.session, "Runtime.evaluate", params, timeout)
	if err != nil {
		return nil, err
	}
	var res evalResult
	if err := json.Unmarshal(raw, &res); err != nil {
		return nil, err
	}
	if d := res.ExceptionDetails; d != nil {
		msg := d.Text
		if d.Exception != nil && d.Exception.Description != "" {
			msg = d.Exception.Description
			if i := strings.IndexByte(msg, '\n'); i > 0 {
				msg = msg[:i]
			}
		}
		return nil, errors.New(msg)
	}
	if len(res.Result.Value) == 0 {
		return nil, nil
	}
	var v interface{}
	if err := json.Unmarshal(res.Result.Value, &v); err != nil {
		return nil, err
	}
	return v, nil
}

// rt calls one function of the injected content-script runtime.
func (p *Page) rt(fn string, params map[string]interface{}) (interface{}, error) {
	b, err := json.Marshal(params)
	if err != nil {
		return nil, err
	}
	expr := "(() => { const B = window.__bottest || (window.__bottest = " + runtimeJS + "); return B." + fn + "(" + string(b) + "); })()"
	return p.st.evaluate(expr, true, 2*time.Minute)
}

// ---------------------------------------------------------------------------
// Navigation
// ---------------------------------------------------------------------------

// Navigate loads url and waits for its load event (like the extension's
// navigate, which waits for the tab to complete). An unserved URL fails
// with net::ERR_BLOCKED_BY_CLIENT.
func (p *Page) Navigate(url string) error {
	p.st.mu.Lock()
	before := p.st.loads
	p.st.mu.Unlock()
	res, err := p.CDP("Page.navigate", map[string]interface{}{"url": url})
	if err != nil {
		return err
	}
	if et, _ := res["errorText"].(string); et != "" {
		return fmt.Errorf("navigate %s: %s", url, et)
	}
	if lid, _ := res["loaderId"].(string); lid == "" {
		return nil // same-document navigation: no load event
	}
	return p.waitLoadAfter(before, p.effectiveTimeout())
}

func (p *Page) waitLoadAfter(n int, timeout time.Duration) error {
	deadline := time.After(timeout)
	for {
		p.st.mu.Lock()
		loads, ch := p.st.loads, p.st.loadCh
		p.st.mu.Unlock()
		if loads > n {
			return nil
		}
		select {
		case <-ch:
		case <-deadline:
			return fmt.Errorf("page did not finish loading within %v", timeout)
		}
	}
}

// WaitLoad waits until document.readyState is "complete".
func (p *Page) WaitLoad() error {
	deadline := time.Now().Add(p.effectiveTimeout())
	for {
		v, err := p.st.evaluate(`document.readyState`, true, 5*time.Second)
		if err == nil && v == "complete" {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("page did not finish loading within %v", p.effectiveTimeout())
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// WaitDOMStable behaves like WaitLoad, as on ExtensionPage (the extension
// ignores the mode).
func (p *Page) WaitDOMStable(timeout time.Duration) error { return p.Timeout(timeout).WaitLoad() }

// WaitIdle behaves like WaitLoad, as on ExtensionPage.
func (p *Page) WaitIdle(timeout time.Duration) error { return p.Timeout(timeout).WaitLoad() }

// Reload reloads and waits for the load event.
func (p *Page) Reload() error {
	p.st.mu.Lock()
	before := p.st.loads
	p.st.mu.Unlock()
	if _, err := p.CDP("Page.reload", nil); err != nil {
		return err
	}
	return p.waitLoadAfter(before, p.effectiveTimeout())
}

// GetURL returns the document's current URL.
func (p *Page) GetURL() (string, error) {
	v, err := p.st.evaluate(`location.href`, true, 10*time.Second)
	if err != nil {
		return "", err
	}
	s, _ := v.(string)
	return s, nil
}

// ---------------------------------------------------------------------------
// Element queries
// ---------------------------------------------------------------------------

func (p *Page) elem(id string) *Element { return &Element{p: p, id: id} }

// poll runs fn every interval until it reports done or timeout passes (at
// least once). Errors — e.g. the execution context being replaced by a
// navigation — count as "not yet".
func poll(timeout, interval time.Duration, fn func() (bool, error)) error {
	deadline := time.Now().Add(timeout)
	var lastErr error
	for {
		done, err := fn()
		if err == nil && done {
			return nil
		}
		if err != nil {
			lastErr = err
		}
		if !time.Now().Add(interval).Before(deadline) {
			return lastErr
		}
		time.Sleep(interval)
	}
}

func (p *Page) find(params map[string]interface{}, what string, timeout time.Duration) (browser.ElementHandle, error) {
	var id string
	_ = poll(timeout, 200*time.Millisecond, func() (bool, error) {
		v, err := p.rt("find", params)
		if err != nil {
			return false, err
		}
		id, _ = v.(string)
		return id != "", nil
	})
	if id == "" {
		return nil, fmt.Errorf("Element not found within %dms: %s", timeout.Milliseconds(), what)
	}
	return p.elem(id), nil
}

// Element polls every 200ms for selector until timeout.
func (p *Page) Element(selector string, timeout time.Duration) (browser.ElementHandle, error) {
	return p.find(map[string]interface{}{"selector": selector}, selector, timeout)
}

// ElementX polls every 200ms for xpath until timeout.
func (p *Page) ElementX(xpath string, timeout time.Duration) (browser.ElementHandle, error) {
	return p.find(map[string]interface{}{"xpath": xpath}, xpath, timeout)
}

// Elements returns every match, polling (200ms) up to the elements wait
// (5s by default) for at least one; no match is an empty slice, not an error.
func (p *Page) Elements(selector string) ([]browser.ElementHandle, error) {
	p.st.mu.Lock()
	wait := p.st.elementsWait
	p.st.mu.Unlock()
	var ids []interface{}
	err := poll(wait, 200*time.Millisecond, func() (bool, error) {
		v, err := p.rt("findAll", map[string]interface{}{"selector": selector})
		if err != nil {
			return false, err
		}
		ids, _ = v.([]interface{})
		return len(ids) > 0, nil
	})
	if err != nil && len(ids) == 0 && strings.Contains(err.Error(), "SyntaxError") {
		return nil, err
	}
	out := make([]browser.ElementHandle, 0, len(ids))
	for _, raw := range ids {
		if id, ok := raw.(string); ok {
			out = append(out, p.elem(id))
		}
	}
	return out, nil
}

// Has reports whether selector matches right now.
func (p *Page) Has(selector string) (bool, error) {
	v, err := p.rt("has", map[string]interface{}{"selector": selector})
	if err != nil {
		return false, err
	}
	b, _ := v.(bool)
	return b, nil
}

// Race waits up to timeout for any selector; when several match, the
// earliest in the list wins. Timing out is an error, as on ExtensionPage.
func (p *Page) Race(selectors []string, timeout time.Duration) (int, browser.ElementHandle, error) {
	if len(selectors) == 0 {
		return -1, nil, errors.New("selectors array is required and must be non-empty")
	}
	idx, id := -1, ""
	_ = poll(timeout, 100*time.Millisecond, func() (bool, error) {
		v, err := p.rt("race", map[string]interface{}{"selectors": selectors})
		if err != nil {
			return false, err
		}
		m, _ := v.(map[string]interface{})
		if m == nil {
			return false, nil
		}
		f, _ := m["index"].(float64)
		idx = int(f)
		id, _ = m["id"].(string)
		return id != "", nil
	})
	if id == "" {
		return -1, nil, fmt.Errorf("None of the selectors matched within %dms", timeout.Milliseconds())
	}
	return idx, p.elem(id), nil
}

// ---------------------------------------------------------------------------
// Keyboard / input (synthetic, like the content script)
// ---------------------------------------------------------------------------

// KeyboardType dispatches synthetic key/input events per character on the
// focused element.
func (p *Page) KeyboardType(keys ...rune) error {
	if len(keys) == 0 {
		return errors.New("text is required")
	}
	_, err := p.rt("keyboardType", map[string]interface{}{"text": string(keys)})
	return err
}

// KeyboardPress dispatches a synthetic keydown/keypress/keyup for key
// (string(key) as the key name, e.g. '\r' — like the extension).
func (p *Page) KeyboardPress(key rune) error {
	_, err := p.rt("keyboardPress", map[string]interface{}{"key": string(key)})
	return err
}

// InsertText inserts text at the focused element (paste → execCommand →
// value → textContent).
func (p *Page) InsertText(text string) error {
	_, err := p.rt("insertText", map[string]interface{}{"text": text})
	return err
}

// InsertTextOnElement focuses and clicks the element, then inserts text.
func (p *Page) InsertTextOnElement(text string, elementID string) error {
	_, err := p.rt("insertText", map[string]interface{}{"text": text, "id": elementID})
	return err
}

// MouseScroll scrolls the window by (x, y) (smooth; steps is ignored, as in
// the extension).
func (p *Page) MouseScroll(x, y float64, steps int) error {
	_, err := p.rt("scrollBy", map[string]interface{}{"x": x, "y": y})
	return err
}

// ---------------------------------------------------------------------------
// Eval
// ---------------------------------------------------------------------------

// Eval runs js like the extension's MAIN-world eval: compiled with
// new Function (blocked by a CSP without 'unsafe-eval'); a function is
// called with args. Errors are swallowed into a nil result exactly like
// ExtensionPage.Eval; use Strict() to get them.
func (p *Page) Eval(js string, args ...interface{}) (*browser.EvalResult, error) {
	if args == nil {
		args = []interface{}{}
	}
	a, err := json.Marshal(args)
	if err != nil {
		if p.strict {
			return nil, err
		}
		return browser.NewEvalResult(nil), nil
	}
	code, _ := json.Marshal(js)
	v, err := p.st.evaluate(fmt.Sprintf(evalWrapperJS, a, code), false, 10*time.Second)
	if err == nil {
		if m, ok := v.(map[string]interface{}); ok {
			if msg, ok := m["__monoagent_error"]; ok {
				err = fmt.Errorf("Eval: %v", msg)
			}
		}
	}
	if err != nil {
		if p.strict {
			return nil, err
		}
		return browser.NewEvalResult(nil), nil
	}
	return browser.NewEvalResult(v), nil
}

// EvalCDP evaluates js via Runtime.evaluate (CSP does not apply), awaiting a
// returned promise; exceptions are errors.
func (p *Page) EvalCDP(js string) (interface{}, error) {
	v, err := p.st.evaluate(js, true, p.effectiveTimeout())
	if err != nil {
		return nil, fmt.Errorf("eval_cdp: %w", err)
	}
	return v, nil
}

// TypeCDP focuses the first visible contenteditable (>100×30) or
// [role=textbox] and inserts text with Input.insertText.
func (p *Page) TypeCDP(text string) error { return p.TypeCDPOnElement(text, "") }

// TypeCDPOnElement mirrors the extension: the element is only used (clicked
// at its centre) if the focus script itself fails; otherwise it types into
// the first visible contenteditable, like TypeCDP.
func (p *Page) TypeCDPOnElement(text string, elementID string) error {
	if text == "" {
		return errors.New("text required")
	}
	v, err := p.st.evaluate(typeCDPFocusJS, true, 10*time.Second)
	if err == nil {
		if m, ok := v.(map[string]interface{}); ok && m["found"] == true {
			time.Sleep(300 * time.Millisecond)
		}
	} else if elementID != "" {
		if r, rerr := p.rt("rect", map[string]interface{}{"id": elementID}); rerr == nil {
			if m, ok := r.(map[string]interface{}); ok {
				x := m["x"].(float64) + m["width"].(float64)/2
				y := m["y"].(float64) + m["height"].(float64)/2
				for _, typ := range []string{"mousePressed", "mouseReleased"} {
					_, _ = p.CDP("Input.dispatchMouseEvent", map[string]interface{}{"type": typ, "x": x, "y": y, "button": "left", "clickCount": 1})
				}
				time.Sleep(300 * time.Millisecond)
			}
		}
	}
	_, err = p.CDP("Input.insertText", map[string]interface{}{"text": text})
	return err
}

// ---------------------------------------------------------------------------
// Cookies
// ---------------------------------------------------------------------------

// SetCookies accepts []*proto.NetworkCookieParam, like ExtensionPage.
func (p *Page) SetCookies(cookies interface{}) error {
	params, ok := cookies.([]*proto.NetworkCookieParam)
	if !ok {
		return fmt.Errorf("SetCookies: expected []*proto.NetworkCookieParam, got %T", cookies)
	}
	if len(params) == 0 {
		return nil
	}
	_, err := p.CDP("Network.setCookies", map[string]interface{}{"cookies": params})
	return err
}

// GetCookies returns the current URL's non-httpOnly cookies as a
// []interface{} of maps, like ExtensionPage.
func (p *Page) GetCookies() (interface{}, error) {
	u, err := p.GetURL()
	if err != nil {
		return nil, err
	}
	res, err := p.CDP("Network.getCookies", map[string]interface{}{"urls": []string{u}})
	if err != nil {
		return nil, err
	}
	all, _ := res["cookies"].([]interface{})
	out := []interface{}{}
	for _, c := range all {
		if m, ok := c.(map[string]interface{}); ok && m["httpOnly"] != true {
			out = append(out, m)
		}
	}
	return out, nil
}

// Timeout returns a view of the same tab with operations bounded by d.
func (p *Page) Timeout(d time.Duration) browser.PageInterface {
	return &Page{st: p.st, timeout: d, strict: p.strict}
}

// Close closes the tab.
func (p *Page) Close() error {
	_, err := p.st.b.send("", "Target.closeTarget", map[string]interface{}{"targetId": p.st.targetID}, 0)
	return err
}

// ---------------------------------------------------------------------------
// Element
// ---------------------------------------------------------------------------

// Element implements browser.ElementHandle like *extension.ExtensionElement:
// a registry id that goes stale when the document is replaced.
type Element struct {
	p  *Page
	id string
}

var _ browser.ElementHandle = (*Element)(nil)

// ElementID returns the registry id (as ExtensionElement.ElementID does).
func (e *Element) ElementID() string { return e.id }

func (e *Element) call(fn string, extra map[string]interface{}) (interface{}, error) {
	params := map[string]interface{}{"id": e.id}
	for k, v := range extra {
		params[k] = v
	}
	return e.p.rt(fn, params)
}

// Click: scrollIntoView, synthetic mousedown/mouseup, el.click() — untrusted.
func (e *Element) Click() error { _, err := e.call("click", nil); return err }

// Input focuses, clears, then types text per character (contenteditable:
// paste → execCommand → textContent).
func (e *Element) Input(text string) error {
	_, err := e.call("input", map[string]interface{}{"text": text})
	return err
}

// Text returns the trimmed textContent.
func (e *Element) Text() (string, error) {
	v, err := e.call("text", nil)
	s, _ := v.(string)
	return s, err
}

// Attribute returns nil when the attribute is missing.
func (e *Element) Attribute(name string) (*string, error) {
	v, err := e.call("attr", map[string]interface{}{"name": name})
	if err != nil {
		return nil, err
	}
	s, ok := v.(string)
	if !ok {
		return nil, nil
	}
	return &s, nil
}

// Property returns el[name] as JSON-decoded data.
func (e *Element) Property(name string) (interface{}, error) {
	return e.call("prop", map[string]interface{}{"name": name})
}

// HTML returns innerHTML (ExtensionElement's default).
func (e *Element) HTML() (string, error) {
	v, err := e.call("html", nil)
	s, _ := v.(string)
	return s, err
}

// Focus calls el.focus() and dispatches a synthetic bubbling focus event.
func (e *Element) Focus() error { _, err := e.call("focus", nil); return err }

// ScrollIntoView centres the element (smooth).
func (e *Element) ScrollIntoView() error { _, err := e.call("scrollIntoView", nil); return err }

// WaitStable waits until the element's box is unchanged across d.
func (e *Element) WaitStable(d time.Duration) error {
	if d <= 0 {
		d = 100 * time.Millisecond
	}
	_, err := e.call("stable", map[string]interface{}{"ms": d.Milliseconds()})
	return err
}

// SetFiles reads the files in Go and assigns them to a file input through a
// DataTransfer, firing change and input — the extension's approach.
func (e *Element) SetFiles(paths []string) error {
	type fileData struct {
		Name     string `json:"name"`
		Data     string `json:"data"`
		MimeType string `json:"mimeType"`
	}
	var files []fileData
	for _, p := range paths {
		data, err := os.ReadFile(p)
		if err != nil {
			return fmt.Errorf("set_files: read %s: %w", p, err)
		}
		name := filepath.Base(p)
		mt := "application/octet-stream"
		switch {
		case strings.HasSuffix(name, ".png"):
			mt = "image/png"
		case strings.HasSuffix(name, ".jpg"), strings.HasSuffix(name, ".jpeg"):
			mt = "image/jpeg"
		case strings.HasSuffix(name, ".mp4"):
			mt = "video/mp4"
		}
		files = append(files, fileData{Name: name, Data: base64.StdEncoding.EncodeToString(data), MimeType: mt})
	}
	_, err := e.call("setFiles", map[string]interface{}{"fileData": files})
	return err
}
