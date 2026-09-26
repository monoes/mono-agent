package bottest

import (
	"sort"
	"strings"
	"testing"

	"github.com/monoes/mono-agent/internal/action"
)

// ImplicitOutputs are keys a browser node adds to every record itself
// (nodes.NormalizeBrowserItem), so actions do not declare them.
var ImplicitOutputs = map[string]bool{"platform": true}

// AssertOutputsDeclared fails t when a record an action emitted carries a
// key its definition does not declare in outputs (any key: success, summary,
// failure, …). records are the node's output items, e.g. the extracted items
// passed through nodes.OutputRecord.
func AssertOutputsDeclared(t testing.TB, automation, actionType string, records []map[string]interface{}) {
	t.Helper()
	if len(records) == 0 {
		return
	}
	def, err := action.GetLoader().Load(automation, actionType)
	if err != nil {
		t.Fatalf("loading %s/%s: %v", automation, actionType, err)
	}
	declared := map[string]bool{}
	for _, names := range def.Outputs {
		for _, n := range names {
			declared[n] = true
		}
	}
	missing := map[string]bool{}
	for _, rec := range records {
		for k := range rec {
			if !declared[k] && !ImplicitOutputs[k] {
				missing[k] = true
			}
		}
	}
	if len(missing) > 0 {
		keys := make([]string, 0, len(missing))
		for k := range missing {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		t.Errorf("%s.%s emits undeclared output keys: %s", automation, actionType, strings.Join(keys, ", "))
	}
}
