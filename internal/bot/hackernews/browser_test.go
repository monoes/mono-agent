//go:build social

package hackernews

// Real-browser tests (bottest): every action against synthetic fixtures in
// testdata/, served at the real news.ycombinator.com URLs with a CSP that
// forbids 'unsafe-eval' — as Hacker News does — so the extension's plain
// Eval path would fail and only the EvalCDP path can pass.
//
//	BOTTEST_BROWSER=/usr/bin/chromium go test -count=1 -tags social ./internal/bot/hackernews/

import (
	"net/url"
	"os"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/monoes/mono-agent/internal/bot/bottest"
)

const hnCSP = "default-src 'self'; script-src 'self' 'unsafe-inline'; style-src 'self' 'unsafe-inline'; img-src 'self' data:"

func fastConfirm(t *testing.T) {
	t.Helper()
	oldC, oldA, oldD, oldW := confirmTimeout, readBackAttempts, readBackDelay, controlWait
	confirmTimeout, readBackAttempts, readBackDelay, controlWait = 1500*time.Millisecond, 2, 100*time.Millisecond, 1500*time.Millisecond
	t.Cleanup(func() { confirmTimeout, readBackAttempts, readBackDelay, controlWait = oldC, oldA, oldD, oldW })
}

func hnPage(t *testing.T, routes ...bottest.Route) (*bottest.Page, *bottest.Recorder) {
	t.Helper()
	fastConfirm(t)
	b := bottest.Launch(t)
	p := b.NewPage(t)
	routes = append(routes, bottest.Route{Pattern: "https://news.ycombinator.com/s.gif", Body: "", ContentType: "image/gif"})
	rec := p.Serve(routes...)
	p.SetCSP(hnCSP)
	return p, rec
}

func file(pattern, f string) bottest.Route {
	return bottest.Route{Pattern: pattern, File: "testdata/" + f}
}

func redirect(pattern, method, location string) bottest.Route {
	return bottest.Route{Pattern: pattern, Method: method, Status: 302, Headers: map[string]string{"Location": location}}
}

func TestBrowserCSPBlocksPlainEvalButMetricsWork(t *testing.T) {
	p, _ := hnPage(t, file("https://news.ycombinator.com/item?id=70000001", "item.html"))
	if err := p.Navigate("https://news.ycombinator.com/item?id=70000001"); err != nil {
		t.Fatal(err)
	}
	// The premise: this CSP blocks the extension-style Eval.
	if res, err := p.Eval(`() => 1 + 1`); err == nil && res != nil && res.Raw() != nil {
		t.Fatalf("fixture CSP should block plain Eval, got %v", res.Raw())
	}
	// Numeric id, as a workflow JSON number would deliver it.
	res, err := bottest.CallMethod(t, &HackerNewsBot{}, p, "get_post_metrics", float64(70000001))
	if err != nil {
		t.Fatal(err)
	}
	m := res.(map[string]interface{})
	if m["points"] != 42 || m["comments"] != 7 || m["title"] != "Show HN: A tiny synthetic widget" || m["author"] != "alice_example" || m["isJob"] != false {
		t.Fatalf("metrics = %v", m)
	}
}

func TestBrowserMetricsJobPostAndDiscuss(t *testing.T) {
	p, _ := hnPage(t,
		file("https://news.ycombinator.com/item?id=70000050", "job.html"),
		file("https://news.ycombinator.com/item?id=70000060", "discuss.html"),
		bottest.Route{Pattern: "https://news.ycombinator.com/item?id=70000077", Body: "No such item."},
	)
	b := &HackerNewsBot{}
	res, err := bottest.CallMethod(t, b, p, "get_post_metrics", "70000050")
	if err != nil {
		t.Fatal(err)
	}
	m := res.(map[string]interface{})
	if m["points"] != nil || m["comments"] != nil || m["isJob"] != true {
		t.Fatalf("job metrics = %v", m)
	}
	res, err = bottest.CallMethod(t, b, p, "get_post_metrics", "70000060")
	if err != nil {
		t.Fatal(err)
	}
	if m := res.(map[string]interface{}); m["points"] != 1 || m["comments"] != 0 {
		t.Fatalf("discuss metrics = %v", m)
	}
	if _, err := bottest.CallMethod(t, b, p, "get_post_metrics", "70000077"); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("missing item: err = %v", err)
	}
	if _, err := bottest.CallMethod(t, b, p, "get_post_metrics", "1 OR 1"); err == nil {
		t.Fatal("non-numeric id must be refused")
	}
}

