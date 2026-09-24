package main

import (
	"strings"
	"testing"

	"github.com/monoes/mono-agent/internal/noderegistry"
)

// Every node type the binary registers must have an entry in the offline
// `ref` manual, so `monoagentcli ref node <type>` never answers "unknown
// node type" for a node that `node list` shows.
func TestRefDocsCoverEveryRegisteredNodeType(t *testing.T) {
	for _, typ := range noderegistry.Build(nil).Types() {
		if findNodeDoc(typ) == nil {
			t.Errorf("node type %q is registered but has no `ref node` entry — add one to ref_nodes_more.go", typ)
		}
	}
	for _, typ := range []string{"trigger.manual", "trigger.schedule", "trigger.webhook", "trigger.org"} {
		if findNodeDoc(typ) == nil {
			t.Errorf("trigger %q has no `ref node` entry", typ)
		}
	}
}

// Deprecated ai.* stubs must say so in the manual instead of documenting a
// provider config that no longer runs.
func TestRefDocsFlagDeprecatedAINodes(t *testing.T) {
	for _, typ := range []string{"ai.chat", "ai.extract", "ai.classify", "ai.transform", "ai.agent", "ai.embed"} {
		d := findNodeDoc(typ)
		if d == nil {
			t.Errorf("%s: missing ref entry", typ)
			continue
		}
		if !strings.Contains(d.Short, "DEPRECATED") {
			t.Errorf("%s: ref Short = %q, want it to start with DEPRECATED", typ, d.Short)
		}
	}
}
