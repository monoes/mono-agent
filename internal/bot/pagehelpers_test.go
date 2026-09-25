package bot

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/monoes/mono-agent/internal/browser"
)

// fakeElement records calls; it is not backed by any DOM.
type fakeElement struct {
	name   string
	calls  []string
	onFoc  func()
	inputs []string
}

func (e *fakeElement) Click() error                         { e.calls = append(e.calls, "click"); return nil }
func (e *fakeElement) Input(t string) error                 { e.inputs = append(e.inputs, t); return nil }
func (e *fakeElement) Text() (string, error)                { return e.name, nil }
func (e *fakeElement) Attribute(string) (*string, error)    { return nil, nil }
func (e *fakeElement) SetFiles([]string) error              { return nil }
func (e *fakeElement) ScrollIntoView() error                { return nil }
func (e *fakeElement) WaitStable(time.Duration) error       { return nil }
func (e *fakeElement) HTML() (string, error)                { return "", nil }
func (e *fakeElement) Property(string) (interface{}, error) { return nil, nil }
func (e *fakeElement) Focus() error {
	e.calls = append(e.calls, "focus")
	if e.onFoc != nil {
		e.onFoc()
	}
	return nil
}

// fakePage is a minimal PageInterface without CDP.
type fakePage struct {
	present   []string // selectors currently "on the page"
	els       map[string]*fakeElement
	evalRaw   interface{}
	evalCalls []string
	pressed   []rune
	elements  func(call int) int
	elCalls   int
	scrolls   int
}

func (p *fakePage) Navigate(string) error                   { return nil }
func (p *fakePage) WaitLoad() error                         { return nil }
func (p *fakePage) WaitDOMStable(time.Duration) error       { return nil }
func (p *fakePage) WaitIdle(time.Duration) error            { return nil }
func (p *fakePage) Reload() error                           { return nil }
func (p *fakePage) GetURL() (string, error)                 { return "https://example.test/", nil }
func (p *fakePage) Has(string) (bool, error)                { return false, nil }
func (p *fakePage) KeyboardType(...rune) error              { return nil }
func (p *fakePage) KeyboardPress(k rune) error              { p.pressed = append(p.pressed, k); return nil }
func (p *fakePage) InsertText(string) error                 { return nil }
func (p *fakePage) MouseScroll(float64, float64, int) error { p.scrolls++; return nil }
func (p *fakePage) SetCookies(interface{}) error            { return nil }
func (p *fakePage) GetCookies() (interface{}, error)        { return nil, nil }
func (p *fakePage) Timeout(time.Duration) browser.PageInterface {
	return p
}
func (p *fakePage) Close() error { return nil }
func (p *fakePage) Element(s string, _ time.Duration) (browser.ElementHandle, error) {
	return nil, errors.New("not found")
}
func (p *fakePage) ElementX(s string, _ time.Duration) (browser.ElementHandle, error) {
	return nil, errors.New("not found")
}
func (p *fakePage) Elements(string) ([]browser.ElementHandle, error) {
	p.elCalls++
	n := 0
	if p.elements != nil {
		n = p.elements(p.elCalls)
	}
	out := make([]browser.ElementHandle, n)
	for i := range out {
		out[i] = &fakeElement{}
	}
	return out, nil
}
func (p *fakePage) Eval(js string, args ...interface{}) (*browser.EvalResult, error) {
	p.evalCalls = append(p.evalCalls, js)
	return browser.NewEvalResult(p.evalRaw), nil
}

// Race reports the LAST present selector, like a driver whose race winner
// is arbitrary — WaitForOutcomes must still pick the earliest.
func (p *fakePage) Race(sels []string, _ time.Duration) (int, browser.ElementHandle, error) {
	for i := len(sels) - 1; i >= 0; i-- {
		for _, s := range p.present {
			if s == sels[i] {
				return i, p.el(s), nil
			}
		}
	}
	return -1, nil, errors.New("none matched")
}

func (p *fakePage) el(s string) *fakeElement {
	if p.els == nil {
		p.els = map[string]*fakeElement{}
	}
	if e, ok := p.els[s]; ok {
		return e
	}
	e := &fakeElement{name: s}
	p.els[s] = e
	return e
}

