package action_test

// The hackernews package is pure declarative (no call_bot_method, no
// requires.native). These tests run each of its actions through the real
// ActionExecutor, with the package attached, against a headless Chromium
// that serves the package's own fixtures at the real news.ycombinator.com
// URLs under a CSP without 'unsafe-eval' (as Hacker News serves). Outputs
// must equal tests/<action>.expect.json and match what the Go bot
// (internal/bot/hackernews) returned for the same pages.
//
//	BOTTEST_BROWSER=/usr/bin/chromium go test -count=1 -run HackerNews ./internal/action/
//
// Without a browser the browser tests skip; TestHackerNewsPackageIsDeclarative
// always runs.

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/monoes/mono-agent/internal/action"
	"github.com/monoes/mono-agent/internal/automation"
	"github.com/monoes/mono-agent/internal/bot/bottest"
	"github.com/rs/zerolog"
)

const hnDir = "../../data/automations/hackernews"

const hnCSP = "default-src 'self'; script-src 'self' 'unsafe-inline'; style-src 'self' 'unsafe-inline'; img-src 'self' data:"

func hnPackage(t *testing.T) *automation.Package {
	t.Helper()
	pkg, err := automation.OpenDir(hnDir)
	if err != nil {
		t.Fatal(err)
	}
	return pkg
}

// TestHackerNewsPackageIsDeclarative: the package validates cleanly, needs
// no native bot, carries no page script, and no action or fragment calls a
// Go bot method.
func TestHackerNewsPackageIsDeclarative(t *testing.T) {
	pkg := hnPackage(t)
	if n := pkg.Manifest.Requires.Native; n != "" {
		t.Fatalf("requires.native = %q, want none", n)
	}
	if pkg.Manifest.Policy.Tier != "social" {
		t.Fatalf("policy.tier = %q, want social", pkg.Manifest.Policy.Tier)
	}
	if s := pkg.ScriptFiles(); len(s) > 0 || len(pkg.Manifest.Permissions.Scripts) > 0 {
		t.Fatalf("scripts = %v / %v, want none", s, pkg.Manifest.Permissions.Scripts)
	}
	for _, is := range automation.Validate(pkg) {
		if is.Severity == "error" || is.Code == "script_used" || is.Code == "contains_scripts" {
			t.Errorf("%s %s %s: %s", is.File, is.StepID, is.Code, is.Message)
		}
	}
	files, err := pkg.Files()
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		if !strings.HasSuffix(f, ".json") || strings.HasPrefix(f, "tests/") {
			continue
		}
		b, err := os.ReadFile(filepath.Join(hnDir, f))
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(b), "call_bot_method") {
			t.Errorf("%s still uses call_bot_method", f)
		}
	}
	// permissions.steps is exactly the set of step types used.
	used := map[string]bool{}
	var walk func([]action.StepDef)
	walk = func(ss []action.StepDef) {
		for _, s := range ss {
			used[s.Type] = true
			walk(s.Steps)
		}
	}
	for _, name := range pkg.Manifest.Actions {
		def, err := pkg.Action(name)
		if err != nil {
			t.Fatal(err)
		}
		if def.SideEffects == "" {
			t.Errorf("%s declares no sideEffects", name)
		}
		walk(def.Steps)
	}
	for _, name := range pkg.FragmentNames() {
		frag, err := pkg.Fragment(name)
		if err != nil {
			t.Fatal(err)
		}
		walk(frag.Steps)
	}
	perm := map[string]bool{}
	for _, s := range pkg.Manifest.Permissions.Steps {
		perm[s] = true
	}
	if !reflect.DeepEqual(used, perm) {
		t.Errorf("permissions.steps = %v, step types used = %v", keys(perm), keys(used))
	}
}

func keys(m map[string]bool) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	return out
}

// --- browser harness --------------------------------------------------------

// fx serves a package fixture (tests/fixtures or tests/pages) at pattern.
func fx(pattern, file string) bottest.Route {
	dir := "pages"
	if !strings.Contains(file, "/") {
		if _, err := os.Stat(filepath.Join(hnDir, "tests/fixtures", file)); err == nil {
			dir = "fixtures"
		}
	}
	return bottest.Route{Pattern: pattern, File: filepath.Join(hnDir, "tests", dir, file)}
}

