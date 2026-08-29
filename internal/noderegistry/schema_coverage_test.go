package noderegistry

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/monoes/mono-agent/internal/workflow"
)

// socialBrowserPlatforms are the opt-in (-tags social) platforms whose node
// types are generated from data/actions JSONs; they resolve through the
// browser-generic runtime schema rather than dedicated embedded schemas.
var socialBrowserPlatforms = []string{
	"instagram.", "linkedin.", "tiktok.", "x.", "hackernews.", "producthunt.",
}

// TestBuildTypesHaveSchemas asserts every node type registered by the registry
// resolves to an embedded schema via workflow.ReadEmbeddedSchema (directly or
// through the documented browser fallbacks), and that the schema is valid
// JSON. This keeps `monoagentcli node schema <type>` working for every type
// listed by `monoagentcli node list`. Social-build browser-action types
// (generated from data/actions) are excluded — they use the browser-generic
// schema path at runtime.
func TestBuildTypesHaveSchemas(t *testing.T) {
	registry := Build(nil)
	types := registry.Types()
	if len(types) == 0 {
		t.Fatal("registry built with no node types")
	}

	var missing []string
	covered := 0
	for _, nodeType := range types {
		if isSocialBrowserType(nodeType) {
			continue
		}
		covered++
		data, ok := workflow.ReadEmbeddedSchema(nodeType)
		if !ok {
			missing = append(missing, nodeType)
			continue
		}
		var schema workflow.NodeSchema
		if err := json.Unmarshal(data, &schema); err != nil {
			t.Errorf("node type %q: schema is not valid NodeSchema JSON: %v", nodeType, err)
		}
	}

	if len(missing) > 0 {
		t.Errorf("%d registered node type(s) have no embedded schema: %v", len(missing), missing)
	}
	t.Logf("%d/%d registered node types resolve to embedded schemas (social browser-action types excluded)",
		covered, len(types))
}

func isSocialBrowserType(nodeType string) bool {
	for _, p := range socialBrowserPlatforms {
		if strings.HasPrefix(nodeType, p) {
			return true
		}
	}
	return false
}