// cdpPage adds EvalCDP and CDP.
type cdpPage struct {
	fakePage
	exprs   []string
	results []interface{}
	cdp     []string
	params  []map[string]interface{}
}

func (p *cdpPage) EvalCDP(js string) (interface{}, error) {
	p.exprs = append(p.exprs, js)
	if len(p.results) == 0 {
		return nil, nil
	}
	r := p.results[0]
	p.results = p.results[1:]
	if err, ok := r.(error); ok {
		return nil, err
	}
	return r, nil
}

func (p *cdpPage) CDP(method string, params map[string]interface{}) (map[string]interface{}, error) {
	p.cdp = append(p.cdp, method)
	p.params = append(p.params, params)
	return map[string]interface{}{}, nil
}

func TestEvalJSONUsesEvalCDPWithEncodedArgs(t *testing.T) {
	p := &cdpPage{results: []interface{}{map[string]interface{}{"n": float64(3)}}}
	var out struct{ N int }
	if err := EvalJSON(p, `(s, n) => ({n: s.length + n})`, &out, `a"b');alert(1)//`, 5); err != nil {
		t.Fatal(err)
	}
	if out.N != 3 {
		t.Fatalf("out = %+v", out)
	}
	want := `((s, n) => ({n: s.length + n}))("a\"b');alert(1)//",5)`
	if p.exprs[0] != want {
		t.Fatalf("expr = %s\nwant %s", p.exprs[0], want)
	}
	if len(p.evalCalls) != 0 {
		t.Fatal("Eval must not be used when EvalCDP exists")
	}
}

func TestEvalJSONEmptyAndErrorResults(t *testing.T) {
	p := &fakePage{}
	if err := EvalJSON(p, `() => 1`, nil); !errors.Is(err, ErrEmptyEval) {
		t.Fatalf("nil result: err = %v, want ErrEmptyEval", err)
	}
	p.evalRaw = map[string]interface{}{"__monoagent_error": "EvalError: unsafe-eval blocked"}
	if err := EvalJSON(p, `() => 1`, nil); err == nil || !strings.Contains(err.Error(), "unsafe-eval") {
		t.Fatalf("error object: err = %v", err)
	}
	p.evalRaw = []interface{}{"a", "b"}
	var got []string
	if err := EvalJSON(p, `() => ['a','b']`, &got); err != nil || len(got) != 2 {
		t.Fatalf("got %v, %v", got, err)
	}
	cp := &cdpPage{results: []interface{}{errors.New("eval_cdp: ReferenceError: x")}}
	if err := EvalJSON(cp, `() => x`, nil); err == nil || !strings.Contains(err.Error(), "ReferenceError") {
		t.Fatalf("cdp error: %v", err)
	}
}

