package workflow

import "testing"

// benchRedactItems builds n items, each with a mix of normal fields and
// credential-shaped fields (matching redactKeyPattern) that must be redacted.
func benchRedactItems(n int) []map[string]any {
	items := make([]map[string]any, n)
	for i := 0; i < n; i++ {
		items[i] = map[string]any{
			"id":           i,
			"name":         "item",
			"status":       "active",
			"password":     "s3cr3t",
			"api_key":      "sk-abc123",
			"access_token": "tok_xyz",
			"nested": map[string]any{
				"client_secret": "nested-secret",
				"note":          "unaffected",
			},
			"tags": []any{"a", "b", "c"},
		}
	}
	return items
}

// BenchmarkRedactItems_1000Items measures redaction throughput over a batch
// of 1000 items with a mix of credential-shaped and normal fields.
func BenchmarkRedactItems_1000Items(b *testing.B) {
	items := benchRedactItems(1000)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = RedactItems(items)
	}
}
