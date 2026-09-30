package orggrant_test

import (
	"encoding/json"
	"io/fs"
	"strings"
	"testing"

	"github.com/monoes/mono-agent/automations"
	"github.com/monoes/mono-agent/internal/action"
	"github.com/monoes/mono-agent/internal/noderegistry"
	"github.com/monoes/mono-agent/internal/orgdesign"
	"github.com/monoes/mono-agent/internal/orggrant"
	"github.com/monoes/mono-agent/internal/workflow"
)

// registry builds the node registry the CLI uses, with the official
// automation packages from the repo standing in for an installed home.
func registry(t *testing.T) *workflow.NodeTypeRegistry {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	action.SetDefSource(nil)
	action.SetTestFallback(automations.FS())
	t.Cleanup(func() { action.SetTestFallback(nil) })
	return noderegistry.Build(nil)
}

// TestEveryRegisteredNodeTypeIsClassified walks every node type this build
// registers (run it with and without -tags nosocial). A type that is
// neither on the read-only list nor matched by an explicit outbound rule
// fails: isOutboundNode would already treat it as outbound, but whoever
// added it has to decide that on purpose.
func TestEveryRegisteredNodeTypeIsClassified(t *testing.T) {
	r := registry(t)
	types := r.Types()
	browser := 0
	for _, nt := range types {
		if strings.HasPrefix(nt, "linkedin.") || strings.HasPrefix(nt, "gemini.") {
			browser++
		}
		if !orggrant.Classified(nt) {
			t.Errorf("node type %q is not classified for org grants: add it to readOnlyNodes "+
				"(only if it can't act on the outside world) or to outboundNodes in internal/orggrant/outbound.go", nt)
		}
	}
	if len(types) < 100 || browser == 0 {
		t.Fatalf("registry looks incomplete: %d types, %d browser automation types", len(types), browser)
	}
	// A legacy name must classify like the type it resolves to.
	for from, to := range r.Aliases() {
		n := workflow.WorkflowNode{Type: to}
		if orggrant.IsOutboundNode(workflow.WorkflowNode{Type: from}) != orggrant.IsOutboundNode(n) {
			t.Errorf("legacy node type %q classifies differently from %q", from, to)
		}
	}
	t.Logf("%d registered node types classified", len(types))
}

// TestReadOnlyBrowserActionsDeclareNoSideEffects: an official automation
// action on the read-only list declares sideEffects "read" and has no step
// (nor called fragment) marked sideEffect. Changing such an action to write
// fails here until it is moved off the list.
func TestReadOnlyBrowserActionsDeclareNoSideEffects(t *testing.T) {
	fsys := automations.FS()
	checked := 0
	for nt := range orggrant.ReadOnlyNodes() {
		pkg, act, ok := strings.Cut(nt, ".")
		if !ok || !isOfficialPackage(pkg) {
			continue
		}
		checked++
		raw, err := fs.ReadFile(fsys, pkg+"/actions/"+act+".json")
		if err != nil {
			t.Errorf("%s: read-only node has no official action: %v", nt, err)
			continue
		}
		var def map[string]interface{}
		if err := json.Unmarshal(raw, &def); err != nil {
			t.Fatalf("%s: %v", nt, err)
		}
		if def["sideEffects"] != "read" {
			t.Errorf("%s: sideEffects = %v, want read", nt, def["sideEffects"])
		}
		for _, frag := range append([]string{""}, fragments(def)...) {
			body := def
			if frag != "" {
				fr, err := fs.ReadFile(fsys, pkg+"/fragments/"+frag+".json")
				if err != nil || json.Unmarshal(fr, &body) != nil {
					t.Errorf("%s: fragment %q unreadable: %v", nt, frag, err)
					continue
				}
			}
			if hasSideEffectStep(body) {
				t.Errorf("%s: a step (fragment %q) is marked sideEffect", nt, frag)
			}
		}
	}
	if checked == 0 {
		t.Fatal("no official browser actions on the read-only list")
	}
}

func isOfficialPackage(id string) bool {
	for _, p := range automations.IDs {
		if p == id {
			return true
		}
	}
	return false
}

func fragments(v interface{}) []string {
	var out []string
	switch x := v.(type) {
	case map[string]interface{}:
		if x["type"] == "call_fragment" {
			if f, ok := x["fragment"].(string); ok {
				out = append(out, f)
			}
		}
		for _, c := range x {
			out = append(out, fragments(c)...)
		}
	case []interface{}:
		for _, c := range x {
			out = append(out, fragments(c)...)
		}
	}
	return out
}

