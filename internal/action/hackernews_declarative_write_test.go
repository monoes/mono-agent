package action_test

// submit_post and reply_to_comment, declaratively, against the same
// scenarios the Go bot is tested with (internal/bot/hackernews
// browser_test.go): confirmed, duplicate, rejected, rate-limited, logged out,
// and a read-back that fails without a second POST.

import (
	"net/url"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/monoes/mono-agent/internal/bot/bottest"
)

// submitRoutes serves /submit and a /submitted list that gains the new
// story once the form was POSTed; postResp answers the POST.
func submitRoutes(postResp bottest.Route) ([]bottest.Route, *int32) {
	var posted int32
	inner := postResp
	post := bottest.Route{Pattern: "https://news.ycombinator.com/r", Method: "POST", Handler: func(r bottest.Request) bottest.Response {
		atomic.AddInt32(&posted, 1)
		resp := bottest.Response{Status: inner.Status, Headers: inner.Headers, ContentType: inner.ContentType, Body: inner.Body}
		if inner.File != "" {
			resp.Body = fxBody(inner.File)
		}
		return resp
	}}
	return []bottest.Route{
		fx("https://news.ycombinator.com/submit", "submit.html"),
		post,
		{Pattern: "https://news.ycombinator.com/submitted?id=test_user", Handler: func(bottest.Request) bottest.Response {
			f := "submitted_before.html"
			if atomic.LoadInt32(&posted) > 0 {
				f = "submit_post.html"
			}
			return bottest.Response{Body: fxBody(f), ContentType: "text/html; charset=utf-8"}
		}},
		fx("https://news.ycombinator.com/newest", "newest.html"),
		fx("https://news.ycombinator.com/item?id=70000100", "get_post_metrics.html"),
	}, &posted
}

func redirectTo(location string) bottest.Route {
	return bottest.Route{Status: 302, Headers: map[string]string{"Location": location}}
}

func TestHackerNewsSubmitConfirmed(t *testing.T) {
	routes, posted := submitRoutes(redirectTo("https://news.ycombinator.com/newest"))
	p, rec := hnBrowserPage(t, routes...)
	got, err := runHN(t, p, "submit_post", map[string]interface{}{"title": "Show HN: Synthetic Gadget", "url": "https://example.test/gadget"})
	if err != nil {
		t.Fatal(err)
	}
	// Not /newest's first row (someone else's), and not the older story with
	// the same title that was already on /submitted.
	jsonEq(t, got, expectFile(t, "submit_post"))
	if *posted != 1 {
		t.Fatalf("POSTs = %d", *posted)
	}
	form, _ := url.ParseQuery(rec.Matching("POST", "*/r")[0].PostData)
	if form.Get("title") != "Show HN: Synthetic Gadget" || form.Get("url") != "https://example.test/gadget" || form.Get("text") != "" {
		t.Fatalf("posted form = %v", form)
	}
}

// The title is compared the way the bot compared it: case, runs of
// whitespace and *asterisks* do not matter.
func TestHackerNewsSubmitMatchesNormalisedTitle(t *testing.T) {
	routes, _ := submitRoutes(redirectTo("https://news.ycombinator.com/newest"))
	p, _ := hnBrowserPage(t, routes...)
	got, err := runHN(t, p, "submit_post", map[string]interface{}{"title": "  show HN:   *Synthetic*  gadget "})
	if err != nil {
		t.Fatal(err)
	}
	jsonEq(t, got, expectFile(t, "submit_post"))
}

func TestHackerNewsSubmitDuplicateRedirectsToExistingItem(t *testing.T) {
	routes, _ := submitRoutes(redirectTo("https://news.ycombinator.com/item?id=70000100"))
	p, _ := hnBrowserPage(t, routes...)
	_, err := runHN(t, p, "submit_post", map[string]interface{}{"title": "Show HN: Synthetic Gadget", "url": "https://example.test/gadget-v1"})
	// The fixture served for item 70000100 is item 70000001's page.
	wantErr(t, err, "already submitted", "existing item 70000001")
}

func TestHackerNewsSubmitRejectedAndRateLimited(t *testing.T) {
	routes, _ := submitRoutes(bottest.Route{File: "submit_rejected.html", ContentType: "text/html"})
	p, _ := hnBrowserPage(t, routes...)
	_, err := runHN(t, p, "submit_post", map[string]interface{}{"title": "Show HN: Synthetic Gadget", "text": "some text"})
	wantErr(t, err, "rejected", "That title is too long.")

	routes, _ = submitRoutes(bottest.Route{File: "too_fast.html", ContentType: "text/html"})
	p, _ = hnBrowserPage(t, routes...)
	_, err = runHN(t, p, "submit_post", map[string]interface{}{"title": "Show HN: Synthetic Gadget", "url": "https://example.test/gadget"})
	wantErr(t, err, "refused the submission: You're posting too fast.")
}

func TestHackerNewsSubmitReadBackFailsWithoutResubmitting(t *testing.T) {
	// HN accepts the POST, but the story never shows on /submitted.
	var posted int32
	p, _ := hnBrowserPage(t,
		fx("https://news.ycombinator.com/submit", "submit.html"),
		bottest.Route{Pattern: "https://news.ycombinator.com/r", Method: "POST", Handler: func(bottest.Request) bottest.Response {
			atomic.AddInt32(&posted, 1)
			return bottest.Response{Status: 302, Headers: map[string]string{"Location": "https://news.ycombinator.com/newest"}}
		}},
		fx("https://news.ycombinator.com/submitted?id=test_user", "submitted_before.html"),
		fx("https://news.ycombinator.com/newest", "newest.html"),
	)
	_, err := runHN(t, p, "submit_post", map[string]interface{}{"title": "Show HN: Brand new thing", "url": "https://example.test/new"})
	wantErr(t, err, "not confirmed", "not resubmitting", "submitted?id=test_user")
	if posted != 1 {
		t.Fatalf("POSTs = %d, want exactly 1", posted)
	}
}

