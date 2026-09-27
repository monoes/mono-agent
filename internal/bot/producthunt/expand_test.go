//go:build !nosocial

package producthunt

// Comment expansion against testdata/launch_paged.html: comments load in
// pages behind "Show more comments", replies behind "View 3 more replies"
// and "Show 2 replies"; the page records every click in window.__clicks.
//
//	BOTTEST_BROWSER=/usr/bin/chromium go test -count=1 ./internal/bot/producthunt/

import (
	"reflect"
	"testing"
	"time"

	botpkg "github.com/monoes/mono-agent/internal/bot"
	"github.com/monoes/mono-agent/internal/bot/bottest"
)

func fastExpand(t *testing.T) {
	t.Helper()
	oldC, oldB, oldS := expandClicks, expandBudget, expandSettle
	expandSettle = time.Second
	t.Cleanup(func() { expandClicks, expandBudget, expandSettle = oldC, oldB, oldS })
}

type pageClick struct {
	Label   string `json:"label"`
	Trusted bool   `json:"trusted"`
}

func pageClicks(t *testing.T, p *bottest.Page) []pageClick {
	t.Helper()
	var cs []pageClick
	if err := botpkg.EvalJSON(p, `() => window.__clicks || []`, &cs); err != nil {
		t.Fatal(err)
	}
	return cs
}

// clickLabels checks every click was a trusted click on an expansion
// control (never Upvote, Reply, Follow, Log in, Hide replies, a comment's
// own "Show more", or anything outside the comments) and returns the labels.
func clickLabels(t *testing.T, p *bottest.Page) []string {
	t.Helper()
	allowed := map[string]bool{"View 3 more replies": true, "Show 2 replies": true, "Show more comments": true, "Load more": true}
	var out []string
	for _, c := range pageClicks(t, p) {
		if !allowed[c.Label] {
			t.Errorf("clicked a control that is not a comment expansion: %q", c.Label)
		}
		if !c.Trusted {
			t.Errorf("click on %q was not trusted", c.Label)
		}
		out = append(out, c.Label)
	}
	return out
}

type commentRow struct {
	ID, Parent string
	Depth      int
}

func commentRows(res interface{}) []commentRow {
	var rows []commentRow
	for _, c := range res.([]map[string]interface{}) {
		rows = append(rows, commentRow{c["id"].(string), c["parentId"].(string), c["depth"].(int)})
	}
	return rows
}

func TestBrowserListCommentsExpandsEverything(t *testing.T) {
	fastExpand(t)
	p := phPage(t, "launch_paged.html")
	res, err := bottest.CallMethod(t, &ProductHuntBot{}, p, "list_comments", launchURL)
	if err != nil {
		t.Fatal(err)
	}
	want := []commentRow{
		{"700001", "", 0},
		{"700002", "700001", 1},
		{"700003", "700001", 1},
		{"700004", "700001", 1},
		{"700005", "700001", 1},
		{"700010", "", 0},
		{"700020", "", 0},
		{"700021", "700020", 1},
		{"700022", "700020", 1},
		{"700030", "", 0},
		{"700040", "", 0},
	}
	if got := commentRows(res); !reflect.DeepEqual(got, want) {
		t.Fatalf("comments:\n got %+v\nwant %+v", got, want)
	}
	for _, c := range res.([]map[string]interface{}) {
		if c["id"] == "700021" && (c["author"] != "Sam Maker" || c["text"] != "Free for now.") {
			t.Errorf("loaded reply read wrong: %v", c)
		}
	}
	// Document order each round; the inert "Load more" is tried once and
	// never again.
	wantClicks := []string{"View 3 more replies", "Show more comments", "Show 2 replies", "Show more comments", "Load more"}
	if got := clickLabels(t, p); !reflect.DeepEqual(got, wantClicks) {
		t.Fatalf("clicks = %q, want %q", got, wantClicks)
	}
}

// Expansion stops once maxComments comments are loaded.
func TestBrowserListCommentsExpandStopsAtMaxComments(t *testing.T) {
	fastExpand(t)
	p := phPage(t, "launch_paged.html")
	res, err := bottest.CallMethod(t, &ProductHuntBot{}, p, "list_comments", launchURL, "5")
	if err != nil {
		t.Fatal(err)
	}
	want := []commentRow{{"700001", "", 0}, {"700002", "700001", 1}, {"700003", "700001", 1}, {"700004", "700001", 1}, {"700005", "700001", 1}}
	if got := commentRows(res); !reflect.DeepEqual(got, want) {
		t.Fatalf("comments:\n got %+v\nwant %+v", got, want)
	}
	if got := clickLabels(t, p); !reflect.DeepEqual(got, []string{"View 3 more replies"}) {
		t.Fatalf("clicks = %q, want only the replies expansion", got)
	}

	// Already at maxComments: nothing is clicked.
	res, err = bottest.CallMethod(t, &ProductHuntBot{}, p, "list_comments", launchURL, "2")
	if err != nil {
		t.Fatal(err)
	}
	if got := commentRows(res); len(got) != 2 {
		t.Fatalf("comments = %+v", got)
	}
	if got := clickLabels(t, p); len(got) != 0 {
		t.Fatalf("clicks = %q, want none", got)
	}
}

// The click cap bounds expansion; what is loaded by then is returned.
func TestBrowserListCommentsExpandClickCap(t *testing.T) {
	fastExpand(t)
	expandClicks = 2
	p := phPage(t, "launch_paged.html")
	res, err := bottest.CallMethod(t, &ProductHuntBot{}, p, "list_comments", launchURL, "0")
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, r := range commentRows(res) {
		ids = append(ids, r.ID)
	}
	want := []string{"700001", "700002", "700003", "700004", "700005", "700010", "700020", "700030"}
	if !reflect.DeepEqual(ids, want) {
		t.Fatalf("ids = %v, want %v", ids, want)
	}
	if got := clickLabels(t, p); len(got) != 2 {
		t.Fatalf("clicks = %q, want 2", got)
	}
}

// The time budget bounds expansion too: with none left, nothing is clicked.
func TestBrowserListCommentsExpandTimeBudget(t *testing.T) {
	fastExpand(t)
	expandBudget = 0
	p := phPage(t, "launch_paged.html")
	res, err := bottest.CallMethod(t, &ProductHuntBot{}, p, "list_comments", launchURL)
	if err != nil {
		t.Fatal(err)
	}
	if got := commentRows(res); len(got) != 3 {
		t.Fatalf("comments = %+v, want the 3 initially rendered", got)
	}
	if got := clickLabels(t, p); len(got) != 0 {
		t.Fatalf("clicks = %q, want none", got)
	}
}

// The JSON action (default maxComments 200) expands too.
func TestFlowProductHuntListCommentsExpands(t *testing.T) {
	fastExpand(t)
	p := phPage(t, "launch_paged.html")
	res, err := runAction(t, p, "list_comments", map[string]interface{}{"launchURL": launchURL})
	if err != nil || len(res.ExtractedItems) != 11 {
		t.Fatalf("list_comments: err=%v items=%v", err, res)
	}
	res, err = runAction(t, p, "list_comments", map[string]interface{}{"launchURL": launchURL, "maxComments": 5})
	if err != nil || len(res.ExtractedItems) != 5 {
		t.Fatalf("list_comments maxComments 5: err=%v items=%v", err, res)
	}
}
