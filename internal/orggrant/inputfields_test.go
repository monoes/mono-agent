package orggrant

import (
	"reflect"
	"testing"
)

// The C-46 live gate's workflows: a role's path argument must be advertised
// or monomind strips it and the run gets input: {}.
func TestInputFields_ListsTemplatedInputReferences(t *testing.T) {
	wf := wfOf(
		node("Start", "trigger.manual", nil),
		node("Content", "core.set", map[string]interface{}{
			"assignments": `[{"field":"content","value":"{{ $json.input.text }}"}]`, "include_input": true}),
		node("Write", "data.write_binary_file", map[string]interface{}{"file_path": "{{ $json.input.path }}", "field": "content"}),
		node("Mail", "comm.email_send", map[string]interface{}{"attachments": []interface{}{"{{ $json.input['attach'] }}"}}),
		node("Multi", "core.set", map[string]interface{}{"value": "{{\n  .json.input.dest + $json.input[\"file name\"]\n}}"}),
		// not templates, or not the input: ignored
		node("Plain", "core.set", map[string]interface{}{"value": "input.literal", "other": "{{ $json.userinput.x }}"}),
	)
	off := node("Off", "core.set", map[string]interface{}{"value": "{{ $json.input.disabled }}"})
	off.Disabled = true
	wf.Nodes = append(wf.Nodes, off)

	got := InputFields(wf)
	want := []string{"attach", "dest", "file name", "path", "text"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("InputFields = %v, want %v", got, want)
	}
	if got := InputFields(wfOf(node("Start", "trigger.manual", nil))); len(got) != 0 {
		t.Fatalf("no references: got %v", got)
	}
	if InputFields(nil) != nil {
		t.Fatal("nil workflow")
	}
}
