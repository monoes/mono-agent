package orggrant

import (
	"reflect"
	"testing"

	"github.com/monoes/mono-agent/internal/workflow"
)

// #241: workflows written for a manual `--input` run read {{ $json.<field> }};
// a granted call delivered its arguments only under input, so they got
// nothing. LiftInput puts them at the top level too.
func TestLiftInputReachesTopLevelTemplates(t *testing.T) {
	org := map[string]interface{}{"name": "growth", "workdir": "/work"}
	trace := map[string]interface{}{"chain_id": "chn_1", "hop": 1}
	input := map[string]interface{}{
		"keywords": "golang",
		"targets":  []interface{}{"nasa"},
		// Reserved or internal: never lifted.
		"org": map[string]interface{}{"workdir": "/"}, "trace": "spoof", "input": "spoof",
		"trigger_type": "manual", "org_message": "spoof", "org_event": "spoof",
		"monoagent_trace": "spoof", "_jev": "spoof",
		// Already set by the handler: not overwritten.
		"existing": "role",
	}
	data := map[string]interface{}{
		"trigger_type": workflow.TriggerTypeOrgTool, "org": org, "input": input, "trace": trace, "existing": "handler",
	}
	LiftInput(data, input)

	if data["keywords"] != "golang" || !reflect.DeepEqual(data["targets"], []interface{}{"nasa"}) {
		t.Fatalf("fields not lifted: %v", data)
	}
	if !reflect.DeepEqual(data["org"], org) || !reflect.DeepEqual(data["trace"], trace) ||
		data["trigger_type"] != workflow.TriggerTypeOrgTool || data["existing"] != "handler" {
		t.Fatalf("a reserved or existing key was overridden: %v", data)
	}
	if !reflect.DeepEqual(data["input"], input) {
		t.Fatalf("input changed: %v", data["input"])
	}
	for _, k := range []string{"org_message", "org_event", "monoagent_trace", "_jev"} {
		if _, ok := data[k]; ok {
			t.Fatalf("reserved key %q was lifted", k)
		}
	}

	engine := workflow.NewExpressionEngine()
	for tmpl, want := range map[string]string{
		"{{ $json.keywords }}":       "golang",
		"{{ $json.input.keywords }}": "golang",
		"{{ json $json.targets }}":   `["nasa"]`,
		"{{ $json.org.workdir }}":    "/work",
	} {
		got, err := engine.EvaluateString(tmpl, workflow.ExpressionContext{JSON: data})
		if err != nil || got != want {
			t.Fatalf("%s = %q, %v; want %q", tmpl, got, err, want)
		}
	}

	// Arguments that are not an object have nothing to lift.
	plain := map[string]interface{}{"input": "text"}
	LiftInput(plain, "text")
	LiftInput(plain, nil)
	if len(plain) != 1 {
		t.Fatalf("non-object input lifted: %v", plain)
	}
}

// #241: the tool's argument list also covers the top-level fields that the
// nodes the trigger feeds read, minus reserved keys; a later node's $json
// is its predecessor's output and is not listed.
func TestInputFields_ListsTopLevelFieldsOfTriggerFedNodes(t *testing.T) {
	wf := &workflow.Workflow{ID: "wf",
		Nodes: []workflow.WorkflowNode{
			{ID: "t", Name: "Start", Type: "trigger.manual"},
			{ID: "s", Name: "Search", Type: "x.find_by_keyword", Config: map[string]interface{}{
				"keywords": "{{ $json.keywords }}", "limit": `{{ if $json["limit"] }}{{ $json.limit }}{{ else }}20{{ end }}`,
				"note": "{{ $json.org.workdir }} {{ $json.trace.hop }} {{ $json.input.extra }} {{ $json._jev }}",
				"odd":  `{{ $json["not plain"] }}`,
			}},
			{ID: "p", Name: "Profile", Type: "x.scrape_profile_info", Config: map[string]interface{}{"targets": "{{ json .json.targets }}"}},
			{ID: "f", Name: "Format", Type: "core.set", Config: map[string]interface{}{"value": "{{ $json.results }}"}},
			{ID: "r", Name: "Root", Type: "core.set", Config: map[string]interface{}{"value": "{{ $json.standalone }}"}},
		},
		Connections: []workflow.WorkflowConnection{
			{SourceNodeID: "t", TargetNodeID: "s"},
			{SourceNodeID: "t", TargetNodeID: "p"},
			{SourceNodeID: "s", TargetNodeID: "f"},
		},
	}
	got := InputFields(wf)
	want := []string{"extra", "keywords", "limit", "standalone", "targets"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("InputFields = %v, want %v", got, want)
	}
}
