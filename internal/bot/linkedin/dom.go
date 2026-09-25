//go:build social

package linkedin

// Page-side plumbing shared by every LinkedIn method. Everything runs through
// browser.PageInterface: in production that is *extension.ExtensionPage,
// whose plain Eval is blocked by LinkedIn's CSP, so page scripts always go
// through botpkg.EvalJSON (EvalCDP when the page offers it). Input is real
// (trusted) CDP mouse/keyboard input whenever the page relays CDP.
//
// Scripts locate a target, tag it with a one-off data-monoagent-li token and
// report its state; Go then acts on the tagged element. Lookups also search
// open shadow roots, because LinkedIn renders its messaging overlay inside
// one (#interop-outlet).

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	botpkg "github.com/monoes/mono-agent/internal/bot"
	"github.com/monoes/mono-agent/internal/browser"
)

// cdpCaller is implemented by pages that relay raw DevTools commands
// (ExtensionPage.CDP, bottest.Page.CDP).
type cdpCaller interface {
	CDP(method string, params map[string]interface{}) (map[string]interface{}, error)
}

// Timing knobs; tests shorten them.
var (
	// pageSettle is the pause after a navigation for LinkedIn's client-side
	// rendering to start.
	pageSettle = 2 * time.Second
	// uiSettle is the pause after a click that opens a menu or editor.
	uiSettle = 700 * time.Millisecond
	// pollEvery is the polling interval of every wait.
	pollEvery = 250 * time.Millisecond
	// findTimeout bounds waiting for a page's main content to render.
	findTimeout = 15 * time.Second
	// verifyTimeout bounds waiting for a write to show up on the page.
	verifyTimeout = 10 * time.Second
	// scrollSettle is the pause after a scroll for lazy content.
	scrollSettle = 1500 * time.Millisecond
)

// markAttr is the attribute scripts tag targets with.
const markAttr = "data-monoagent-li"

// jsLib is prepended to every page function. It must stay free of Go
// raw-string backquotes.
const jsLib = `
const L = {
  deepAll(sel, root) {
    const out = [];
    const walk = (r) => {
      try { r.querySelectorAll(sel).forEach((e) => out.push(e)); } catch (e) {}
      r.querySelectorAll('*').forEach((el) => { if (el.shadowRoot) walk(el.shadowRoot); });
    };
    walk(root || document);
    return out;
  },
  vis(el) {
    if (!el || !el.isConnected) return false;
    const r = el.getBoundingClientRect();
    if (r.width <= 0 || r.height <= 0) return false;
    const s = getComputedStyle(el);
    return s.visibility !== 'hidden' && s.display !== 'none';
  },
  text(el) { return el ? (el.innerText || el.textContent || '').replace(/\s+/g, ' ').trim() : ''; },
  lines(el) {
    if (!el) return [];
    const out = [];
    for (const raw of (el.innerText || '').split('\n')) {
      const t = raw.replace(/\s+/g, ' ').trim();
      if (t && out[out.length - 1] !== t) out.push(t);
    }
    return out;
  },
  label(el) { return el ? ((el.getAttribute('aria-label') || '').trim() || L.text(el)) : ''; },
  mark(el, tok) { el.setAttribute('` + markAttr + `', tok); return tok; },
  marked(tok) { return L.deepAll('[` + markAttr + `="' + tok + '"]')[0] || null; },
  unmarkAll(tok) { L.deepAll('[` + markAttr + `="' + tok + '"]').forEach((e) => e.removeAttribute('` + markAttr + `')); },
  num(s) {
    const m = String(s || '').replace(/,/g, '').match(/(\d+(?:\.\d+)?)\s*([KkMm])?/);
    if (!m) return 0;
    let n = parseFloat(m[1]);
    if (m[2]) n *= (m[2].toLowerCase() === 'k' ? 1000 : 1000000);
    return Math.round(n);
  },
  // bareName drops the connection-degree / relationship suffix LinkedIn
  // renders next to a name ("Ann Lee • 2nd", "Ann Lee · 3rd+", "Ann Lee • You").
  bareName(s) {
    return String(s || '').replace(/\s*[•·]\s*(?:1st|2nd|3rd\+?|You|Following|Author)\b.*$/i, '').trim();
  },
  // a11yOnly: the texts of screen-reader-only elements inside el — presence
  // ("Status is online"), degree descriptions and the like. They render (so
  // innerText includes them) but are never visible content.
  a11yOnly(el) {
    const out = new Set();
    if (!el) return out;
    el.querySelectorAll('.visually-hidden, .a11y-text, [class*="presence-entity__indicator"], [class*="presence-indicator"]').forEach((x) => {
      const t = L.text(x);
      if (t) out.add(t);
    });
    return out;
  },
  activeDeep() {
    let a = document.activeElement;
    while (a && a.shadowRoot && a.shadowRoot.activeElement) a = a.shadowRoot.activeElement;
    return a;
  },
  // convo returns the conversation container of a message composer: a known
  // bubble/thread class, else the nearest ancestor that has a heading.
  convo(e) {
    const known = e.closest('.msg-overlay-conversation-bubble, .msg-convo-wrapper, .msg-thread, [data-view-name*="conversation"], [role="dialog"]');
    if (known) return known;
    let c = e.parentElement;
    for (let i = 0; c && i < 8 && c !== document.body && c.tagName !== 'MAIN'; i++, c = c.parentElement) {
      if (c.querySelector('h1, h2, h3, h4, header')) return c;
    }
    return null;
  },
  editorText(el) {
    if (!el) return '';
    if ('value' in el && typeof el.value === 'string') return el.value.trim();
    return (el.innerText || el.textContent || '').replace(/ /g, ' ').trim();
  },
};
`

