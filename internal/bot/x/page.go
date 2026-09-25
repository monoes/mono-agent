//go:build !nosocial

package x

// PageInterface helpers shared by the X bot methods. Everything here runs on
// whatever browser.PageInterface the session hands the bot — in production
// *extension.ExtensionPage — through bot.EvalJSON (CDP evaluation, immune to
// x.com's CSP), bot.ClickTrusted and bot.TypeInto.

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	botpkg "github.com/monoes/mono-agent/internal/bot"
	"github.com/monoes/mono-agent/internal/browser"
)

// markAttr is the attribute page scripts set on an element the bot then
// resolves through the driver (page.Element) to click or type into it.
const markAttr = "data-monoagent-x"

// Timings. Variables so tests can shorten them.
var (
	// loadTimeout bounds waiting for a page's main content to render.
	loadTimeout = 20 * time.Second
	// pollInterval is how often page state is re-read while waiting.
	pollInterval = 250 * time.Millisecond
	// verifyTimeout bounds waiting for a write's outcome to show.
	verifyTimeout = 12 * time.Second
	// scrollSettle is the wait after each scroll for lazy content.
	scrollSettle = 1200 * time.Millisecond
	// actionPause is the short human-ish pause between UI steps.
	actionPause = 400 * time.Millisecond
)

// errNotLoggedIn is returned when X sent the page to its login flow.
var errNotLoggedIn = errors.New("x: not logged in (redirected to the login flow)")

// sleep waits d or until ctx is done.
func sleep(ctx context.Context, d time.Duration) error {
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

// poll calls fn every pollInterval until it reports done, returns an error,
// ctx ends, or timeout passes (errTimeout).
func poll(ctx context.Context, timeout time.Duration, fn func() (bool, error)) error {
	deadline := time.Now().Add(timeout)
	for {
		done, err := fn()
		if err != nil {
			return err
		}
		if done {
			return nil
		}
		if time.Now().After(deadline) {
			return errTimeout
		}
		if err := sleep(ctx, pollInterval); err != nil {
			return err
		}
	}
}

var errTimeout = errors.New("timed out")

// token returns a random marker value.
func token() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return strconv.FormatInt(time.Now().UnixNano(), 36)
	}
	return hex.EncodeToString(b[:])
}

// markedSelector selects the element a page script marked with tok.
func markedSelector(tok string) string {
	return fmt.Sprintf("[%s='%s']", markAttr, tok)
}

// clearMarks removes every marker this bot set on the page.
func clearMarks(p browser.PageInterface) {
	var ok bool
	_ = botpkg.EvalJSON(p, `(attr) => { for (const e of document.querySelectorAll('['+attr+']')) e.removeAttribute(attr); return true; }`, &ok, markAttr)
}

// navigate loads u and fails when X bounced the page to its login flow.
func navigate(p browser.PageInterface, u string) error {
	if err := p.Navigate(u); err != nil {
		return fmt.Errorf("x: navigate to %s: %w", u, err)
	}
	if err := p.WaitLoad(); err != nil {
		return fmt.Errorf("x: %s did not load: %w", u, err)
	}
	return checkLoginRedirect(p)
}

// checkLoginRedirect returns errNotLoggedIn when the page sits on X's login
// or onboarding flow.
func checkLoginRedirect(p browser.PageInterface) error {
	cur, err := p.GetURL()
	if err != nil {
		return nil // unknown; later waits report what is missing
	}
	if isLoginURL(cur) {
		return errNotLoggedIn
	}
	return nil
}

func isLoginURL(u string) bool {
	pu, err := url.Parse(u)
	if err != nil {
		return false
	}
	path := strings.ToLower(pu.Path)
	return strings.HasPrefix(path, "/i/flow/login") || path == "/login" || strings.HasPrefix(path, "/login/") ||
		strings.HasPrefix(path, "/i/jf/onboarding") || strings.HasPrefix(path, "/i/flow/signup")
}

// canonicalHost rewrites twitter.com and its variants to x.com, where the
// app actually lives.
func canonicalHost(u *url.URL) {
	switch strings.ToLower(u.Host) {
	case "twitter.com", "www.twitter.com", "mobile.twitter.com", "mobile.x.com", "www.x.com", "x.com":
		u.Host = "x.com"
		u.Scheme = "https"
	}
}

var handleRe = regexp.MustCompile(`^@?([A-Za-z0-9_]{1,15})$`)

