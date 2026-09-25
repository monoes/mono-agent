package bot

// PageInterface-based replacements for the Rod-only helpers in humanize.go.
//
// Bots run on whatever browser.PageInterface the session provider hands
// them — in practice *extension.ExtensionPage (the user's own browser, driven
// through the Chrome extension). Code here therefore never type-asserts a
// concrete page: optional capabilities are discovered through small
// interfaces (cdpEvaler, cdpCaller) that ExtensionPage, the bottest harness
// page, and any future driver can implement.
//
// Two names collide with the Rod helpers in humanize.go and carry a Page
// prefix instead: PageClickAndWaitNavigation, PageScrollAndCollect.

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/monoes/mono-agent/internal/browser"
)

// cdpEvaler is implemented by pages that can run JavaScript through the
// DevTools Runtime.evaluate path, which is not subject to the page's CSP
// (ExtensionPage.EvalCDP, bottest.Page.EvalCDP).
type cdpEvaler interface {
	EvalCDP(js string) (interface{}, error)
}

// cdpCaller is implemented by pages that relay raw DevTools commands
// (ExtensionPage.CDP, bottest.Page.CDP). Input.* events sent this way are
// trusted (isTrusted === true), unlike DOM-dispatched synthetic events.
type cdpCaller interface {
	CDP(method string, params map[string]interface{}) (map[string]interface{}, error)
}

// evalErrorKey is the property the extension's MAIN-world eval wrapper uses
// to report a thrown exception as a value; EvalJSON treats it as an error.
const evalErrorKey = "__monoagent_error"

// ErrEmptyEval is returned (wrapped) by EvalJSON when the page produced no
// value — typically because ExtensionPage.Eval swallowed a CSP or script
// error and returned nil.
var ErrEmptyEval = errors.New("eval returned no result")

// EvalJSON runs a JavaScript function on the page and decodes its return value
// into out (which may be nil to discard it). fnSrc must be a function source,
// e.g. `(sel, n) => document.querySelectorAll(sel).length + n`; args are
// passed as JSON-encoded call arguments, never spliced into the source as
// strings. A returned Promise is awaited.
//
// When the page offers EvalCDP (CSP-proof) it is used; otherwise p.Eval.
// A nil/undefined result is an error (ErrEmptyEval) — functions must return a
// value — as is an object carrying a "__monoagent_error" key.
func EvalJSON(p browser.PageInterface, fnSrc string, out interface{}, args ...interface{}) error {
	if p == nil {
		return errors.New("EvalJSON: nil page")
	}
	var raw interface{}
	if ce, ok := p.(cdpEvaler); ok {
		encoded := make([]string, len(args))
		for i, a := range args {
			b, err := json.Marshal(a)
			if err != nil {
				return fmt.Errorf("EvalJSON: encode arg %d: %w", i, err)
			}
			encoded[i] = string(b)
		}
		expr := "(" + fnSrc + ")(" + strings.Join(encoded, ",") + ")"
		v, err := ce.EvalCDP(expr)
		if err != nil {
			return fmt.Errorf("EvalJSON: %w", err)
		}
		raw = v
	} else {
		res, err := p.Eval(fnSrc, args...)
		if err != nil {
			return fmt.Errorf("EvalJSON: %w", err)
		}
		if res != nil {
			raw = res.Raw()
		}
	}
	return decodeEvalValue(raw, out)
}