// run evaluates body — a JS function source — with jsLib in scope and
// decodes its result into out.
func run(p browser.PageInterface, body string, out interface{}, args ...interface{}) error {
	fn := "(...__a) => {" + jsLib + "\nreturn (" + body + ")(...__a); }"
	return botpkg.EvalJSON(p, fn, out, args...)
}

// newToken returns a fresh marker token.
func newToken(prefix string) string {
	return fmt.Sprintf("%s-%d", prefix, time.Now().UnixNano())
}

// unmark removes a marker token from the page; errors are ignored (the page
// may have navigated).
func unmark(p browser.PageInterface, tok string) {
	var ok bool
	_ = run(p, `(tok) => { L.unmarkAll(tok); return true; }`, &ok, tok)
}

// sleepCtx waits d or until ctx is done.
func sleepCtx(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return ctx.Err()
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// errTimeout is returned by poll when the condition never held.
var errTimeout = errors.New("timed out")

// poll calls cond every pollEvery until it reports true, returns an error,
// timeout elapses (errTimeout) or ctx ends.
func poll(ctx context.Context, timeout time.Duration, cond func() (bool, error)) error {
	deadline := time.Now().Add(timeout)
	for {
		ok, err := cond()
		if err != nil {
			return err
		}
		if ok {
			return nil
		}
		if time.Now().After(deadline) {
			return errTimeout
		}
		if err := sleepCtx(ctx, pollEvery); err != nil {
			return err
		}
	}
}

// navigate loads url and gives the client-side app a moment to render.
func navigate(ctx context.Context, p browser.PageInterface, url string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := p.Navigate(url); err != nil {
		return fmt.Errorf("linkedin: navigate to %s: %w", url, err)
	}
	if err := p.WaitLoad(); err != nil {
		return fmt.Errorf("linkedin: %s did not load: %w", url, err)
	}
	return sleepCtx(ctx, pageSettle)
}

// waitFor polls a page function until it returns a truthy "ok" field and
// decodes that result into out.
func waitFor(ctx context.Context, p browser.PageInterface, timeout time.Duration, body string, out interface{}, args ...interface{}) error {
	var last error
	err := poll(ctx, timeout, func() (bool, error) {
		var probe struct {
			OK bool `json:"ok"`
		}
		var raw map[string]interface{}
		if err := run(p, body, &raw, args...); err != nil {
			last = err
			return false, nil
		}
		b, _ := jsonMarshal(raw)
		_ = jsonUnmarshal(string(b), &probe)
		if !probe.OK {
			return false, nil
		}
		if out != nil {
			return true, jsonUnmarshal(string(b), out)
		}
		return true, nil
	})
	if errors.Is(err, errTimeout) && last != nil {
		return fmt.Errorf("%w (last page error: %v)", err, last)
	}
	return err
}

type box struct {
	X, Y, W, H float64
	Hit        bool
}

// markedBox scrolls the marked element into view and returns its viewport
// box, and whether the element (not something covering it) is what a click
// at its centre would hit.
func markedBox(p browser.PageInterface, tok string) (box, error) {
	var b box
	err := run(p, `(tok) => {
		const el = L.marked(tok);
		if (!el) return { __monoagent_error: 'marked element not found' };
		el.scrollIntoView({ block: 'center', inline: 'center', behavior: 'instant' });
		const r = el.getBoundingClientRect();
		const x = r.left + r.width / 2, y = r.top + r.height / 2;
		let hit = document.elementFromPoint(x, y);
		while (hit && hit.shadowRoot) {
			const inner = hit.shadowRoot.elementFromPoint(x, y);
			if (!inner || inner === hit) break;
			hit = inner;
		}
		const ok = !!hit && (hit === el || el.contains(hit) || (hit.contains && hit.contains(el) && hit.children.length === 0));
		return { X: r.left, Y: r.top, W: r.width, H: r.height, Hit: ok };
	}`, &b, tok)
	return b, err
}

// clickMarked clicks the element tagged tok: trusted CDP mouse input at its
// centre when the page relays CDP and nothing covers it, else a DOM click.
func clickMarked(p browser.PageInterface, tok string) error {
	b, err := markedBox(p, tok)
	if err != nil {
		return err
	}
	if b.W <= 0 || b.H <= 0 {
		return errors.New("element has no visible box")
	}
	if cc, ok := p.(cdpCaller); ok && b.Hit {
		x, y := b.X+b.W/2, b.Y+b.H/2
		for _, ev := range []map[string]interface{}{
			{"type": "mouseMoved", "x": x, "y": y},
			{"type": "mousePressed", "x": x, "y": y, "button": "left", "buttons": 1, "clickCount": 1},
			{"type": "mouseReleased", "x": x, "y": y, "button": "left", "buttons": 0, "clickCount": 1},
		} {
			if _, err := cc.CDP("Input.dispatchMouseEvent", ev); err != nil {
				return fmt.Errorf("mouse input: %w", err)
			}
		}
		return nil
	}
	if el, err := p.Element("["+markAttr+"=\""+tok+"\"]", time.Second); err == nil && el != nil {
		return el.Click()
	}
	var ok bool
	return run(p, `(tok) => {
		const el = L.marked(tok);
		if (!el) return { __monoagent_error: 'marked element not found' };
		for (const t of ['pointerdown', 'mousedown', 'pointerup', 'mouseup']) el.dispatchEvent(new MouseEvent(t, { bubbles: true, composed: true }));
		el.click();
		return true;
	}`, &ok, tok)
}

// hoverMarked moves the (trusted) mouse over the tagged element, which opens
// hover menus such as LinkedIn's reactions picker. Without CDP it dispatches
// mouse events, which LinkedIn's hover triggers may ignore.
func hoverMarked(p browser.PageInterface, tok string) error {
	b, err := markedBox(p, tok)
	if err != nil {
		return err
	}
	if cc, ok := p.(cdpCaller); ok {
		x, y := b.X+b.W/2, b.Y+b.H/2
		for _, d := range []float64{-6, -2, 0} {
			if _, err := cc.CDP("Input.dispatchMouseEvent", map[string]interface{}{"type": "mouseMoved", "x": x + d, "y": y}); err != nil {
				return fmt.Errorf("mouse move: %w", err)
			}
		}
		return nil
	}
	var ok bool
	return run(p, `(tok) => {
		const el = L.marked(tok);
		if (!el) return { __monoagent_error: 'marked element not found' };
		for (const t of ['pointerover', 'pointerenter', 'mouseover', 'mouseenter', 'mousemove']) el.dispatchEvent(new MouseEvent(t, { bubbles: true, composed: true }));
		return true;
	}`, &ok, tok)
}

// typeMarked focuses the tagged editor and types text at its caret with
// trusted input (CDP Input.insertText — accepted by LinkedIn's Quill and
// contenteditable editors), then checks the editor now holds the text.
func typeMarked(ctx context.Context, p browser.PageInterface, tok, text string) error {
	if err := clickMarked(p, tok); err != nil {
		return fmt.Errorf("focus editor: %w", err)
	}
	if err := sleepCtx(ctx, 150*time.Millisecond); err != nil {
		return err
	}
	var focused struct {
		Focused bool `json:"focused"`
	}
	if err := run(p, `(tok) => {
		const el = L.marked(tok);
		if (!el) return { __monoagent_error: 'editor vanished' };
		const a = L.activeDeep();
		if (!(a === el || el.contains(a))) {
			el.focus();
			if (el.isContentEditable) {
				const sel = (el.getRootNode().getSelection ? el.getRootNode() : window).getSelection();
				const r = document.createRange();
				r.selectNodeContents(el);
				r.collapse(false);
				sel.removeAllRanges();
				sel.addRange(r);
			}
		}
		const b = L.activeDeep();
		return { focused: b === el || el.contains(b) };
	}`, &focused, tok); err != nil {
		return fmt.Errorf("focus editor: %w", err)
	}
	if cc, ok := p.(cdpCaller); ok {
		if !focused.Focused {
			return errors.New("editor did not take focus")
		}
		if _, err := cc.CDP("Input.insertText", map[string]interface{}{"text": text}); err != nil {
			return fmt.Errorf("insert text: %w", err)
		}
	} else if el, err := p.Element("["+markAttr+"=\""+tok+"\"]", time.Second); err == nil && el != nil {
		if err := el.Input(text); err != nil {
			return fmt.Errorf("type: %w", err)
		}
	} else {
		var ok bool
		if err := run(p, `(tok, text) => {
			const el = L.marked(tok);
			if (!el) return { __monoagent_error: 'editor vanished' };
			el.focus();
			return document.execCommand('insertText', false, text);
		}`, &ok, tok, text); err != nil {
			return fmt.Errorf("type: %w", err)
		}
	}
	want := snippet(text)
	return poll(ctx, 3*time.Second, func() (bool, error) {
		var got string
		if err := run(p, `(tok) => { const el = L.marked(tok); return el ? L.editorText(el) : ''; }`, &got, tok); err != nil {
			return false, nil
		}
		return strings.Contains(normSpace(got), want), nil
	})
}

// snippet is the searchable part of a typed text: whitespace-normalised and
// cut to 60 runes (LinkedIn truncates long comments/messages visually).
func snippet(text string) string {
	r := []rune(normSpace(text))
	if len(r) > 60 {
		r = r[:60]
	}
	return string(r)
}

func normSpace(s string) string {
	return strings.Join(strings.Fields(strings.ReplaceAll(s, " ", " ")), " ")
}

// scrollPage scrolls the window down by about two screens.
func scrollPage(p browser.PageInterface) {
	var ok bool
	if err := run(p, `() => { window.scrollBy(0, window.innerHeight * 2); return true; }`, &ok); err != nil {
		_ = p.MouseScroll(0, 1200, 3)
	}
}