func fxBody(file string) string {
	for _, dir := range []string{"fixtures", "pages"} {
		if b, err := os.ReadFile(filepath.Join(hnDir, "tests", dir, file)); err == nil {
			return string(b)
		}
	}
	panic("no fixture " + file)
}

func hnBrowserPage(t *testing.T, routes ...bottest.Route) (*bottest.Page, *bottest.Recorder) {
	t.Helper()
	b := bottest.Launch(t)
	p := b.NewPage(t)
	routes = append(routes, bottest.Route{Pattern: "https://news.ycombinator.com/s.gif", Body: "", ContentType: "image/gif"})
	rec := p.Serve(routes...)
	p.SetCSP(hnCSP)
	return p, rec
}

// runHN runs actions/<name>.json with params as its inputs and returns the
// records it produced.
func runHN(t *testing.T, page *bottest.Page, name string, params map[string]interface{}) ([]map[string]interface{}, error) {
	t.Helper()
	pkg := hnPackage(t)
	def, err := pkg.Action(name)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	logger := zerolog.Nop()
	if os.Getenv("HN_DEBUG") != "" {
		logger = zerolog.New(os.Stderr).Level(zerolog.DebugLevel)
	}
	ae := action.NewActionExecutor(ctx, page, nil, nil, nil, nil, logger)
	ae.SetPackage(pkg.Context())
	res, err := ae.ExecuteDef(&action.StorageAction{ID: "hn-test", Type: name, TargetPlatform: "hackernews", Params: params}, def)
	if res == nil {
		return nil, err
	}
	return res.ExtractedItems, err
}

// jsonEq compares a result with an expectation through JSON, so numbers and
// nulls compare the way the node output serialises them.
func jsonEq(t *testing.T, got interface{}, want interface{}) {
	t.Helper()
	g, _ := json.Marshal(got)
	w, _ := json.Marshal(want)
	var gv, wv interface{}
	_ = json.Unmarshal(g, &gv)
	_ = json.Unmarshal(w, &wv)
	if !reflect.DeepEqual(gv, wv) {
		t.Fatalf("result mismatch\n got %s\nwant %s", g, w)
	}
}

func expectFile(t *testing.T, name string) interface{} {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(hnDir, "tests", name+".expect.json"))
	if err != nil {
		t.Fatal(err)
	}
	var v interface{}
	if err := json.Unmarshal(b, &v); err != nil {
		t.Fatal(err)
	}
	// {"records": [...], "requests": [...]} (automation test's form) → records.
	if m, ok := v.(map[string]interface{}); ok {
		return m["records"]
	}
	return v
}

func wantErr(t *testing.T, err error, substrs ...string) {
	t.Helper()
	if err == nil {
		t.Fatalf("want an error containing %q, got none", substrs)
	}
	for _, s := range substrs {
		if !strings.Contains(err.Error(), s) {
			t.Fatalf("error %q does not contain %q", err, s)
		}
	}
}

// --- get_post_metrics -------------------------------------------------------

func TestHackerNewsGetPostMetrics(t *testing.T) {
	p, _ := hnBrowserPage(t,
		fx("https://news.ycombinator.com/item?id=70000001", "get_post_metrics.html"),
		fx("https://news.ycombinator.com/item?id=70000050", "job.html"),
		fx("https://news.ycombinator.com/item?id=70000060", "discuss.html"),
		bottest.Route{Pattern: "https://news.ycombinator.com/item?id=70000077", Body: "No such item."},
	)
	// A JSON number, as a workflow would deliver it.
	got, err := runHN(t, p, "get_post_metrics", map[string]interface{}{"itemID": float64(70000001)})
	if err != nil {
		t.Fatal(err)
	}
	jsonEq(t, got, expectFile(t, "get_post_metrics"))

	got, err = runHN(t, p, "get_post_metrics", map[string]interface{}{"itemID": "70000050"})
	if err != nil {
		t.Fatal(err)
	}
	jsonEq(t, got, []map[string]interface{}{{"itemID": "70000050", "points": nil, "comments": nil,
		"title": "Example Corp is hiring synthetic engineers", "author": "", "age": "2030-01-02T01:00:00", "isJob": true}})

	got, err = runHN(t, p, "get_post_metrics", map[string]interface{}{"itemID": " 70000060 "})
	if err != nil {
		t.Fatal(err)
	}
	jsonEq(t, got, []map[string]interface{}{{"itemID": "70000060", "points": 1, "comments": 0,
		"title": "Ask HN: A question nobody answered", "author": "henry_example", "age": "2030-01-02T01:00:00", "isJob": false}})

	_, err = runHN(t, p, "get_post_metrics", map[string]interface{}{"itemID": "70000077"})
	wantErr(t, err, "item 70000077 not found", "No such item.")

	_, err = runHN(t, p, "get_post_metrics", map[string]interface{}{"itemID": "1 OR 1"})
	wantErr(t, err, "item id must be numeric")
}

