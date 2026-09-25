//go:build !nosocial

package nodes_test

// Output parity of the declarative hackernews package with the native Go
// bot it replaced. Each case runs the real BrowserNode twice on the same
// fixture pages (headless Chromium via bottest), once with the pre-port
// package (testdata/hackernews_native: call_bot_method + requires.native,
// the bot from internal/bot/hackernews) and once with the shipped
// declarative package, and requires identical node output items.
//
//	BOTTEST_BROWSER=/usr/bin/chromium go test -count=1 -run HackerNewsParity ./internal/nodes/

import (
	"context"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"sync/atomic"
	"testing"

	"github.com/monoes/mono-agent/data"
	"github.com/monoes/mono-agent/internal/action"
	"github.com/monoes/mono-agent/internal/automation"
	"github.com/monoes/mono-agent/internal/bot/bottest"
	"github.com/monoes/mono-agent/internal/bot/hackernews"
	"github.com/monoes/mono-agent/internal/browser"
	"github.com/monoes/mono-agent/internal/nodes"
	"github.com/monoes/mono-agent/internal/workflow"
)

const hnTests = "../../data/automations/hackernews/tests"

// pkgSource serves one package as the whole definition source.
type pkgSource struct{ p *automation.Package }

func (s pkgSource) Load(_, name string) ([]byte, error) { return s.p.ActionJSON(name) }
func (s pkgSource) List() ([]string, error)             { return nil, nil }
func (s pkgSource) Package(id string) action.PackageContext {
	if id != s.p.Manifest.ID {
		return nil
	}
	return s.p.Context()
}

type hnBots struct{}

func (hnBots) GetAdapter(platform string) (action.BotAdapter, bool) {
	if platform == "hackernews" {
		return &hackernews.HackerNewsBot{}, true
	}
	return nil, false
}

// onePage hands the node the test's page itself (a wrapper would hide the
// page's EvalCDP/CDP capabilities); the node closes it when done.
type onePage struct{ p browser.PageInterface }

func (o onePage) GetPage(context.Context, string, string) (browser.PageInterface, error) {
	return o.p, nil
}