// decodeEvalValue normalises a driver value (plain Go value or gson.JSON,
// which implements json.Marshaler) through JSON and decodes it into out.
func decodeEvalValue(raw interface{}, out interface{}) error {
	if raw == nil {
		return fmt.Errorf("EvalJSON: %w", ErrEmptyEval)
	}
	b, err := json.Marshal(raw)
	if err != nil {
		return fmt.Errorf("EvalJSON: re-encode result: %w", err)
	}
	trimmed := strings.TrimSpace(string(b))
	if trimmed == "" || trimmed == "null" {
		return fmt.Errorf("EvalJSON: %w", ErrEmptyEval)
	}
	if strings.HasPrefix(trimmed, "{") {
		var probe map[string]json.RawMessage
		if json.Unmarshal(b, &probe) == nil {
			if msg, ok := probe[evalErrorKey]; ok {
				var s string
				if json.Unmarshal(msg, &s) != nil {
					s = string(msg)
				}
				return fmt.Errorf("EvalJSON: page script error: %s", s)
			}
		}
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(b, out); err != nil {
		return fmt.Errorf("EvalJSON: decode result into %T: %w", out, err)
	}
	return nil
}

// FindFirst waits up to timeout for the first of primary or alts to appear,
// using p.Race. It returns the element and the index of the winning selector
// (0 = primary, i = alts[i-1]).
func FindFirst(p browser.PageInterface, primary string, alts []string, timeout time.Duration) (browser.ElementHandle, int, error) {
	sels := make([]string, 0, 1+len(alts))
	if primary != "" {
		sels = append(sels, primary)
	}
	for _, a := range alts {
		if a != "" {
			sels = append(sels, a)
		}
	}
	if len(sels) == 0 {
		return nil, -1, errors.New("FindFirst: no selectors")
	}
	idx, el, err := p.Race(sels, timeout)
	if err != nil {
		return nil, -1, fmt.Errorf("FindFirst: none of %q appeared within %v: %w", sels, timeout, err)
	}
	if el == nil || idx < 0 || idx >= len(sels) {
		return nil, -1, fmt.Errorf("FindFirst: none of %q appeared within %v", sels, timeout)
	}
	if primary == "" {
		idx++ // keep the documented numbering even without a primary
	}
	return el, idx, nil
}

// Outcome is one possible result of an action, recognised by a selector.
type Outcome struct {
	Label    string
	Selector string
}

// WaitForOutcomes waits up to timeout for any outcome's selector to appear.
// When several are present at once the earliest in outs wins, whichever
// driver is underneath.
func WaitForOutcomes(p browser.PageInterface, outs []Outcome, timeout time.Duration) (Outcome, browser.ElementHandle, error) {
	if len(outs) == 0 {
		return Outcome{}, nil, errors.New("WaitForOutcomes: no outcomes")
	}
	sels := make([]string, len(outs))
	for i, o := range outs {
		sels[i] = o.Selector
	}
	idx, el, err := p.Race(sels, timeout)
	if err != nil || el == nil || idx < 0 || idx >= len(outs) {
		if err == nil {
			err = errors.New("no match")
		}
		return Outcome{}, nil, fmt.Errorf("WaitForOutcomes: none of %d outcomes appeared within %v: %w", len(outs), timeout, err)
	}
	// Drivers differ in which of several simultaneously present selectors
	// Race reports; settle ties towards the earliest outcome.
	for idx > 0 {
		j, e, err := p.Race(sels[:idx], 50*time.Millisecond)
		if err != nil || e == nil || j < 0 || j >= idx {
			break
		}
		idx, el = j, e
	}
	return outs[idx], el, nil
}

// markElement tags el with a unique marker attribute so page-side JS can find
// it. ElementHandle has no "run JS on this element" primitive, so a one-shot
// capture-phase focus listener tags whatever el.Focus() targets: the
// extension (and bottest) dispatch a synthetic bubbling "focus" event on the
// element even when it is not natively focusable.
func markElement(p browser.PageInterface, el browser.ElementHandle) (attr, value string, err error) {
	attr = "data-monoagent-mark"
	value = randomToken()
	var armed bool
	if err := EvalJSON(p, `(attr, v) => {
		const h = (e) => {
			const t = e.composedPath ? e.composedPath()[0] : e.target;
			if (t && t.setAttribute) t.setAttribute(attr, v);
			document.removeEventListener('focus', h, true);
		};
		document.addEventListener('focus', h, true);
		window['__mark_' + v] = h;
		return true;
	}`, &armed, attr, value); err != nil {
		return "", "", err
	}
	ferr := el.Focus()
	var found bool
	_ = EvalJSON(p, `(attr, v) => {
		const h = window['__mark_' + v];
		if (h) document.removeEventListener('focus', h, true);
		delete window['__mark_' + v];
		return !!document.querySelector('[' + attr + '="' + v + '"]');
	}`, &found, attr, value)
	if !found {
		if ferr != nil {
			return "", "", fmt.Errorf("mark element: focus: %w", ferr)
		}
		return "", "", errors.New("mark element: element could not be located on the page")
	}
	return attr, value, nil
}

type rect struct {
	X, Y, W, H float64
}

// ClickTrusted clicks el with real (trusted) mouse input when the page
// relays CDP: the element is scrolled into view and a mouseMoved /
// mousePressed / mouseReleased sequence is dispatched at its centre. Pages
// without CDP fall back to el.Click() (synthetic on ExtensionPage).
func ClickTrusted(p browser.PageInterface, el browser.ElementHandle) error {
	if el == nil {
		return errors.New("ClickTrusted: nil element")
	}
	cc, ok := p.(cdpCaller)
	if !ok {
		return el.Click()
	}
	attr, value, err := markElement(p, el)
	if err != nil {
		return fmt.Errorf("ClickTrusted: %w", err)
	}
	var r rect
	if err := EvalJSON(p, `(attr, v) => {
		const el = document.querySelector('[' + attr + '="' + v + '"]');
		if (!el) return { __monoagent_error: 'marked element vanished' };
		el.removeAttribute(attr);
		el.scrollIntoView({ block: 'center', inline: 'center', behavior: 'instant' });
		const b = el.getBoundingClientRect();
		return { X: b.left, Y: b.top, W: b.width, H: b.height };
	}`, &r, attr, value); err != nil {
		return fmt.Errorf("ClickTrusted: %w", err)
	}
	if r.W <= 0 || r.H <= 0 {
		return errors.New("ClickTrusted: element has no visible box")
	}
	x, y := r.X+r.W/2, r.Y+r.H/2
	for _, ev := range []map[string]interface{}{
		{"type": "mouseMoved", "x": x, "y": y},
		{"type": "mousePressed", "x": x, "y": y, "button": "left", "buttons": 1, "clickCount": 1},
		{"type": "mouseReleased", "x": x, "y": y, "button": "left", "buttons": 0, "clickCount": 1},
	} {
		if _, err := cc.CDP("Input.dispatchMouseEvent", ev); err != nil {
			return fmt.Errorf("ClickTrusted: %w", err)
		}
	}
	return nil
}

// PageClickAndWaitNavigation clicks el and waits until a new document has
// replaced the current one (a window token set before the click is gone),
// then waits for it to load. It is the PageInterface counterpart of the Rod
// ClickAndWaitNavigation in humanize.go.
func PageClickAndWaitNavigation(ctx context.Context, p browser.PageInterface, el browser.ElementHandle, timeout time.Duration) error {
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	key := "__monoagent_nav_" + randomToken()
	var ok bool
	if err := EvalJSON(p, `(k) => { window[k] = 1; return true; }`, &ok, key); err != nil {
		return fmt.Errorf("ClickAndWaitNavigation: set token: %w", err)
	}
	if err := ClickTrusted(p, el); err != nil {
		return fmt.Errorf("ClickAndWaitNavigation: click: %w", err)
	}
	deadline := time.Now().Add(timeout)
	for {
		var same bool
		// Errors while the old document unloads are expected; keep polling.
		if err := EvalJSON(p, `(k) => window[k] === 1`, &same, key); err == nil && !same {
			if err := p.WaitLoad(); err != nil {
				return fmt.Errorf("ClickAndWaitNavigation: wait load: %w", err)
			}
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("ClickAndWaitNavigation: no navigation within %v", timeout)
		}
		if err := sleepCtx(ctx, 100*time.Millisecond); err != nil {
			return err
		}
	}
}

// TypeInto focuses el (trusted click when possible) and types text at the
// caret. With CDP it uses Input.insertText — trusted input that React,
// Lexical and other contenteditable editors accept; otherwise el.Input(text),
// which on ExtensionPage clears the field first. TypeInto itself does not
// clear existing content.
func TypeInto(ctx context.Context, p browser.PageInterface, el browser.ElementHandle, text string) error {
	if el == nil {
		return errors.New("TypeInto: nil element")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	cc, ok := p.(cdpCaller)
	if !ok {
		if err := el.Input(text); err != nil {
			return fmt.Errorf("TypeInto: %w", err)
		}
		return nil
	}
	if err := ClickTrusted(p, el); err != nil {
		if ferr := el.Focus(); ferr != nil {
			return fmt.Errorf("TypeInto: focus: %w (click: %v)", ferr, err)
		}
	}
	if _, err := cc.CDP("Input.insertText", map[string]interface{}{"text": text}); err != nil {
		return fmt.Errorf("TypeInto: %w", err)
	}
	return nil
}

// PressEnter presses Enter on the focused element: a trusted CDP key event
// when available, otherwise p.KeyboardPress('\r').
func PressEnter(p browser.PageInterface) error {
	cc, ok := p.(cdpCaller)
	if !ok {
		return p.KeyboardPress('\r')
	}
	base := map[string]interface{}{
		"key": "Enter", "code": "Enter",
		"windowsVirtualKeyCode": 13, "nativeVirtualKeyCode": 13,
	}
	down := map[string]interface{}{"type": "keyDown", "text": "\r", "unmodifiedText": "\r"}
	up := map[string]interface{}{"type": "keyUp"}
	for _, ev := range []map[string]interface{}{down, up} {
		for k, v := range base {
			ev[k] = v
		}
		if _, err := cc.CDP("Input.dispatchKeyEvent", ev); err != nil {
			return fmt.Errorf("PressEnter: %w", err)
		}
	}
	return nil
}

// scrollSettle is how long PageScrollAndCollect waits for lazy content after
// each scroll; a variable so tests can shorten it.
var scrollSettle = 800 * time.Millisecond

// PageScrollAndCollect scrolls the page until at least max elements match sel
// (max <= 0: until the list stops growing) or three scrolls in a row add
// nothing, and returns the matches (at most max). It is the PageInterface
// counterpart of the Rod ScrollAndCollect in humanize.go.
func PageScrollAndCollect(ctx context.Context, p browser.PageInterface, sel string, max int) ([]browser.ElementHandle, error) {
	const maxStale = 3
	var collected []browser.ElementHandle
	prev, stale := -1, 0
	for {
		if err := ctx.Err(); err != nil {
			return collected, err
		}
		els, err := p.Elements(sel)
		if err != nil {
			return collected, fmt.Errorf("ScrollAndCollect: query %q: %w", sel, err)
		}
		collected = els
		if max > 0 && len(collected) >= max {
			return collected[:max], nil
		}
		if len(collected) == prev {
			stale++
			if stale >= maxStale {
				return collected, nil
			}
		} else {
			stale = 0
		}
		prev = len(collected)
		if err := p.MouseScroll(0, 600, 3); err != nil {
			return collected, fmt.Errorf("ScrollAndCollect: scroll: %w", err)
		}
		if err := sleepCtx(ctx, scrollSettle); err != nil {
			return collected, err
		}
	}
}

// Args unpacks the arguments call_bot_method passes to a bot method:
// args[0] must be the browser.PageInterface, and the next n arguments are
// coerced to strings (nil ⇒ "", whole float64 ⇒ no exponent, so numeric IDs
// such as 123456789012 survive JSON decoding). names labels those n
// arguments for error messages; an argument whose name ends in "?" — or that
// has no name — is optional, every other one must be non-empty.
func Args(args []interface{}, n int, names ...string) (browser.PageInterface, []string, error) {
	if len(args) == 0 {
		return nil, nil, errors.New("missing page argument")
	}
	p, ok := args[0].(browser.PageInterface)
	if !ok || p == nil {
		return nil, nil, fmt.Errorf("first argument must be a browser page, got %T", args[0])
	}
	out := make([]string, n)
	for i := 0; i < n; i++ {
		if 1+i < len(args) {
			out[i] = argString(args[1+i])
		}
		if i < len(names) {
			name := names[i]
			if name != "" && !strings.HasSuffix(name, "?") && strings.TrimSpace(out[i]) == "" {
				return nil, nil, fmt.Errorf("missing required argument %q (position %d)", name, i+1)
			}
		}
	}
	return p, out, nil
}

func argString(v interface{}) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		return x
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64)
	case float32:
		return strconv.FormatFloat(float64(x), 'f', -1, 32)
	case json.Number:
		return x.String()
	case fmt.Stringer:
		return x.String()
	default:
		return fmt.Sprint(v)
	}
}

func randomToken() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return strconv.FormatInt(time.Now().UnixNano(), 36)
	}
	return hex.EncodeToString(b[:])
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}