func TestHackerNewsSubmitRefusesBadInputAndLoggedOut(t *testing.T) {
	p, rec := hnBrowserPage(t, fx("https://news.ycombinator.com/submit", "submit_loggedout.html"))
	_, err := runHN(t, p, "submit_post", map[string]interface{}{"title": "Title"})
	wantErr(t, err, "not logged in")
	_, err = runHN(t, p, "submit_post", map[string]interface{}{"title": "Title", "url": "javascript:alert(1)"})
	wantErr(t, err, "absolute http(s) URL")
	if n := len(rec.Matching("POST", "*")); n != 0 {
		t.Fatalf("POSTs = %d", n)
	}

	p, rec = hnBrowserPage(t, fx("https://news.ycombinator.com/submit", "submit_loggedout_bare.html"))
	_, err = runHN(t, p, "submit_post", map[string]interface{}{"title": "Title"})
	assertTrimmedRefusal(t, err, "You have to be logged in to submit.")
	if n := len(rec.Matching("POST", "*")); n != 0 {
		t.Fatalf("POSTs = %d", n)
	}
}

// assertTrimmedRefusal: the error ends with the refusal sentence only, never
// the login form that follows it on the bare page.
func assertTrimmedRefusal(t *testing.T, err error, sentence string) {
	t.Helper()
	if err == nil || !strings.HasSuffix(err.Error(), "refused: "+sentence) {
		t.Fatalf("err = %v, want it to end with the refusal sentence %q only", err, sentence)
	}
	for _, junk := range []string{"username", "password", "Create Account", "Login"} {
		if strings.Contains(err.Error(), junk) {
			t.Fatalf("err quotes the login form (%q): %v", junk, err)
		}
	}
}

// --- reply_to_comment -------------------------------------------------------

// replyRoutes serves the reply form for 70000010 and answers the POST with a
// redirect to afterFile.
func replyRoutes(formFile, afterFile string) []bottest.Route {
	return []bottest.Route{
		fx("https://news.ycombinator.com/reply?*", formFile),
		{Pattern: "https://news.ycombinator.com/comment*", Method: "POST", Status: 302,
			Headers: map[string]string{"Location": "https://news.ycombinator.com/item?id=70000010"}},
		fx("https://news.ycombinator.com/item?id=70000010", afterFile),
	}
}

const replyText = "Thanks for the *synthetic* write-up.\n\nSecond line here."

func TestHackerNewsReplyConfirmed(t *testing.T) {
	p, rec := hnBrowserPage(t, replyRoutes("reply_to_comment.html", "item_after_reply.html")...)
	got, err := runHN(t, p, "reply_to_comment", map[string]interface{}{"itemID": float64(70000010), "text": replyText})
	if err != nil {
		t.Fatal(err)
	}
	jsonEq(t, got, expectFile(t, "reply_to_comment"))
	posts := rec.Matching("POST", "https://news.ycombinator.com/comment*")
	if len(posts) != 1 {
		t.Fatalf("want exactly one POST, got %d", len(posts))
	}
	form, _ := url.ParseQuery(posts[0].PostData)
	if form.Get("parent") != "70000010" || strings.ReplaceAll(form.Get("text"), "\r\n", "\n") != replyText {
		t.Fatalf("posted form = %v", form)
	}
}

func TestHackerNewsReplyNotConfirmed(t *testing.T) {
	// The redirect target holds only an older, different reply by the user.
	p, rec := hnBrowserPage(t, replyRoutes("reply_to_comment.html", "item_after_reply_missing.html")...)
	_, err := runHN(t, p, "reply_to_comment", map[string]interface{}{"itemID": "70000010", "text": replyText})
	wantErr(t, err, "not confirmed")
	if n := len(rec.Matching("POST", "*/comment*")); n != 1 {
		t.Fatalf("POSTs = %d, want 1", n)
	}
}

func TestHackerNewsReplyPostingTooFast(t *testing.T) {
	p, _ := hnBrowserPage(t,
		fx("https://news.ycombinator.com/reply?*", "reply_to_comment.html"),
		bottest.Route{Pattern: "https://news.ycombinator.com/comment*", Method: "POST", Body: fxBody("too_fast.html"), ContentType: "text/html"},
	)
	_, err := runHN(t, p, "reply_to_comment", map[string]interface{}{"itemID": "70000010", "text": replyText})
	wantErr(t, err, "refused the reply: You're posting too fast.")
}

func TestHackerNewsReplyLoggedOut(t *testing.T) {
	p, rec := hnBrowserPage(t, fx("https://news.ycombinator.com/reply?*", "reply_loggedout.html"))
	_, err := runHN(t, p, "reply_to_comment", map[string]interface{}{"itemID": "70000010", "text": replyText})
	wantErr(t, err, "not logged in")
	_, err = runHN(t, p, "reply_to_comment", map[string]interface{}{"itemID": "70000010#x", "text": replyText})
	wantErr(t, err, "item id must be numeric")
	if n := len(rec.Matching("POST", "*")); n != 0 {
		t.Fatalf("logged-out reply must not POST, got %d", n)
	}

	p, rec = hnBrowserPage(t, fx("https://news.ycombinator.com/reply?*", "reply_loggedout_bare.html"))
	_, err = runHN(t, p, "reply_to_comment", map[string]interface{}{"itemID": "70000010", "text": replyText})
	assertTrimmedRefusal(t, err, "You have to be logged in to reply.")
	if n := len(rec.Matching("POST", "*")); n != 0 {
		t.Fatalf("logged-out reply must not POST, got %d", n)
	}
}
