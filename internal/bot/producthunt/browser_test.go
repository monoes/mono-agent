//go:build !nosocial

package producthunt

// Real-browser tests (bottest) against synthetic launch pages in testdata/,
// served at real www.producthunt.com URLs under a CSP without
// 'unsafe-eval'.
//
//	BOTTEST_BROWSER=/usr/bin/chromium go test -count=1 ./internal/bot/producthunt/

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/rs/zerolog"

	"github.com/monoes/mono-agent/internal/action"
	"github.com/monoes/mono-agent/internal/bot/bottest"
	"github.com/monoes/mono-agent/internal/jev"
	"github.com/monoes/mono-agent/internal/jev/jevtest"
)

const (
	phCSP     = "default-src 'self'; script-src 'self' 'unsafe-inline'; style-src 'self' 'unsafe-inline'; img-src 'self' data:"
	launchURL = "https://www.producthunt.com/posts/synthetic-gadget"
)

func fastWaits(t *testing.T) {
	t.Helper()
	oldW, oldC := controlWait, confirmTimeout
	controlWait, confirmTimeout = 2*time.Second, 2*time.Second
	t.Cleanup(func() { controlWait, confirmTimeout = oldW, oldC })
}

func phPage(t *testing.T, fixture string, extra ...bottest.Route) *bottest.Page {
	t.Helper()
	fastWaits(t)
	b := bottest.Launch(t)
	p := b.NewPage(t)
	routes := append(extra, bottest.Route{Pattern: launchURL, File: "testdata/" + fixture})
	p.Serve(routes...)
	p.SetCSP(phCSP)
	return p
}

// feedIDs lists the comment ids now in the page.
func feedIDs(t *testing.T, p *bottest.Page) []string {
	t.Helper()
	cs, err := readComments(p)
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, c := range cs {
		ids = append(ids, c.ID)
	}
	return ids
}

func TestBrowserListComments(t *testing.T) {
	p := phPage(t, "launch.html")
	res, err := bottest.CallMethod(t, &ProductHuntBot{}, p, "list_comments", launchURL)
	if err != nil {
		t.Fatal(err)
	}
	type row struct {
		id, author, username, text, parent string
		depth, upvotes                     int
	}
	var got []row
	for _, c := range res.([]map[string]interface{}) {
		got = append(got, row{c["id"].(string), c["author"].(string), c["username"].(string), c["text"].(string), c["parentId"].(string), c["depth"].(int), c["upvotes"].(int)})
	}
	want := []row{
		{"900001", "Pat Example", "pat_example", "Congrats on shipping!\nLooks neat.", "", 0, 3},
		{"900002", "Sam Maker", "sam_maker", "Thanks Pat!", "900001", 1, 1},
		{"900003", "Pat Example", "pat_example", "Anytime.", "900002", 2, 0},
		{"900010", "Lee Example", "lee_example", "How does it compare to the other gadget?", "", 0, 1200},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("comments:\n got %+v\nwant %+v", got, want)
	}
}

// maxComments keeps the first N comments in page order; 0 keeps them all.
func TestBrowserListCommentsMaxComments(t *testing.T) {
	p := phPage(t, "launch.html")
	for _, c := range []struct {
		max  interface{}
		want []string
	}{
		{"2", []string{"900001", "900002"}},
		{2.0, []string{"900001", "900002"}},
		{"0", []string{"900001", "900002", "900003", "900010"}},
		{"10", []string{"900001", "900002", "900003", "900010"}},
	} {
		res, err := bottest.CallMethod(t, &ProductHuntBot{}, p, "list_comments", launchURL, c.max)
		if err != nil {
			t.Fatalf("max %v: %v", c.max, err)
		}
		var got []string
		for _, m := range res.([]map[string]interface{}) {
			got = append(got, m["id"].(string))
		}
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("max %v: ids %v, want %v", c.max, got, c.want)
		}
	}
	if _, err := bottest.CallMethod(t, &ProductHuntBot{}, p, "list_comments", launchURL, "-1"); err == nil {
		t.Error("maxComments -1: want an error")
	}
}