func TestBrowserListCommentsAllPagesWithDepth(t *testing.T) {
	p, _ := hnPage(t,
		file("https://news.ycombinator.com/item?id=70000001&p=2", "item_p2.html"),
		file("https://news.ycombinator.com/item?id=70000001", "item.html"),
	)
	res, err := bottest.CallMethod(t, &HackerNewsBot{}, p, "list_comments", float64(70000001))
	if err != nil {
		t.Fatal(err)
	}
	got := res.([]map[string]interface{})
	type row struct {
		id, author, parent string
		depth              int
		deleted            bool
	}
	var rows []row
	for _, c := range got {
		rows = append(rows, row{c["id"].(string), c["author"].(string), c["parentId"].(string), c["depth"].(int), c["deleted"].(bool)})
	}
	want := []row{
		{"70000010", "bob_example", "70000001", 0, false},
		{"70000011", "carol_example", "70000010", 1, false},
		{"70000012", "dave_example", "70000011", 2, false},
		{"70000013", "", "70000010", 1, true},
		{"70000014", "erin_example", "70000001", 0, false},
		{"70000020", "frank_example", "70000014", 1, false}, // page 2 continues erin's thread
		{"70000021", "grace_example", "70000001", 0, false},
	}
	if !reflect.DeepEqual(rows, want) {
		t.Fatalf("comments:\n got %v\nwant %v", rows, want)
	}
	if txt := got[0]["text"].(string); txt != "First synthetic paragraph about the widget.\n\nSecond paragraph with a link." {
		t.Fatalf("paragraph text = %q", txt)
	}
	if strings.HasSuffix(got[1]["text"].(string), "reply") {
		t.Fatalf("reply link leaked into text: %q", got[1]["text"])
	}

	res, err = bottest.CallMethod(t, &HackerNewsBot{}, p, "list_comments", "70000001", true)
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, c := range res.([]map[string]interface{}) {
		ids = append(ids, c["id"].(string))
	}
	if strings.Join(ids, ",") != "70000010,70000014,70000021" {
		t.Fatalf("topLevelOnly ids = %v", ids)
	}
}

// replyRoutes serves the reply form for 70000010 and answers the POST with a
// redirect to afterFile's URL.
func replyRoutes(formFile, afterFile string) []bottest.Route {
	return []bottest.Route{
		file("https://news.ycombinator.com/reply?*", formFile),
		redirect("https://news.ycombinator.com/comment*", "POST", "https://news.ycombinator.com/item?id=70000010"),
		file("https://news.ycombinator.com/item?id=70000010", afterFile),
	}
}

const replyText = "Thanks for the *synthetic* write-up.\n\nSecond line here."

func TestBrowserReplyConfirmed(t *testing.T) {
	p, rec := hnPage(t, replyRoutes("reply.html", "item_after_reply.html")...)
	res, err := bottest.CallMethod(t, &HackerNewsBot{}, p, "reply_to_comment", float64(70000010), replyText)
	if err != nil {
		t.Fatal(err)
	}
	m := res.(map[string]interface{})
	if m["success"] != true || m["commentID"] != "70000099" || m["itemID"] != "70000010" {
		t.Fatalf("result = %v", m)
	}
	posts := rec.Matching("POST", "https://news.ycombinator.com/comment*")
	if len(posts) != 1 {
		t.Fatalf("want exactly one POST, got %d", len(posts))
	}
	form, _ := url.ParseQuery(posts[0].PostData)
	if form.Get("parent") != "70000010" || strings.ReplaceAll(form.Get("text"), "\r\n", "\n") != replyText {
		t.Fatalf("posted form = %v", form)
	}
}

func TestBrowserReplyNotConfirmed(t *testing.T) {
	// The redirect target holds only an older, different reply by the user.
	p, rec := hnPage(t, replyRoutes("reply.html", "item_after_reply_missing.html")...)
	_, err := bottest.CallMethod(t, &HackerNewsBot{}, p, "reply_to_comment", "70000010", replyText)
	if err == nil || !strings.Contains(err.Error(), "not confirmed") {
		t.Fatalf("err = %v", err)
	}
	if n := len(rec.Matching("POST", "*/comment*")); n != 1 {
		t.Fatalf("POSTs = %d, want 1", n)
	}
}

