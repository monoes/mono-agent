package control

import (
	"context"
	"strings"
	"testing"

	"github.com/monoes/mono-agent/internal/workflow"
)

// TestFilterInvalidConditionFailsNode is a regression test: a condition that
// cannot be parsed (Go templates have no `!=` operator) used to be treated as
// a clean false — all items dropped, execution still SUCCESS. It must now
// fail the node with a wrapped error.
func TestFilterInvalidConditionFailsNode(t *testing.T) {
	node := &FilterNode{}
	_, err := node.Execute(context.Background(), workflow.NodeInput{
		Items: []workflow.Item{{JSON: map[string]interface{}{"title": "hello"}}},
	}, map[string]interface{}{
		"condition": `{{ $json.title != "" }}`,
	})
	if err == nil {
		t.Fatalf("expected error for unparseable condition, got nil")
	}
	if !strings.HasPrefix(err.Error(), "filter condition error:") {
		t.Errorf("error %q should be wrapped as \"filter condition error: ...\"", err)
	}
}

// TestFilterNonBooleanConditionFailsWithoutLeakingValue verifies that a
// condition rendering a non-boolean string fails the node AND that the error
// message does not echo the evaluated item value back.
func TestFilterNonBooleanConditionFailsWithoutLeakingValue(t *testing.T) {
	node := &FilterNode{}
	_, err := node.Execute(context.Background(), workflow.NodeInput{
		Items: []workflow.Item{{JSON: map[string]interface{}{"title": "SECRET-HEADLINE"}}},
	}, map[string]interface{}{
		"condition": `{{ $json.title }}`,
	})
	if err == nil {
		t.Fatalf("expected error for non-boolean condition, got nil")
	}
	if strings.Contains(err.Error(), "SECRET-HEADLINE") {
		t.Errorf("error %q leaks the evaluated item value", err)
	}
	if !strings.HasPrefix(err.Error(), "filter condition error:") {
		t.Errorf("error %q should be wrapped as \"filter condition error: ...\"", err)
	}
}

// TestFilterNeConditionKeepsTitledItems is the documented valid form: a
// boolean-producing comparison via the `ne` builtin. Items with a non-empty
// title pass, items with an empty title are rejected.
func TestFilterNeConditionKeepsTitledItems(t *testing.T) {
	node := &FilterNode{}
	out, err := node.Execute(context.Background(), workflow.NodeInput{
		Items: []workflow.Item{
			{JSON: map[string]interface{}{"title": "first"}},
			{JSON: map[string]interface{}{"title": ""}},
			{JSON: map[string]interface{}{"title": "third"}},
		},
	}, map[string]interface{}{
		"condition": `{{ ne $json.title "" }}`,
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	var passed, rejected []workflow.Item
	for _, o := range out {
		if o.Handle == "main" {
			passed = o.Items
		}
		if o.Handle == "rejected" {
			rejected = o.Items
		}
	}
	if len(passed) != 2 || passed[0].JSON["title"] != "first" || passed[1].JSON["title"] != "third" {
		t.Errorf("main handle should keep the two titled items, got %v", passed)
	}
	if len(rejected) != 1 || rejected[0].JSON["title"] != "" {
		t.Errorf("rejected handle should hold the untitled item, got %v", rejected)
	}
}

// TestFilterRemoveModeInvertsKeep verifies mode "remove" still works with the
// error-fail change in place.
func TestFilterRemoveModeInvertsKeep(t *testing.T) {
	node := &FilterNode{}
	out, err := node.Execute(context.Background(), workflow.NodeInput{
		Items: []workflow.Item{
			{JSON: map[string]interface{}{"title": "keep-me"}},
			{JSON: map[string]interface{}{"title": ""}},
		},
	}, map[string]interface{}{
		"condition": `{{ ne $json.title "" }}`,
		"mode":      "remove",
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	for _, o := range out {
		if o.Handle == "main" && len(o.Items) != 1 {
			t.Errorf("remove mode should keep only the untitled item, got %v", o.Items)
		}
	}
}
