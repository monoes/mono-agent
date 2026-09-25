//go:build !nosocial

package instagram

// Driver-agnostic page plumbing shared by the Instagram methods. Everything
// here works on browser.PageInterface — in production the user's own browser
// through the extension (*extension.ExtensionPage) — and uses the helpers in
// internal/bot/pagehelpers.go: EvalJSON for page scripts, ClickTrusted for
// real mouse clicks, CDP Input.insertText for typing.

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	botpkg "github.com/monoes/mono-agent/internal/bot"
	"github.com/monoes/mono-agent/internal/browser"
	"github.com/monoes/mono-agent/internal/jevpick"
)

// Timing. Variables so tests can shorten them.
var (
	// pageSettle is how long to let the single-page app render after a load.
	pageSettle = 2500 * time.Millisecond
	// uiPause separates UI steps (a click and the next lookup).
	uiPause = 800 * time.Millisecond
	// findTimeout bounds waiting for a control to render.
	findTimeout = 12 * time.Second
	// verifyTimeout bounds waiting for a write's visible confirmation.
	verifyTimeout = 12 * time.Second
	// pollEvery is the polling interval of finders and verifiers.
	pollEvery = 250 * time.Millisecond
	// storyDwell is how long each story stays on screen before advancing.
	storyDwell = 3 * time.Second
	// publishTimeout bounds waiting for "Your post has been shared".
	publishTimeout = 90 * time.Second
)

// markAttr tags elements the page scripts selected, for the page driver to
// find (see jsLib's mark()).
const markAttr = "data-monoagent-ig"

// ErrNotLoggedIn is returned when Instagram redirected to its login page.
var ErrNotLoggedIn = errors.New("instagram: not logged in (Instagram showed its login page)")

// cdpPage is the raw-DevTools capability of ExtensionPage (and bottest.Page).
type cdpPage interface {
	CDP(method string, params map[string]interface{}) (map[string]interface{}, error)
}

func pause(ctx context.Context, d time.Duration) error {
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

// evalJS runs a page script (see js.go) and decodes its result into out.
func evalJS(p browser.PageInterface, body string, out interface{}, args ...interface{}) error {
	return botpkg.EvalJSON(p, js(body), out, args...)
}

// errTimeout is wrapped by poll when check never reported done.
var errTimeout = errors.New("timed out")

// poll calls check every pollEvery until it reports done, fails hard (err
// wrapped in stopErr), or timeout passes. A plain error from check is
// remembered and retried (the page may be mid-navigation).
func poll(ctx context.Context, timeout time.Duration, check func() (bool, error)) error {
	deadline := time.Now().Add(timeout)
	var last error
	for {
		done, err := check()
		var se stopErr
		if errors.As(err, &se) {
			return se.err
		}
		if err == nil && done {
			return nil
		}
		if err != nil {
			last = err
		}
		if time.Now().After(deadline) {
			if last != nil {
				return fmt.Errorf("%w after %v: %v", errTimeout, timeout, last)
			}
			return fmt.Errorf("%w after %v", errTimeout, timeout)
		}
		if err := pause(ctx, pollEvery); err != nil {
			return err
		}
	}
}

// stopErr makes poll give up immediately.
type stopErr struct{ err error }

func (s stopErr) Error() string { return s.err.Error() }

func newMark() string {
	var b [6]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("m%d", time.Now().UnixNano())
	}
	return "m" + hex.EncodeToString(b[:])
}

func markSel(m string) string { return fmt.Sprintf("[%s='%s']", markAttr, m) }

// clickMarked clicks the element a page script marked with m, with trusted
// mouse input where the page relays CDP.
func clickMarked(p browser.PageInterface, m string) error {
	el, err := p.Element(markSel(m), 5*time.Second)
	if err != nil || el == nil {
		if err == nil {
			err = errors.New("not found")
		}
		return fmt.Errorf("marked element vanished before the click: %w", err)
	}
	return botpkg.ClickTrusted(p, el)
}

// unmark removes marker m (best effort).
func unmark(p browser.PageInterface, m string) {
	var ok bool
	_ = evalJS(p, `(m) => { document.querySelectorAll('['+MARK+'="'+m+'"]').forEach((e) => e.removeAttribute(MARK)); return true; }`, &ok, m)
}

// typeAtEnd focuses the marked input, moves the caret to the end of its
// content and inserts text — trusted CDP Input.insertText where available
// (accepted by React inputs and Lexical editors), el.Input otherwise.
func typeAtEnd(ctx context.Context, p browser.PageInterface, m, text string) error {
	el, err := p.Element(markSel(m), 5*time.Second)
	if err != nil || el == nil {
		return fmt.Errorf("input vanished before typing: %v", err)
	}
	cp, hasCDP := p.(cdpPage)
	if hasCDP {
		if cerr := botpkg.ClickTrusted(p, el); cerr != nil {
			_ = el.Focus()
		}
	} else {
		_ = el.Focus()
	}
	var before string
	if err := evalJS(p, `(m) => {
		const el = document.querySelector('['+MARK+'="'+m+'"]');
		if (!el) return { __monoagent_error: 'input vanished' };
		el.focus();
		if ('value' in el && el.setSelectionRange) { const n = el.value.length; el.setSelectionRange(n, n); }
		else { const r = document.createRange(); r.selectNodeContents(el); r.collapse(false); const s = getSelection(); s.removeAllRanges(); s.addRange(r); }
		return boxValue(el);
	}`, &before, m); err != nil {
		return err
	}
	if err := pause(ctx, 200*time.Millisecond); err != nil {
		return err
	}
	if hasCDP {
		if _, err := cp.CDP("Input.insertText", map[string]interface{}{"text": text}); err != nil {
			return fmt.Errorf("type: %w", err)
		}
	} else if err := el.Input(before + text); err != nil {
		return fmt.Errorf("type: %w", err)
	}
	// Confirm the input took the text (a React input that ignored the event
	// keeps its old value).
	return poll(ctx, 3*time.Second, func() (bool, error) {
		var v string
		if err := evalJS(p, `(m) => boxValue(document.querySelector('['+MARK+'="'+m+'"]'))`, &v, m); err != nil {
			return false, err
		}
		return strings.Contains(normSpace(v), normSpace(text)), nil
	})
}

