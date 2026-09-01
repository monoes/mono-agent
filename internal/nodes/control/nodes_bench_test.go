package control

import (
	"context"
	"fmt"
	"testing"

	"github.com/monoes/mono-agent/internal/workflow"
)

// benchItems builds n items shaped like {"id": i, "name": "item-i", "score": i}.
func benchItems(n int) []workflow.Item {
	items := make([]workflow.Item, n)
	for i := 0; i < n; i++ {
		items[i] = workflow.Item{JSON: map[string]interface{}{
			"id":    i,
			"name":  fmt.Sprintf("item-%d", i),
			"score": i,
		}}
	}
	return items
}

// BenchmarkSetNode_1000Items measures core.set throughput assigning two
// fields (one template expression, one static value) across 1000 items.
func BenchmarkSetNode_1000Items(b *testing.B) {
	node := &SetNode{}
	items := benchItems(1000)
	config := map[string]interface{}{
		"assignments": []interface{}{
			map[string]interface{}{"field": "label", "value": "{{ $json.name }}", "type": "string"},
			map[string]interface{}{"field": "status", "value": "active", "type": "string"},
		},
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := node.Execute(context.Background(), workflow.NodeInput{Items: items}, config); err != nil {
			b.Fatalf("Execute: %v", err)
		}
	}
}

// BenchmarkFilterNode_1000Items measures core.filter throughput evaluating a
// boolean condition across 1000 items.
func BenchmarkFilterNode_1000Items(b *testing.B) {
	node := &FilterNode{}
	items := benchItems(1000)
	config := map[string]interface{}{
		"condition": `{{ gt $json.score 500 }}`,
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := node.Execute(context.Background(), workflow.NodeInput{Items: items}, config); err != nil {
			b.Fatalf("Execute: %v", err)
		}
	}
}

// BenchmarkCodeNode_1000Items measures core.code throughput running a small
// JS transform over 1000 input items via goja.
func BenchmarkCodeNode_1000Items(b *testing.B) {
	node := &CodeNode{}
	items := benchItems(1000)
	config := map[string]interface{}{
		"code": `
			(function() {
				var all = $input.all();
				return all.map(function(item) {
					return { id: item.id, doubled: item.score * 2 };
				});
			})();
		`,
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := node.Execute(context.Background(), workflow.NodeInput{Items: items}, config); err != nil {
			b.Fatalf("Execute: %v", err)
		}
	}
}