func nativePkg(t *testing.T) *automation.Package {
	t.Helper()
	p, err := automation.OpenDir("testdata/hackernews_native")
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func declarativePkg(t *testing.T) *automation.Package {
	t.Helper()
	sub, err := fs.Sub(data.AutomationsFS, "automations/hackernews")
	if err != nil {
		t.Fatal(err)
	}
	p, err := automation.OpenFS(sub, automation.SourceBuiltin)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func page(pattern, file string) bottest.Route {
	for _, dir := range []string{"fixtures", "pages"} {
		f := filepath.Join(hnTests, dir, file)
		if _, err := os.Stat(f); err == nil {
			return bottest.Route{Pattern: pattern, File: f}
		}
	}
	panic("no fixture " + file)
}

func body(file string) string {
	for _, dir := range []string{"fixtures", "pages"} {
		if b, err := os.ReadFile(filepath.Join(hnTests, dir, file)); err == nil {
			return string(b)
		}
	}
	panic("no fixture " + file)
}

// runNode runs hackernews.<act> as a workflow node against pkg on a fresh
// page serving routes(), and returns the output items as JSON values.
func runNode(t *testing.T, pkg *automation.Package, act string, config map[string]interface{}, routes func() []bottest.Route) []interface{} {
	t.Helper()
	b := bottest.Launch(t)
	p := b.NewPage(t)
	p.Serve(append(routes(), bottest.Route{Pattern: "https://news.ycombinator.com/s.gif", Body: "", ContentType: "image/gif"})...)
	p.SetCSP("default-src 'self'; script-src 'self' 'unsafe-inline'; style-src 'self' 'unsafe-inline'; img-src 'self' data:")

	action.SetDefSource(pkgSource{pkg})
	nodes.SetGlobalSessionProvider(onePage{p})
	nodes.SetGlobalBotRegistry(hnBots{})
	t.Cleanup(func() { action.SetDefSource(nil) })

	in := workflow.NodeInput{Items: []workflow.Item{workflow.NewItem(map[string]interface{}{"row": 7.0, "note": "from upstream"})}}
	out, err := nodes.NewBrowserNode("hackernews", act).Execute(context.Background(), in, config)
	if err != nil {
		t.Fatalf("%s (%s): %v", act, pkg.Manifest.Version, err)
	}
	var items []map[string]interface{}
	for _, it := range out[0].Items {
		items = append(items, it.JSON)
	}
	var v []interface{}
	b2, _ := json.Marshal(items)
	_ = json.Unmarshal(b2, &v)
	return v
}

func TestHackerNewsParityNodeOutput(t *testing.T) {
	if os.Getenv("BOTTEST_BROWSER") == "" && os.Getenv("JEV_E2E_BROWSER") == "" {
		t.Skip("needs BOTTEST_BROWSER")
	}
	submit := func() []bottest.Route {
		var posted int32
		return []bottest.Route{
			page("https://news.ycombinator.com/submit", "submit.html"),
			{Pattern: "https://news.ycombinator.com/r", Method: "POST", Handler: func(bottest.Request) bottest.Response {
				atomic.AddInt32(&posted, 1)
				return bottest.Response{Status: 302, Headers: map[string]string{"Location": "https://news.ycombinator.com/newest"}}
			}},
			{Pattern: "https://news.ycombinator.com/submitted?id=test_user", Handler: func(bottest.Request) bottest.Response {
				f := "submitted_before.html"
				if atomic.LoadInt32(&posted) > 0 {
					f = "submitted_after.html"
				}
				return bottest.Response{Body: body(f), ContentType: "text/html; charset=utf-8"}
			}},
			page("https://news.ycombinator.com/newest", "newest.html"),
		}
	}
	cases := []struct {
		name, act string
		config    map[string]interface{}
		routes    func() []bottest.Route
	}{
		{"metrics", "get_post_metrics", map[string]interface{}{"itemID": 70000001.0}, func() []bottest.Route {
			return []bottest.Route{page("https://news.ycombinator.com/item?id=70000001", "get_post_metrics.html")}
		}},
		{"metrics-job", "get_post_metrics", map[string]interface{}{"itemID": "70000050"}, func() []bottest.Route {
			return []bottest.Route{page("https://news.ycombinator.com/item?id=70000050", "job.html")}
		}},
		{"metrics-discuss", "get_post_metrics", map[string]interface{}{"itemID": "70000060"}, func() []bottest.Route {
			return []bottest.Route{page("https://news.ycombinator.com/item?id=70000060", "discuss.html")}
		}},
		{"comments", "list_comments", map[string]interface{}{"itemID": "70000001"}, func() []bottest.Route {
			return []bottest.Route{
				page("https://news.ycombinator.com/item?id=70000001&p=2", "item_p2.html"),
				page("https://news.ycombinator.com/item?id=70000001", "list_comments.html"),
			}
		}},
		{"comments-top", "list_comments", map[string]interface{}{"itemID": 70000001.0, "topLevelOnly": true}, func() []bottest.Route {
			return []bottest.Route{
				page("https://news.ycombinator.com/item?id=70000001&p=2", "item_p2.html"),
				page("https://news.ycombinator.com/item?id=70000001", "list_comments.html"),
			}
		}},
		{"reply", "reply_to_comment", map[string]interface{}{"itemID": "70000010", "text": "Thanks for the *synthetic* write-up.\n\nSecond line here."}, func() []bottest.Route {
			return []bottest.Route{
				page("https://news.ycombinator.com/reply?*", "reply_to_comment.html"),
				{Pattern: "https://news.ycombinator.com/comment*", Method: "POST", Status: 302,
					Headers: map[string]string{"Location": "https://news.ycombinator.com/item?id=70000010"}},
				page("https://news.ycombinator.com/item?id=70000010", "item_after_reply.html"),
			}
		}},
		{"reply-twice", "reply_to_comment", map[string]interface{}{"itemID": "70000010", "text": "Thanks for the *synthetic* write-up.\n\nSecond line here."}, func() []bottest.Route {
			return []bottest.Route{
				page("https://news.ycombinator.com/reply?*", "reply_to_comment.html"),
				{Pattern: "https://news.ycombinator.com/comment*", Method: "POST", Status: 302,
					Headers: map[string]string{"Location": "https://news.ycombinator.com/item?id=70000010"}},
				page("https://news.ycombinator.com/item?id=70000010", "item_after_reply_twice.html"),
			}
		}},
		{"submit-normalised-title", "submit_post", map[string]interface{}{"title": "  show HN:   *Synthetic*  gadget "}, submit},
		{"submit", "submit_post", map[string]interface{}{"title": "Show HN: Synthetic Gadget", "url": "https://example.test/gadget"}, submit},
	}
	native, decl := nativePkg(t), declarativePkg(t)
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			want := runNode(t, native, c.act, c.config, c.routes)
			got := runNode(t, decl, c.act, c.config, c.routes)
			if len(want) == 0 {
				t.Fatal("native run produced no items")
			}
			if !reflect.DeepEqual(got, want) {
				g, _ := json.MarshalIndent(got, "", " ")
				w, _ := json.MarshalIndent(want, "", " ")
				t.Fatalf("node output differs\ndeclarative: %s\nnative:      %s", g, w)
			}
		})
	}
}