// profileURL turns a profile target — a URL, "/handle", "@handle" or
// "handle" — into https://x.com/<handle> and returns the handle too.
func (b *XBot) profileURL(target string) (string, string, error) {
	target = strings.TrimSpace(target)
	if target == "" {
		return "", "", errors.New("x: profile URL or username is required")
	}
	if m := handleRe.FindStringSubmatch(target); m != nil {
		return "https://x.com/" + m[1], m[1], nil
	}
	u, err := url.Parse(b.ResolveURL(target))
	if err != nil || u.Host == "" {
		return "", "", fmt.Errorf("x: %q is not a profile URL or username", target)
	}
	canonicalHost(u)
	if u.Host != "x.com" {
		return "", "", fmt.Errorf("x: %q is not an x.com profile URL", target)
	}
	handle := b.ExtractUsername(u.String())
	if handle == "" || !handleRe.MatchString(handle) {
		return "", "", fmt.Errorf("x: no username in %q", target)
	}
	return "https://x.com/" + handle, handle, nil
}

var statusRe = regexp.MustCompile(`/status(?:es)?/(\d+)`)

// postURL normalises a post (status) URL and returns it with its id.
func (b *XBot) postURL(target string) (string, string, error) {
	target = strings.TrimSpace(target)
	if target == "" {
		return "", "", errors.New("x: post URL is required")
	}
	u, err := url.Parse(b.ResolveURL(target))
	if err != nil || u.Host == "" {
		return "", "", fmt.Errorf("x: %q is not a post URL", target)
	}
	canonicalHost(u)
	if u.Host != "x.com" {
		return "", "", fmt.Errorf("x: %q is not an x.com post URL", target)
	}
	m := statusRe.FindStringSubmatch(u.Path)
	if m == nil {
		return "", "", fmt.Errorf("x: %q is not a post URL (no /status/<id>)", target)
	}
	u.RawQuery, u.Fragment = "", ""
	return u.String(), m[1], nil
}

// countRe matches the leading count of an X counter ("1,234", "12.5K",
// "60.7m", "3 B").
var countRe = regexp.MustCompile(`^([0-9][0-9.,\s\x{00a0}\x{202f}]*)\s*([KkMmBb])?`)

// parseCount converts an X counter text to a number; ok is false when the
// text holds no count.
func parseCount(s string) (int64, bool) {
	s = strings.TrimSpace(s)
	m := countRe.FindStringSubmatch(s)
	if m == nil {
		return 0, false
	}
	num := strings.NewReplacer(" ", "", " ", "", " ", "").Replace(strings.TrimSpace(m[1]))
	mult := 1.0
	switch strings.ToLower(m[2]) {
	case "k":
		mult = 1e3
	case "m":
		mult = 1e6
	case "b":
		mult = 1e9
	}
	if mult > 1 {
		// "12.5K" or (some locales) "12,5K": one decimal separator.
		num = strings.Replace(num, ",", ".", 1)
		f, err := strconv.ParseFloat(num, 64)
		if err != nil {
			return 0, false
		}
		return int64(f*mult + 0.5), true
	}
	// Plain counts: "," and "." are thousands separators.
	num = strings.NewReplacer(",", "", ".", "").Replace(num)
	n, err := strconv.ParseInt(num, 10, 64)
	if err != nil {
		return 0, false
	}
	return n, true
}

// intArg parses a numeric bot-method argument, with def for empty/invalid.
func intArg(s string, def int) int {
	n, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil || n <= 0 {
		return def
	}
	return n
}

// boolArg parses a boolean bot-method argument, with def for empty/invalid.
func boolArg(s string, def bool) bool {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "true", "1", "yes", "y", "on":
		return true
	case "false", "0", "no", "n", "off":
		return false
	}
	return def
}

// truncateForError shortens a string for inclusion in error messages.
func truncateForError(s string, max int) string {
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max]) + "…"
}

// messageSnippet returns the searchable portion of a message used to match a
// rendered message bubble (long messages may be visually truncated by the UI).
func messageSnippet(message string) string {
	const maxSnippet = 80
	r := []rune(strings.TrimSpace(message))
	if len(r) > maxSnippet {
		return string(r[:maxSnippet])
	}
	return string(r)
}

// markedElement resolves an element a page script marked with tok.
func markedElement(p browser.PageInterface, tok string) (browser.ElementHandle, error) {
	el, err := p.Element(markedSelector(tok), 3*time.Second)
	if err == nil && el == nil {
		err = errors.New("marked element not found")
	}
	return el, err
}