// --- list_comments ----------------------------------------------------------

func TestHackerNewsListComments(t *testing.T) {
	p, _ := hnBrowserPage(t,
		fx("https://news.ycombinator.com/item?id=70000001&p=2", "item_p2.html"),
		fx("https://news.ycombinator.com/item?id=70000001", "list_comments.html"),
		bottest.Route{Pattern: "https://news.ycombinator.com/item?id=70000077", Body: "No such item."},
	)
	got, err := runHN(t, p, "list_comments", map[string]interface{}{"itemID": float64(70000001)})
	if err != nil {
		t.Fatal(err)
	}
	if os.Getenv("HN_WRITE_EXPECT") != "" {
		b, _ := json.MarshalIndent(got, "", "  ")
		_ = os.WriteFile(filepath.Join(hnDir, "tests/list_comments.expect.json"), append(b, '\n'), 0o644)
	}
	jsonEq(t, got, expectFile(t, "list_comments"))

	// The bot's thread shape (internal/bot/hackernews browser_test.go).
	var rows []string
	for _, c := range got {
		rows = append(rows, fmt.Sprintf("%v/%v/%v/%v/%v", c["id"], c["author"], c["parentId"], c["depth"], c["deleted"]))
	}
	want := []string{
		"70000010/bob_example/70000001/0/false",
		"70000011/carol_example/70000010/1/false",
		"70000012/dave_example/70000011/2/false",
		"70000013//70000010/1/true",
		"70000014/erin_example/70000001/0/false",
		"70000020/frank_example/70000014/1/false", // page 2 continues erin's thread
		"70000021/grace_example/70000001/0/false",
	}
	if !reflect.DeepEqual(rows, want) {
		t.Fatalf("comments:\n got %v\nwant %v", rows, want)
	}
	if txt := got[0]["text"]; txt != "First synthetic paragraph about the widget.\n\nSecond paragraph with a link." {
		t.Fatalf("paragraph text = %q", txt)
	}

	got, err = runHN(t, p, "list_comments", map[string]interface{}{"itemID": "70000001", "topLevelOnly": true})
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, c := range got {
		ids = append(ids, fmt.Sprint(c["id"]))
	}
	if strings.Join(ids, ",") != "70000010,70000014,70000021" {
		t.Fatalf("topLevelOnly ids = %v", ids)
	}

	// Rows without td.ind[indent]: the depth comes from the indent image's
	// width (40px per level), as with the bot.
	p2, _ := hnBrowserPage(t,
		fx("https://news.ycombinator.com/item?id=70000001&p=2", "item_p2.html"),
		fx("https://news.ycombinator.com/item?id=70000001", "item_noindent.html"),
	)
	got, err = runHN(t, p2, "list_comments", map[string]interface{}{"itemID": "70000001"})
	if err != nil {
		t.Fatal(err)
	}
	jsonEq(t, got, expectFile(t, "list_comments"))

	_, err = runHN(t, p, "list_comments", map[string]interface{}{"itemID": "70000077"})
	wantErr(t, err, "item 70000077 not found", "No such item.")
	_, err = runHN(t, p, "list_comments", map[string]interface{}{"itemID": "7&x=1"})
	wantErr(t, err, "item id must be numeric")
}
