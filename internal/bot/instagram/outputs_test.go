//go:build !nosocial

package instagram

import (
	"testing"

	"github.com/monoes/mono-agent/internal/action"
	"github.com/monoes/mono-agent/internal/bot/bottest"
	"github.com/monoes/mono-agent/internal/nodes"
)

// assertOutputsDeclared checks that every key of the node output items a
// flow run produced is declared in the action's outputs. Every flow run
// that succeeds goes through it.
func assertOutputsDeclared(t *testing.T, actionType string, res *action.ExecutionResult) {
	t.Helper()
	if res == nil {
		return
	}
	var recs []map[string]interface{}
	for _, raw := range res.ExtractedItems {
		if rec := nodes.OutputRecord(raw, "instagram"); rec != nil {
			recs = append(recs, rec)
		}
	}
	bottest.AssertOutputsDeclared(t, "instagram", actionType, recs)
}