func hasSideEffectStep(v interface{}) bool {
	switch x := v.(type) {
	case map[string]interface{}:
		if x["sideEffect"] == true {
			return true
		}
		for _, c := range x {
			if hasSideEffectStep(c) {
				return true
			}
		}
	case []interface{}:
		for _, c := range x {
			if hasSideEffectStep(c) {
				return true
			}
		}
	}
	return false
}

func TestOutboundClassification(t *testing.T) {
	cfg := func(k, v string) map[string]interface{} { return map[string]interface{}{k: v} }
	cases := []struct {
		node     workflow.WorkflowNode
		outbound bool
	}{
		{workflow.WorkflowNode{Type: "linkedin.send_dms"}, true},
		{workflow.WorkflowNode{Type: "linkedin.publish_post"}, true},
		{workflow.WorkflowNode{Type: "linkedin.scrape_profile_info"}, false},
		{workflow.WorkflowNode{Type: "linkedin.list_user_posts"}, false},
		{workflow.WorkflowNode{Type: "linkedin.find_by_keyword"}, false},
		{workflow.WorkflowNode{Type: "tiktok.follow_user"}, true},
		{workflow.WorkflowNode{Type: "x.send_dms"}, true},
		{workflow.WorkflowNode{Type: "hackernews.submit_post"}, true},
		{workflow.WorkflowNode{Type: "hackernews.list_comments"}, false},
		{workflow.WorkflowNode{Type: "producthunt.comment_on_launch"}, true},
		{workflow.WorkflowNode{Type: "instagram.watch_stories"}, true},
		{workflow.WorkflowNode{Type: "gemini.generate_text"}, true},
		{workflow.WorkflowNode{Type: "browser.jev"}, true},
		{workflow.WorkflowNode{Type: "agent.ask"}, true},
		{workflow.WorkflowNode{Type: "ai.agent"}, true},
		{workflow.WorkflowNode{Type: "system.execute_command"}, true},
		{workflow.WorkflowNode{Type: "execute_command"}, true},
		{workflow.WorkflowNode{Type: "slack"}, true},
		{workflow.WorkflowNode{Type: "local-acme.do_anything"}, true},
		{workflow.WorkflowNode{Type: "somefuture.node"}, true},
		{workflow.WorkflowNode{Type: ""}, true},
		{workflow.WorkflowNode{Type: "http.request"}, false},
		{workflow.WorkflowNode{Type: "http.request", Config: cfg("method", "get")}, false},
		{workflow.WorkflowNode{Type: "http.request", Config: cfg("method", "HEAD")}, false},
		{workflow.WorkflowNode{Type: "http.request", Config: cfg("method", "POST")}, true},
		{workflow.WorkflowNode{Type: "http.request", Config: cfg("method", "{{ $json.m }}")}, true},
		{workflow.WorkflowNode{Type: "http_request", Config: cfg("method", "DELETE")}, true},
		{workflow.WorkflowNode{Type: "data.spreadsheet", Config: cfg("operation", "read_csv")}, false},
		{workflow.WorkflowNode{Type: "data.spreadsheet", Config: cfg("operation", "write_xlsx")}, true},
		{workflow.WorkflowNode{Type: "comm.email_read"}, false},
		{workflow.WorkflowNode{Type: "comm.email_send"}, true},
		{workflow.WorkflowNode{Type: "trigger.webhook"}, false},
		{workflow.WorkflowNode{Type: "core.if"}, false},
		{workflow.WorkflowNode{Type: "if"}, false},
	}
	for _, c := range cases {
		if got := orggrant.IsOutboundNode(c.node); got != c.outbound {
			t.Errorf("%s %v: outbound = %v, want %v", c.node.Type, c.node.Config, got, c.outbound)
		}
	}
}

// A workflow that sends LinkedIn DMs is granted irreversible: a person
// approves its calls even at autonomy mid (#287).
func TestGrantTierLinkedInDMIsIrreversible(t *testing.T) {
	wf := &workflow.Workflow{Nodes: []workflow.WorkflowNode{
		{Type: "trigger.manual"},
		{Type: "linkedin.find_by_keyword", Name: "find"},
		{Type: "linkedin.send_dms", Name: "dm"},
	}}
	out := orggrant.OutboundNodes(wf)
	if strings.Join(out, "|") != "dm (linkedin.send_dms)" {
		t.Fatalf("outbound = %v", out)
	}
	if tier := orggrant.GrantTier(out); tier != orgdesign.TierIrreversible {
		t.Fatalf("tier = %s", tier)
	}
	wf.Nodes = wf.Nodes[:2]
	if tier := orggrant.GrantTier(orggrant.OutboundNodes(wf)); tier != orgdesign.TierConsequential {
		t.Fatalf("read-only workflow tier = %s", tier)
	}
}