func normSpace(s string) string { return strings.Join(strings.Fields(s), " ") }

// open navigates to url, waits for the app to render, refuses a login
// redirect and dismisses the "Turn on Notifications" style dialogs.
func (b *InstagramBot) open(ctx context.Context, p browser.PageInterface, url string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := p.Navigate(url); err != nil {
		return fmt.Errorf("instagram: navigate to %s: %w", url, err)
	}
	if err := p.WaitLoad(); err != nil {
		return fmt.Errorf("instagram: %s did not load: %w", url, err)
	}
	if err := pause(ctx, pageSettle); err != nil {
		return err
	}
	var login bool
	if err := evalJS(p, `() => location.pathname.startsWith('/accounts/login') ||
		!!(document.querySelector('form input[name="username"]') && document.querySelector('form input[name="password"]'))`, &login); err == nil && login {
		return ErrNotLoggedIn
	}
	dismissInterstitials(ctx, p)
	return nil
}

// dismissInterstitials clicks "Not Now" on a visible Instagram dialog (Turn
// on Notifications, Save login info). It never touches anything else.
func dismissInterstitials(ctx context.Context, p browser.PageInterface) {
	for i := 0; i < 2; i++ {
		m := newMark()
		var found bool
		if err := evalJS(p, `(m) => {
			for (const d of dialogs()) {
				const b = byText(d, /^not now$/i)[0];
				if (b) { mark(b, m); return true; }
			}
			return false;
		}`, &found, m); err != nil || !found {
			return
		}
		_ = clickMarked(p, m)
		_ = pause(ctx, uiPause)
	}
}

// describe summarises the element at sel for validating a Jev pick.
type described struct {
	Found    bool   `json:"found"`
	Text     string `json:"text"`
	Label    string `json:"label"`
	SVGLabel string `json:"svg"`
	InList   bool   `json:"inList"`
	InDialog bool   `json:"inDialog"`
	InHeader bool   `json:"inHeader"`
	Tag      string `json:"tag"`
}

func describe(p browser.PageInterface, sel, m string) (described, error) {
	var d described
	err := evalJS(p, `(sel, m) => {
		const el = document.querySelector(sel);
		if (!el) return { found: false };
		const b = el.matches('textarea, input, [contenteditable="true"], [role="textbox"]') ? el : (btnOf(el) || el);
		mark(b, m);
		const svg = b.matches('svg[aria-label]') ? b : b.querySelector('svg[aria-label]');
		return { found: true, text: T(b), label: lbl(b) || lbl(el), svg: svg ? lbl(svg) : '',
			inList: !!b.closest('li, ul'), inDialog: !!b.closest('[role="dialog"]'), inHeader: !!b.closest('header'),
			tag: b.tagName.toLowerCase() };
	}`, &d, sel, m)
	return d, err
}

// jevPick asks Jev (when this bot has a picker) for the element described by
// intent, marks its clickable ancestor with m and returns its description.
// The caller validates the description before acting on it.
func (b *InstagramBot) jevPick(ctx context.Context, p browser.PageInterface, kind, intent, hint, m string) (described, error) {
	if !b.JevAvailable(p) {
		return described{}, botpkg.ErrJevUnavailable
	}
	pick, err := b.JevElement(ctx, p, jevpick.Target{Kind: kind, Intent: intent, Hint: hint})
	if err != nil {
		return described{}, err
	}
	defer pick.Release()
	d, err := describe(p, pick.Selector, m)
	if err != nil {
		return d, err
	}
	if !d.Found {
		return d, errors.New("jev pick vanished")
	}
	return d, nil
}

// pressArrowRight presses the right arrow key (trusted via CDP when possible).
func pressArrowRight(p browser.PageInterface) error {
	cp, ok := p.(cdpPage)
	if !ok {
		return p.KeyboardPress('') // Rod/extension ArrowRight
	}
	for _, typ := range []string{"keyDown", "keyUp"} {
		if _, err := cp.CDP("Input.dispatchKeyEvent", map[string]interface{}{
			"type": typ, "key": "ArrowRight", "code": "ArrowRight",
			"windowsVirtualKeyCode": 39, "nativeVirtualKeyCode": 39,
		}); err != nil {
			return err
		}
	}
	return nil
}

// pressEnter presses Enter on the focused element (trusted via CDP).
func pressEnter(p browser.PageInterface) error { return botpkg.PressEnter(p) }
