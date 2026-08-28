package workflow

import (
	"encoding/json"
	"fmt"
	"regexp"
)

// redactKeyPattern matches map keys whose VALUES are credentials. Matching is
// key-name-only (case-insensitive, exact after trimming nothing) — the
// redactor never inspects or pattern-matches value CONTENT, so a secret that
// lands under an innocuous key is left alone and a public value under a
// sensitive key is still masked (fail-closed on the key name).
var redactKeyPattern = regexp.MustCompile(
	`(?i)^(password|passwd|secret|secrets|token|access_token|refresh_token|id_token|api_key|apikey|authorization|auth|cookie|cookies|client_secret|private_key|bearer)$`)

// RedactedValue replaces every value stored under a sensitive key.
const RedactedValue = "***"

// MaxOutputItemBytes caps a single output item included in execution
// summaries (`workflow run --json`, MCP workflow_run/workflow_status);
// larger items are replaced by a truncation note so one huge scrape
// doesn't make the summary unusable. Item counts are always preserved
// (the array shape never changes).
const MaxOutputItemBytes = 4 * 1024

// RedactItems returns a copy of items with every value stored under a
// sensitive key (see redactKeyPattern) replaced by RedactedValue. The walk
// is recursive through nested maps and arrays; non-matching keys, and
// values not reachable from a matching key, are copied through unchanged.
// The input is never mutated. A nil slice yields nil.
func RedactItems(items []map[string]any) []map[string]any {
	if items == nil {
		return nil
	}
	out := make([]map[string]any, len(items))
	for i, it := range items {
		out[i] = redactMap(it)
	}
	return out
}

// redactMap returns a redacted copy of m.
func redactMap(m map[string]any) map[string]any {
	if m == nil {
		return nil
	}
	out := make(map[string]any, len(m))
	for k, v := range m {
		if redactKeyPattern.MatchString(k) {
			out[k] = RedactedValue
			continue
		}
		out[k] = redactValue(v)
	}
	return out
}

// redactValue recursively redacts maps and arrays; scalars pass through.
func redactValue(v any) any {
	switch t := v.(type) {
	case map[string]any:
		return redactMap(t)
	case []any:
		out := make([]any, len(t))
		for i, e := range t {
			out[i] = redactValue(e)
		}
		return out
	default:
		return v
	}
}

// RedactItemJSON returns a copy of item with its JSON map redacted (Binary
// passes through untouched), for callers that deal in Item values.
func RedactItemJSON(item Item) Item {
	return Item{JSON: redactMap(item.JSON), Binary: item.Binary}
}

// TruncateItems returns items with any single item whose JSON encoding
// exceeds MaxOutputItemBytes (or that fails to encode) replaced by a stub
// noting the original size. The input is never mutated.
func TruncateItems(items []Item) []Item {
	out := make([]Item, len(items))
	for i, it := range items {
		b, err := json.Marshal(it)
		if err == nil && len(b) <= MaxOutputItemBytes {
			out[i] = it
			continue
		}
		note := "output item exceeded 4KB and was truncated"
		if err != nil {
			note = fmt.Sprintf("output item could not be encoded (%v) and was truncated", err)
		}
		out[i] = Item{JSON: map[string]interface{}{
			"truncated":      true,
			"original_bytes": len(b),
			"note":           note,
		}}
	}
	return out
}

// RedactAndTruncateItems applies redaction FIRST, then the size truncation,
// so a masked item smaller than the limit keeps its shape and an oversized
// item is still replaced by the truncation stub. This is the exact pipeline
// used for both `workflow run --json` and the MCP workflow_run /
// workflow_status tools.
func RedactAndTruncateItems(items []Item) []Item {
	if items == nil {
		return nil
	}
	maps := make([]map[string]any, len(items))
	for i, it := range items {
		maps[i] = it.JSON
	}
	redacted := RedactItems(maps)
	out := make([]Item, len(items))
	for i, it := range items {
		out[i] = Item{JSON: redacted[i], Binary: it.Binary}
	}
	return TruncateItems(out)
}
