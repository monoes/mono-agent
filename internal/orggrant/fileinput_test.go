package orggrant

import (
	"reflect"
	"strings"
	"testing"

	"github.com/monoes/mono-agent/internal/workflow"
)

func wfOf(nodes ...workflow.WorkflowNode) *workflow.Workflow {
	return &workflow.Workflow{ID: "wf", Nodes: nodes}
}

func node(name, typ string, cfg map[string]interface{}) workflow.WorkflowNode {
	return workflow.WorkflowNode{Name: name, Type: typ, Config: cfg}
}

func TestFileInputNodes_FlagsTemplatedPaths(t *testing.T) {
	wf := wfOf(
		node("Trigger", "trigger.manual", nil),
		node("Save", "data.write_binary_file", map[string]interface{}{"file_path": "{{ $json.input.path }}"}),
		node("Load", "data.spreadsheet", map[string]interface{}{"operation": "read_csv", "file_path": "{{ .json.input.file }}"}),
		node("Mail", "comm.email_send", map[string]interface{}{"attachments": []interface{}{"/fixed/report.pdf", "{{ $json.input.attach }}"}}),
		node("Pull", "http.ftp", map[string]interface{}{"operation": "download", "local_path": "{{ $json.input.dest }}"}),
		node("Shell", "system.execute_command", map[string]interface{}{"command": "cat", "args": []interface{}{"{{ $json.input.path }}"}}),
	)
	got := FileInputNodes(wf)
	want := []FileInputNode{
		{Node: "Load (data.spreadsheet)", Type: "data.spreadsheet", Field: "file_path", Access: AccessRead, Confined: true},
		{Node: "Mail (comm.email_send)", Type: "comm.email_send", Field: "attachments", Access: AccessRead, Confined: true},
		{Node: "Pull (http.ftp)", Type: "http.ftp", Field: "local_path", Access: AccessWrite, Confined: true},
		{Node: "Save (data.write_binary_file)", Type: "data.write_binary_file", Field: "file_path", Access: AccessWrite, Confined: true},
		{Node: "Shell (system.execute_command)", Type: "system.execute_command", Field: "args", Access: AccessExec, Confined: false},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("FileInputNodes =\n%+v\nwant\n%+v", got, want)
	}
}

func TestFileInputNodes_ImageNodesTakeThePathFromTheItem(t *testing.T) {
	got := FileInputNodes(wfOf(
		node("Resize", "image.resize", map[string]interface{}{"width": 10}),
		node("Thumb", "image.thumbnail", map[string]interface{}{"field": "photo", "output_dir": "/fixed"}),
	))
	if len(got) != 2 || got[0].Field != "item:image_path" || got[1].Field != "item:photo" || got[0].Access != AccessReadWrite {
		t.Fatalf("image nodes = %+v", got)
	}
}

func TestFileInputNodes_IgnoresFixedPathsDisabledNodesAndOtherNodes(t *testing.T) {
	disabled := node("Off", "data.write_binary_file", map[string]interface{}{"file_path": "{{ $json.p }}"})
	disabled.Disabled = true
	got := FileInputNodes(wfOf(
		node("Fixed", "data.write_binary_file", map[string]interface{}{"file_path": "/srv/out.bin"}),
		node("Fixed sheet", "data.spreadsheet", map[string]interface{}{"operation": "write_xlsx", "file_path": "report.xlsx"}),
		disabled,
		node("HTTP", "http.request", map[string]interface{}{"url": "{{ $json.input.url }}"}),
		node("Shell", "system.execute_command", map[string]interface{}{"command": "date"}),
	))
	if len(got) != 0 {
		t.Fatalf("flagged %+v, want nothing", got)
	}
	if FileInputNodes(nil) != nil {
		t.Fatal("nil workflow flagged")
	}
}

func TestFileInputNodes_SpreadsheetAccessFollowsOperation(t *testing.T) {
	for op, want := range map[string]string{"read_xlsx": AccessRead, "write_csv": AccessWrite, "": AccessReadWrite} {
		got := FileInputNodes(wfOf(node("S", "data.spreadsheet", map[string]interface{}{"operation": op, "file_path": "{{ $json.f }}"})))
		if len(got) != 1 || got[0].Access != want {
			t.Errorf("operation %q: %+v, want access %s", op, got, want)
		}
	}
}

func TestFileInputWarning(t *testing.T) {
	if FileInputWarning(nil) != "" {
		t.Fatal("warning for no nodes")
	}
	confined := []FileInputNode{{Node: "Save (data.write_binary_file)", Confined: true}}
	if w := FileInputWarning(confined); w == "" || !strings.Contains(w, "workdir") {
		t.Fatalf("confined warning = %q", w)
	}
	open := append(confined, FileInputNode{Node: "Shell (system.execute_command)", Confined: false})
	if w := FileInputWarning(open); !strings.Contains(w, "Shell (system.execute_command)") || !strings.Contains(w, "cannot") {
		t.Fatalf("unconfined warning = %q", w)
	}
}
