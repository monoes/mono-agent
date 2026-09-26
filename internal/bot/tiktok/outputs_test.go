//go:build !nosocial

package tiktok

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
		if rec := nodes.OutputRecord(raw, "tiktok"); rec != nil {
			// Known artifact, not declared on purpose: a comment result
			// ({text, …} with no author field) goes through the node's
			// profile-card heuristic, which copies its text into full_name
			// and name (nodes.NormalizeBrowserItem).
			if _, had := raw["full_name"]; !had {
				delete(rec, "full_name")
			}
			if _, had := raw["name"]; !had {
				delete(rec, "name")
			}
			recs = append(recs, rec)
		}
	}
	bottest.AssertOutputsDeclared(t, "tiktok", actionType, recs)
}