func TestBrowserReplyPostingTooFast(t *testing.T) {
	p, _ := hnPage(t,
		file("https://news.ycombinator.com/reply?*", "reply.html"),
		bottest.Route{Pattern: "https://news.ycombinator.com/comment*", Method: "POST", File: "testdata/too_fast.html", ContentType: "text/html"},
	)
	_, err := bottest.CallMethod(t, &HackerNewsBot{}, p, "reply_to_comment", "70000010", replyText)
	if err == nil || !strings.Contains(err.Error(), "posting too fast") {
		t.Fatalf("err = %v", err)
	}
}

func TestBrowserReplyLoggedOut(t *testing.T) {
	p, rec := hnPage(t, file("https://news.ycombinator.com/reply?*", "reply_loggedout.html"))
	_, err := bottest.CallMethod(t, &HackerNewsBot{}, p, "reply_to_comment", "70000010", replyText)
	if err == nil || !strings.Contains(err.Error(), "not logged in") {
		t.Fatalf("err = %v", err)
	}
	if n := len(rec.Matching("POST", "*")); n != 0 {
		t.Fatalf("logged-out reply must not POST, got %d", n)
	}
}

// TestBrowserReplyLoggedOutBarePage uses HN's real logged-out reply page: a
// bare page (no #hnmain) with the refusal line followed by the login and
// create-account forms. The error quotes only the refusal sentence.
func TestBrowserReplyLoggedOutBarePage(t *testing.T) {
	p, rec := hnPage(t, file("https://news.ycombinator.com/reply?*", "reply_loggedout_bare.html"))
	_, err := bottest.CallMethod(t, &HackerNewsBot{}, p, "reply_to_comment", "70000010", replyText)
	assertTrimmedRefusal(t, err, "You have to be logged in to reply.")
	if n := len(rec.Matching("POST", "*")); n != 0 {
		t.Fatalf("logged-out reply must not POST, got %d", n)
	}
}

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

// submitRoutes serves /submit and a /submitted list that gains the new
// story once the form was POSTed; postResp answers the POST.
func submitRoutes(postResp bottest.Route) ([]bottest.Route, *int32) {
	var posted int32
	postResp.Pattern, postResp.Method = "https://news.ycombinator.com/r", "POST"
	inner := postResp
	postResp.Handler = func(r bottest.Request) bottest.Response {
		atomic.AddInt32(&posted, 1)
		if inner.Handler != nil {
			return inner.Handler(r)
		}
		resp := bottest.Response{Status: inner.Status, Headers: inner.Headers, ContentType: inner.ContentType, Body: inner.Body}
		if inner.File != "" {
			resp.Body = mustRead(inner.File)
		}
		return resp
	}
	return []bottest.Route{
		file("https://news.ycombinator.com/submit", "submit.html"),
		postResp,
		{Pattern: "https://news.ycombinator.com/submitted?id=test_user", Handler: func(bottest.Request) bottest.Response {
			f := "testdata/submitted_before.html"
			if atomic.LoadInt32(&posted) > 0 {
				f = "testdata/submitted_after.html"
			}
			return bottest.Response{Body: mustRead(f), ContentType: "text/html; charset=utf-8"}
		}},
		file("https://news.ycombinator.com/newest", "newest.html"),
		file("https://news.ycombinator.com/item?id=70000100", "item.html"),
	}, &posted
}

func TestBrowserSubmitConfirmedFromSubmittedPage(t *testing.T) {
	routes, posted := submitRoutes(bottest.Route{Status: 302, Headers: map[string]string{"Location": "https://news.ycombinator.com/newest"}})
	p, rec := hnPage(t, routes...)
	res, err := bottest.CallMethod(t, &HackerNewsBot{}, p, "submit_post", "Show HN: Synthetic Gadget", "https://example.test/gadget", "")
	if err != nil {
		t.Fatal(err)
	}
	m := res.(map[string]interface{})
	// Not /newest's first row (someone else's), and not the older story
	// with the same title that was already on /submitted.
	if m["id"] != "70000200" || m["url"] != "https://news.ycombinator.com/item?id=70000200" || m["author"] != "test_user" {
		t.Fatalf("result = %v", m)
	}
	if *posted != 1 {
		t.Fatalf("POSTs = %d", *posted)
	}
	form, _ := url.ParseQuery(rec.Matching("POST", "*/r")[0].PostData)
	if form.Get("title") != "Show HN: Synthetic Gadget" || form.Get("url") != "https://example.test/gadget" {
		t.Fatalf("posted form = %v", form)
	}
}

