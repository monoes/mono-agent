package workflow

import "testing"

// BenchmarkExpressionEngine_Simple measures evaluating a single top-level
// field reference, e.g. {{ $json.field }}.
func BenchmarkExpressionEngine_Simple(b *testing.B) {
	engine := NewExpressionEngine()
	ctx := ExpressionContext{
		JSON: map[string]interface{}{"field": "value"},
	}
	tmpl := `{{ $json.field }}`
	// Warm the parse cache so the loop measures execution, not first-parse.
	if _, err := engine.EvaluateString(tmpl, ctx); err != nil {
		b.Fatalf("warmup EvaluateString: %v", err)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := engine.EvaluateString(tmpl, ctx); err != nil {
			b.Fatalf("EvaluateString: %v", err)
		}
	}
}

// BenchmarkExpressionEngine_Nested measures evaluating a nested field
// reference chained through several map levels, e.g. {{ $json.a.b.c }}.
func BenchmarkExpressionEngine_Nested(b *testing.B) {
	engine := NewExpressionEngine()
	ctx := ExpressionContext{
		JSON: map[string]interface{}{
			"a": map[string]interface{}{
				"b": map[string]interface{}{
					"c": "value",
				},
			},
		},
	}
	tmpl := `{{ $json.a.b.c }}`
	if _, err := engine.EvaluateString(tmpl, ctx); err != nil {
		b.Fatalf("warmup EvaluateString: %v", err)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := engine.EvaluateString(tmpl, ctx); err != nil {
			b.Fatalf("EvaluateString: %v", err)
		}
	}
}

// BenchmarkExpressionEngine_FallbackChain measures a longer expression that
// combines a node-output lookup with a default fallback, representative of
// expressions seen in real workflow configs.
func BenchmarkExpressionEngine_FallbackChain(b *testing.B) {
	engine := NewExpressionEngine()
	ctx := ExpressionContext{
		JSON: map[string]interface{}{"a": map[string]interface{}{"b": map[string]interface{}{"c": ""}}},
		Node: map[string][]Item{
			"Previous": {{JSON: map[string]interface{}{"fallback": "default-value"}}},
		},
	}
	tmpl := `{{ if $json.a.b.c }}{{ $json.a.b.c }}{{ else }}{{ $node["Previous"].json.fallback }}{{ end }}`
	if _, err := engine.EvaluateString(tmpl, ctx); err != nil {
		b.Fatalf("warmup EvaluateString: %v", err)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := engine.EvaluateString(tmpl, ctx); err != nil {
			b.Fatalf("EvaluateString: %v", err)
		}
	}
}