// TestBrowserListCommentsThreadedReplies uses the real 2026 layout: every
// comment (reply or not) sits in its own thread-<id> container, and a
// reply's thread is nested inside its parent's thread rather than inside
// the parent comment element. Replies must come back at depth 1 under the
// enclosing thread, not at depth 0 because closest() found their own thread.
func TestBrowserListCommentsThreadedReplies(t *testing.T) {
	p := phPage(t, "launch_threads.html")
	res, err := bottest.CallMethod(t, &ProductHuntBot{}, p, "list_comments", launchURL)
	if err != nil {
		t.Fatal(err)
	}
	type row struct {
		id, author, username, text, parent string
		depth, upvotes                     int
	}
	var got []row
	for _, c := range res.([]map[string]interface{}) {
		got = append(got, row{c["id"].(string), c["author"].(string), c["username"].(string), c["text"].(string), c["parentId"].(string), c["depth"].(int), c["upvotes"].(int)})
	}
	want := []row{
		{"810001", "Sam Maker", "sam_maker", "Hello testers! We built a synthetic widget.\n\nAsk us anything.", "", 0, 5},
		{"810005", "Pat Example", "pat_example", "Shoutout to @sam_maker for this.", "810001", 1, 2},
		{"810007", "Sam Maker", "sam_maker", "Thanks Pat! @pat_example", "810001", 1, 1},
		{"810020", "Lee Example", "lee_example", "Does it work offline?", "", 0, 0},
		{"810030", "Kim Example", "kim_example", "Pricing?", "", 0, 1},
		{"810031", "Sam Maker", "sam_maker", "Free for now.", "810030", 1, 0},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("comments:\n got %+v\nwant %+v", got, want)
	}
}

func TestBrowserLaunchMetrics(t *testing.T) {
	p := phPage(t, "launch.html")
	res, err := bottest.CallMethod(t, &ProductHuntBot{}, p, "get_launch_metrics", launchURL)
	if err != nil {
		t.Fatal(err)
	}
	m := res.(map[string]interface{})
	// The launch's own button — not the "93" or the "Upvote 555" of the
	// similar launches in the sidebar.
	if m["upvotes"] != 1234 || m["upvotesHidden"] != false || m["comments"] != 4 || m["commentsSource"] != "loaded" || m["name"] != "Synthetic Gadget" {
		t.Fatalf("metrics = %v", m)
	}
}

func TestBrowserLaunchMetricsHiddenCount(t *testing.T) {
	p := phPage(t, "launch_hidden_votes.html")
	res, err := bottest.CallMethod(t, &ProductHuntBot{}, p, "get_launch_metrics", launchURL)
	if err != nil {
		t.Fatal(err)
	}
	if m := res.(map[string]interface{}); m["upvotes"] != nil || m["upvotesHidden"] != true {
		t.Fatalf("metrics = %v", m)
	}
}

func TestBrowserLaunchMetricsAmbiguousIsAnError(t *testing.T) {
	fastWaits(t)
	b := bottest.Launch(t)
	p := b.NewPage(t)
	p.Serve(bottest.Route{Pattern: launchURL, Body: `<!doctype html><h1>X</h1>
<div><button data-test="vote-button">Upvote 10</button></div><div><button data-test="vote-button">Upvote 20</button></div>`})
	p.SetCSP(phCSP)
	_, err := bottest.CallMethod(t, &ProductHuntBot{}, p, "get_launch_metrics", launchURL)
	if err == nil || !strings.Contains(err.Error(), "cannot tell") {
		t.Fatalf("err = %v", err)
	}
}

func TestBrowserRefusesForeignURL(t *testing.T) {
	p := phPage(t, "launch.html")
	for _, m := range []string{"get_launch_metrics", "list_comments"} {
		if _, err := bottest.CallMethod(t, &ProductHuntBot{}, p, m, "https://evil.example/posts/x"); err == nil {
			t.Fatalf("%s accepted a non-Product-Hunt URL", m)
		}
	}
}

const commentText = "Nice work on the synthetic gadget — how do you test it?"

func TestBrowserCommentConfirmed(t *testing.T) {
	p := phPage(t, "launch.html")
	res, err := bottest.CallMethod(t, &ProductHuntBot{}, p, "comment_on_launch", launchURL, commentText)
	if err != nil {
		t.Fatal(err)
	}
	m := res.(map[string]interface{})
	if m["success"] != true || m["commentID"] != "900100" {
		t.Fatalf("result = %v", m)
	}
	ids := feedIDs(t, p)
	if strings.Join(ids, ",") != "900100,900001,900002,900003,900010" {
		t.Fatalf("feed = %v (want exactly one new comment)", ids)
	}
}

func TestBrowserCommentNotConfirmed(t *testing.T) {
	p := phPage(t, "launch_broken.html")
	_, err := bottest.CallMethod(t, &ProductHuntBot{}, p, "comment_on_launch", launchURL, commentText)
	if err == nil || !strings.Contains(err.Error(), "not confirmed") {
		t.Fatalf("err = %v", err)
	}
}

