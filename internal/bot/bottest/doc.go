// Package bottest is a real-browser test harness for the social bots under
// internal/bot/<platform>. It launches a private headless Chromium, serves
// every request from local fixtures (nothing ever reaches the network), and
// hands the bot a page that behaves like the one it meets in production.
//
// # Why
//
// In production a bot's page is an *extension.ExtensionPage: the user's own
// browser driven through the Chrome extension. Its semantics differ from
// Rod's in ways that break bots silently — synthetic (untrusted) clicks,
// Input that clears first, element handles that go stale on navigation, and
// an Eval that runs through `new Function` in the page's MAIN world, so a
// page CSP without 'unsafe-eval' blocks it. bottest.Page reproduces those
// semantics over a direct DevTools connection so bot code can be exercised
// end to end in `go test`, without a login, an extension, or the network.
//
// # Using it from a platform test
//
// Platform packages carry the `social` build tag; their tests do too:
//
//	//go:build social
//
//	package instagram
//
//	func TestLikePost(t *testing.T) {
//		b := bottest.Launch(t) // skips unless $BOTTEST_BROWSER is set
//		page := b.NewPage(t)
//		rec := page.Serve(
//			bottest.Route{Pattern: "https://www.instagram.com/p/*", File: "testdata/post.html"},
//			bottest.Route{Pattern: "https://www.instagram.com/api/v1/web/likes/*", Method: "POST",
//				Body: `{"status":"ok"}`, ContentType: "application/json"},
//		)
//		page.SetCSP("script-src 'self' 'unsafe-inline'") // Instagram-like: no unsafe-eval
//		if err := page.Navigate("https://www.instagram.com/p/ABC123/"); err != nil {
//			t.Fatal(err)
//		}
//		res, err := bottest.CallMethod(t, New(), page, "like_post", "https://www.instagram.com/p/ABC123/")
//		// ... assert on res/err, then on the WRITE the bot made:
//		if _, ok := rec.Wait("POST", "*/likes/*", 5*time.Second); !ok {
//			t.Fatalf("no like request; saw %v", rec.Requests())
//		}
//	}
//
// Run with a Chromium-family binary:
//
//	BOTTEST_BROWSER=/usr/bin/chromium go test -tags social ./internal/bot/instagram/
//
// ($JEV_E2E_BROWSER is accepted too.) Without either, Launch calls t.Skip,
// so plain `go test ./...` stays browser-free.
//
// # Fixtures
//
// Put fixtures under internal/bot/<platform>/testdata/*.html (Route.File is
// read relative to the test's working directory, i.e. the package dir). They
// must be SYNTHETIC: hand-written markup that mimics the structure,
// selectors, and aria labels the bot relies on, with invented names and
// content. Never commit captured pages, cookies, tokens, or anyone's private
// data. Keep scripts inline or served by another Route; any request that no
// Route matches fails with net::ERR_BLOCKED_BY_CLIENT and is recorded with
// Blocked=true, which makes missing fixtures easy to spot.
//
// # Fidelity notes
//
// Page mirrors ExtensionPage/ExtensionElement (chrome-extension/content.js
// and background.js): Element/ElementX poll every 200ms until their timeout,
// Elements polls up to 5s for a non-empty result, Click is scrollIntoView +
// synthetic mousedown/mouseup + el.click(), Input focuses, clears, then types
// per character with InputEvents (contenteditable: paste → execCommand →
// textContent), Text is trimmed textContent, Attribute is nil when missing,
// HTML is innerHTML, Eval swallows errors and returns a nil result (use
// Strict() to see them), EvalCDP bypasses CSP, and TypeCDP focuses the first
// visible contenteditable. Two deliberate differences, because the extension
// is buggy there: Has reports presence correctly (ExtensionPage.Has reads a
// "found" key the content script never sends, so it is always false), and
// WaitStable waits for the element's box to stop moving (the extension's
// wait_element needs a selector and always fails for a handle). Element ids
// are unique per document, so a handle from a previous page fails loudly
// instead of aliasing a new element.
package bottest