func TestBrowserSubmitDuplicateRedirectsToExistingItem(t *testing.T) {
	routes, _ := submitRoutes(bottest.Route{Status: 302, Headers: map[string]string{"Location": "https://news.ycombinator.com/item?id=70000100"}})
	p, _ := hnPage(t, routes...)
	_, err := bottest.CallMethod(t, &HackerNewsBot{}, p, "submit_post", "Show HN: Synthetic Gadget", "https://example.test/gadget-v1", "")
	if err == nil || !strings.Contains(err.Error(), "already submitted") || !strings.Contains(err.Error(), "70000100") {
		t.Fatalf("err = %v", err)
	}
}

func TestBrowserSubmitRejectedAndRateLimited(t *testing.T) {
	routes, _ := submitRoutes(bottest.Route{File: "testdata/submit_rejected.html", ContentType: "text/html"})
	p, _ := hnPage(t, routes...)
	_, err := bottest.CallMethod(t, &HackerNewsBot{}, p, "submit_post", "Show HN: Synthetic Gadget", "", "some text")
	if err == nil || !strings.Contains(err.Error(), "rejected") || !strings.Contains(err.Error(), "too long") {
		t.Fatalf("rejected: err = %v", err)
	}

	routes, _ = submitRoutes(bottest.Route{File: "testdata/too_fast.html", ContentType: "text/html"})
	p, _ = hnPage(t, routes...)
	_, err = bottest.CallMethod(t, &HackerNewsBot{}, p, "submit_post", "Show HN: Synthetic Gadget", "https://example.test/gadget", "")
	if err == nil || !strings.Contains(err.Error(), "posting too fast") {
		t.Fatalf("too fast: err = %v", err)
	}
}

func TestBrowserSubmitReadBackFailsWithoutResubmitting(t *testing.T) {
	// HN accepts the POST, but the story never shows on /submitted.
	var posted int32
	p, _ := hnPage(t,
		file("https://news.ycombinator.com/submit", "submit.html"),
		bottest.Route{Pattern: "https://news.ycombinator.com/r", Method: "POST", Handler: func(bottest.Request) bottest.Response {
			atomic.AddInt32(&posted, 1)
			return bottest.Response{Status: 302, Headers: map[string]string{"Location": "https://news.ycombinator.com/newest"}}
		}},
		file("https://news.ycombinator.com/submitted?id=test_user", "submitted_before.html"),
		file("https://news.ycombinator.com/newest", "newest.html"),
	)
	_, err := bottest.CallMethod(t, &HackerNewsBot{}, p, "submit_post", "Show HN: Brand new thing", "https://example.test/new", "")
	if err == nil || !strings.Contains(err.Error(), "not resubmitting") {
		t.Fatalf("err = %v", err)
	}
	if posted != 1 {
		t.Fatalf("POSTs = %d, want exactly 1", posted)
	}
}

func TestBrowserSubmitLoggedOut(t *testing.T) {
	p, rec := hnPage(t, file("https://news.ycombinator.com/submit", "submit_loggedout.html"))
	_, err := bottest.CallMethod(t, &HackerNewsBot{}, p, "submit_post", "Title", "", "")
	if err == nil || !strings.Contains(err.Error(), "not logged in") {
		t.Fatalf("err = %v", err)
	}
	if n := len(rec.Matching("POST", "*")); n != 0 {
		t.Fatalf("POSTs = %d", n)
	}
}

func TestBrowserSubmitLoggedOutBarePage(t *testing.T) {
	p, rec := hnPage(t, file("https://news.ycombinator.com/submit", "submit_loggedout_bare.html"))
	_, err := bottest.CallMethod(t, &HackerNewsBot{}, p, "submit_post", "Title", "", "")
	assertTrimmedRefusal(t, err, "You have to be logged in to submit.")
	if n := len(rec.Matching("POST", "*")); n != 0 {
		t.Fatalf("POSTs = %d", n)
	}
}

func mustRead(f string) string {
	b, err := os.ReadFile(f)
	if err != nil {
		panic(err)
	}
	return string(b)
}