func TestBrowserCommentLoggedOut(t *testing.T) {
	p := phPage(t, "launch_loggedout.html", bottest.Route{Pattern: "https://www.producthunt.com/login", Body: "<!doctype html><p>login</p>"})
	_, err := bottest.CallMethod(t, &ProductHuntBot{}, p, "comment_on_launch", launchURL, commentText)
	if err == nil || !strings.Contains(err.Error(), "not logged in") {
		t.Fatalf("err = %v", err)
	}
	if u, _ := p.GetURL(); u != launchURL {
		t.Fatalf("the login button was clicked: now on %s", u)
	}
}

// jevChoose answers every pick with the first offered element whose label
// and role satisfy match, or NONE.
func jevChoose(match func(label, role string) bool) jevtest.Handler {
	return func(req jev.Request) map[string]string {
		st, _ := req.State.(map[string]interface{})
		els, _ := st["elements"].([]interface{})
		for _, e := range els {
			m, _ := e.(map[string]interface{})
			label, _ := m["label"].(string)
			role, _ := m["role"].(string)
			if match(label, role) {
				idx, _ := m["index"].(string)
				return map[string]string{"target": idx}
			}
		}
		return map[string]string{"target": "NONE"}
	}
}

func jevBot(t *testing.T, h jevtest.Handler) (*ProductHuntBot, *jevtest.Server) {
	t.Helper()
	srv := jevtest.NewServer(t, h)
	c, err := jev.NewClient("test-key", "")
	if err != nil {
		t.Fatal(err)
	}
	b := &ProductHuntBot{}
	b.SetJevPicker(c, 0)
	return b, srv
}

// The composer lost its data-test hooks: Jev finds the comment box and the
// "Comment" button (not the "Comments" tab).
func TestBrowserCommentJevFallback(t *testing.T) {
	b, srv := jevBot(t, jevChoose(func(label, role string) bool {
		return label == "Write a comment" || (label == "Comment" && role == "button")
	}))
	p := phPage(t, "launch_jev.html")
	res, err := bottest.CallMethod(t, b, p, "comment_on_launch", launchURL, commentText)
	if err != nil {
		t.Fatal(err)
	}
	if res.(map[string]interface{})["commentID"] != "900100" {
		t.Fatalf("result = %v", res)
	}
	if srv.Calls() != 2 {
		t.Fatalf("jev calls = %d, want 2", srv.Calls())
	}
}

func TestBrowserCommentJevNoneFails(t *testing.T) {
	b, _ := jevBot(t, jevChoose(func(string, string) bool { return false }))
	p := phPage(t, "launch_jev.html")
	_, err := bottest.CallMethod(t, b, p, "comment_on_launch", launchURL, commentText)
	if err == nil || !strings.Contains(err.Error(), "comment box not found") {
		t.Fatalf("err = %v", err)
	}
	if ids := feedIDs(t, p); len(ids) != 4 {
		t.Fatalf("feed changed: %v", ids)
	}
}

// JSON-flow tests: data/actions/producthunt/*.json through the executor.

func runAction(t *testing.T, p *bottest.Page, typ string, params map[string]interface{}) (*action.ExecutionResult, error) {
	t.Helper()
	ae := action.NewActionExecutor(context.Background(), p, nil, nil, nil, &ProductHuntBot{}, zerolog.Nop())
	return ae.Execute(&action.StorageAction{ID: "flow-" + typ, Type: typ, TargetPlatform: "PRODUCTHUNT", Params: params})
}

func TestFlowProductHunt(t *testing.T) {
	p := phPage(t, "launch.html")
	res, err := runAction(t, p, "list_comments", map[string]interface{}{"launchURL": launchURL})
	if err != nil || len(res.ExtractedItems) != 4 {
		t.Fatalf("list_comments: err=%v items=%v", err, res)
	}
	res, err = runAction(t, p, "get_launch_metrics", map[string]interface{}{"launchURL": launchURL})
	if err != nil || len(res.ExtractedItems) != 1 || res.ExtractedItems[0]["upvotes"] != 1234 {
		t.Fatalf("get_launch_metrics: err=%v res=%v", err, res)
	}
	res, err = runAction(t, p, "comment_on_launch", map[string]interface{}{"launchURL": launchURL, "text": commentText})
	if err != nil || len(res.ExtractedItems) != 1 || res.ExtractedItems[0]["commentID"] != "900100" {
		t.Fatalf("comment_on_launch: err=%v res=%v", err, res)
	}

	broken := phPage(t, "launch_broken.html")
	if _, err := runAction(t, broken, "comment_on_launch", map[string]interface{}{"launchURL": launchURL, "text": commentText}); err == nil || !strings.Contains(err.Error(), "not confirmed") {
		t.Fatalf("broken comment_on_launch: err=%v", err)
	}
}