func TestFindFirstAndWaitForOutcomesOrdering(t *testing.T) {
	p := &fakePage{present: []string{"#alt2", "#alt1"}}
	el, idx, err := FindFirst(p, "#primary", []string{"#alt1", "#alt2"}, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if idx != 2 || el.(*fakeElement).name != "#alt2" {
		t.Fatalf("FindFirst reports the Race winner: idx=%d el=%v", idx, el)
	}
	if _, _, err := FindFirst(&fakePage{}, "#x", nil, time.Millisecond); err == nil {
		t.Fatal("expected error when nothing matches")
	}

	p = &fakePage{present: []string{".err", ".ok", ".later"}}
	out, oel, err := WaitForOutcomes(p, []Outcome{{"none", ".none"}, {"ok", ".ok"}, {"err", ".err"}, {"later", ".later"}}, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if out.Label != "ok" || oel.(*fakeElement).name != ".ok" {
		t.Fatalf("WaitForOutcomes = %+v, want the earliest present outcome (ok)", out)
	}
	if _, _, err := WaitForOutcomes(&fakePage{}, []Outcome{{"x", ".x"}}, time.Millisecond); err == nil {
		t.Fatal("expected timeout error")
	}
}

func TestArgsCoercion(t *testing.T) {
	p := &fakePage{}
	page, got, err := Args([]interface{}{p, float64(123456789012), nil, "hi"}, 3, "id", "note?", "msg")
	if err != nil {
		t.Fatal(err)
	}
	if page != p || got[0] != "123456789012" || got[1] != "" || got[2] != "hi" {
		t.Fatalf("got %q", got)
	}
	if _, got, _ := Args([]interface{}{p, 1.5, int64(7)}, 3); got[0] != "1.5" || got[1] != "7" || got[2] != "" {
		t.Fatalf("got %q", got)
	}
	if _, _, err := Args([]interface{}{p}, 1, "username"); err == nil || !strings.Contains(err.Error(), "username") {
		t.Fatalf("missing required: %v", err)
	}
	if _, _, err := Args([]interface{}{"not a page"}, 0); err == nil {
		t.Fatal("non-page first arg must fail")
	}
	if _, _, err := Args(nil, 0); err == nil {
		t.Fatal("empty args must fail")
	}
}

func TestInputHelpersWithoutCDPFallBack(t *testing.T) {
	p := &fakePage{}
	el := &fakeElement{}
	if err := ClickTrusted(p, el); err != nil || len(el.calls) != 1 || el.calls[0] != "click" {
		t.Fatalf("ClickTrusted fallback: %v %v", el.calls, err)
	}
	if err := TypeInto(context.Background(), p, el, "hello"); err != nil || len(el.inputs) != 1 || el.inputs[0] != "hello" {
		t.Fatalf("TypeInto fallback: %v %v", el.inputs, err)
	}
	if err := PressEnter(p); err != nil || len(p.pressed) != 1 || p.pressed[0] != '\r' {
		t.Fatalf("PressEnter fallback: %v %v", p.pressed, err)
	}
}

func TestInputHelpersWithCDP(t *testing.T) {
	p := &cdpPage{}
	el := &fakeElement{}
	// mark armed, mark found, rect.
	p.results = []interface{}{true, true, map[string]interface{}{"X": 10.0, "Y": 20.0, "W": 100.0, "H": 40.0}}
	if err := ClickTrusted(p, el); err != nil {
		t.Fatal(err)
	}
	if strings.Join(p.cdp, ",") != "Input.dispatchMouseEvent,Input.dispatchMouseEvent,Input.dispatchMouseEvent" {
		t.Fatalf("cdp = %v", p.cdp)
	}
	if p.params[1]["type"] != "mousePressed" || p.params[1]["x"] != 60.0 || p.params[1]["y"] != 40.0 {
		t.Fatalf("press = %v", p.params[1])
	}
	for _, c := range el.calls {
		if c == "click" {
			t.Fatal("synthetic Click must not be used when CDP exists")
		}
	}

	p.cdp, p.params = nil, nil
	if err := PressEnter(p); err != nil {
		t.Fatal(err)
	}
	if len(p.cdp) != 2 || p.params[0]["type"] != "keyDown" || p.params[0]["windowsVirtualKeyCode"] != 13 || p.params[1]["type"] != "keyUp" {
		t.Fatalf("PressEnter cdp = %v %v", p.cdp, p.params)
	}

	// Marking fails (nothing found) → TypeInto falls back to Focus, then insertText.
	p.cdp, p.params = nil, nil
	p.results = []interface{}{true, false}
	if err := TypeInto(context.Background(), p, el, "héllo 👋"); err != nil {
		t.Fatal(err)
	}
	if last := p.cdp[len(p.cdp)-1]; last != "Input.insertText" || p.params[len(p.params)-1]["text"] != "héllo 👋" {
		t.Fatalf("TypeInto cdp = %v", p.cdp)
	}
}

func TestPageScrollAndCollect(t *testing.T) {
	old := scrollSettle
	scrollSettle = time.Millisecond
	defer func() { scrollSettle = old }()

	p := &fakePage{elements: func(call int) int { return call * 2 }}
	els, err := PageScrollAndCollect(context.Background(), p, ".item", 5)
	if err != nil || len(els) != 5 {
		t.Fatalf("got %d, %v", len(els), err)
	}
	p = &fakePage{elements: func(call int) int {
		if call > 2 {
			return 4
		}
		return call * 2
	}}
	els, err = PageScrollAndCollect(context.Background(), p, ".item", 0)
	if err != nil || len(els) != 4 || p.scrolls < 3 {
		t.Fatalf("got %d (scrolls %d), %v", len(els), p.scrolls, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := PageScrollAndCollect(ctx, p, ".item", 0); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled: %v", err)
	}
}
