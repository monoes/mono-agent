//go:build !nosocial

package hackernews

import (
	"context"
	"fmt"
	"io/fs"
	"strings"
	"testing"

	"github.com/rs/zerolog"

	"github.com/monoes/mono-agent/data"
	"github.com/monoes/mono-agent/internal/action"
	"github.com/monoes/mono-agent/internal/automation"
	"github.com/monoes/mono-agent/internal/bot/bottest"
)

// JSON-flow tests: each data/automations/hackernews/actions/*.json
// definition, loaded from the embedded seed like production, run through the
// real action executor with its package attached (fragments, selectors,
// scripts) against a bottest page. The actions are declarative now: no bot
// adapter is passed, so none of them can reach a Go bot method.

func runAction(t *testing.T, p *bottest.Page, typ string, params map[string]interface{}) (*action.ExecutionResult, error) {
	t.Helper()
	sub, err := fs.Sub(data.AutomationsFS, "automations/hackernews")
	if err != nil {
		t.Fatal(err)
	}
	pkg, err := automation.OpenFS(sub, automation.SourceBuiltin)
	if err != nil {
		t.Fatal(err)
	}
	def, err := pkg.Action(typ)
	if err != nil {
		t.Fatal(err)
	}
	ae := action.NewActionExecutor(context.Background(), p, nil, nil, nil, nil, zerolog.Nop())
	ae.SetPackage(pkg.Context())
	return ae.ExecuteDef(&action.StorageAction{ID: "flow-" + typ, Type: typ, TargetPlatform: "hackernews", Params: params}, def)
}

func TestFlowGetPostMetrics(t *testing.T) {
	p, _ := hnPage(t, file("https://news.ycombinator.com/item?id=70000001", "item.html"))
	res, err := runAction(t, p, "get_post_metrics", map[string]interface{}{"itemID": float64(70000001)})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.ExtractedItems) != 1 || fmt.Sprint(res.ExtractedItems[0]["points"]) != "42" || fmt.Sprint(res.ExtractedItems[0]["comments"]) != "7" {
		t.Fatalf("items = %v", res.ExtractedItems)
	}
}

func TestFlowListComments(t *testing.T) {
	p, _ := hnPage(t,
		file("https://news.ycombinator.com/item?id=70000001&p=2", "item_p2.html"),
		file("https://news.ycombinator.com/item?id=70000001", "item.html"),
	)
	res, err := runAction(t, p, "list_comments", map[string]interface{}{"itemID": "70000001"})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.ExtractedItems) != 7 {
		t.Fatalf("items = %d, want 7 (all depths, both pages)", len(res.ExtractedItems))
	}
	res, err = runAction(t, p, "list_comments", map[string]interface{}{"itemID": "70000001", "topLevelOnly": true})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.ExtractedItems) != 3 {
		t.Fatalf("topLevelOnly items = %d, want 3", len(res.ExtractedItems))
	}
}

func TestFlowReplyToComment(t *testing.T) {
	p, rec := hnPage(t, replyRoutes("reply.html", "item_after_reply.html")...)
	res, err := runAction(t, p, "reply_to_comment", map[string]interface{}{"itemID": float64(70000010), "text": replyText})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.ExtractedItems) != 1 || res.ExtractedItems[0]["commentID"] != "70000099" {
		t.Fatalf("items = %v", res.ExtractedItems)
	}
	if n := len(rec.Matching("POST", "*")); n != 1 {
		t.Fatalf("POSTs = %d", n)
	}
}

func TestFlowReplyNotConfirmedFailsOnce(t *testing.T) {
	p, rec := hnPage(t, replyRoutes("reply.html", "item_after_reply_missing.html")...)
	_, err := runAction(t, p, "reply_to_comment", map[string]interface{}{"itemID": "70000010", "text": replyText})
	if err == nil || !strings.Contains(err.Error(), "not confirmed") {
		t.Fatalf("err = %v", err)
	}
	if n := len(rec.Matching("POST", "*")); n != 1 {
		t.Fatalf("POSTs = %d, want exactly 1 (no retry)", n)
	}
}

func TestFlowSubmitPost(t *testing.T) {
	routes, posted := submitRoutes(bottest.Route{Status: 302, Headers: map[string]string{"Location": "https://news.ycombinator.com/newest"}})
	p, _ := hnPage(t, routes...)
	res, err := runAction(t, p, "submit_post", map[string]interface{}{"title": "Show HN: Synthetic Gadget", "url": "https://example.test/gadget"})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.ExtractedItems) != 1 || res.ExtractedItems[0]["id"] != "70000200" {
		t.Fatalf("items = %v", res.ExtractedItems)
	}
	if *posted != 1 {
		t.Fatalf("POSTs = %d", *posted)
	}
}

func TestFlowSubmitReadBackFailureDoesNotRetry(t *testing.T) {
	routes, posted := submitRoutes(bottest.Route{Status: 302, Headers: map[string]string{"Location": "https://news.ycombinator.com/newest"}})
	p, _ := hnPage(t, routes...)
	// A title that never shows on /submitted: the read-back fails.
	_, err := runAction(t, p, "submit_post", map[string]interface{}{"title": "Unlisted synthetic title"})
	if err == nil || !strings.Contains(err.Error(), "not resubmitting") {
		t.Fatalf("err = %v", err)
	}
	if *posted != 1 {
		t.Fatalf("POSTs = %d, want exactly 1", *posted)
	}
}
