package noderegistry

import (
	"encoding/json"
	"testing"

	"github.com/monoes/mono-agent/internal/workflow"
)

// TestBuildTypesHaveSchemas asserts every node type registered by the default
// registry resolves to an embedded schema via workflow.ReadEmbeddedSchema
// (directly or through the documented browser fallbacks), and that the schema
// is valid JSON. This keeps `monoagentcli node schema <type>` working for
// every type listed by `monoagentcli node list`.
func TestBuildTypesHaveSchemas(t *testing.T) {
	registry := Build(nil)
	types := registry.Types()
	if len(types) == 0 {
		t.Fatal("registry built with no node types")
	}

	var missing []string
	for _, nodeType := range types {
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
	t.Logf("%d registered node types all resolve to embedded schemas", len(types))
}
