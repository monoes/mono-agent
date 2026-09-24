package workflow

import (
	"context"
	"testing"

	"github.com/rs/zerolog"
)

// triggerProbeNode records the trigger data its context carries.
type triggerProbeNode struct {
	got     *map[string]interface{}
	gotType *string
}

func (triggerProbeNode) Type() string { return "test.triggerprobe" }
func (n triggerProbeNode) Execute(ctx context.Context, in NodeInput, _ map[string]interface{}) ([]NodeOutput, error) {
	*n.got = TriggerDataFrom(ctx)
	*n.gotType = TriggerTypeFrom(ctx)
	return []NodeOutput{{Handle: "main", Items: in.Items}}, nil
}

// Every node sees the execution's trigger data through its context, so an
// org node can continue the chain trace even when a node before it dropped
// the `trace` field.
func TestRunExecution_PutsTriggerDataInNodeContext(t *testing.T) {
	var got map[string]interface{}
	var gotType string
	reg := NewNodeTypeRegistry()
	reg.Register("test.triggerprobe", func() NodeExecutor { return triggerProbeNode{got: &got, gotType: &gotType} })
	wf := &Workflow{
		ID: "wf-td",
		Nodes: []WorkflowNode{
			{ID: "t", WorkflowID: "wf-td", Type: "trigger.manual", Name: "T"},
			{ID: "p", WorkflowID: "wf-td", Type: "test.triggerprobe", Name: "Probe"},
		},
		Connections: []WorkflowConnection{{SourceNodeID: "t", SourceHandle: "main", TargetNodeID: "p", TargetHandle: "main"}},
	}
	dag, err := BuildDAG(wf.Nodes, wf.Connections)
	if err != nil {
		t.Fatal(err)
	}
	td := map[string]interface{}{"trace": map[string]interface{}{"chain_id": "chn_x", "hop": 2}}
	exec := &WorkflowExecution{ID: "e-td", WorkflowID: wf.ID, TriggerNodeID: "t", TriggerType: TriggerTypeOrgTool, TriggerData: td}
	if err := RunExecution(context.Background(), exec, wf, dag, reg, &stubStore{}, nil, NewExpressionEngine(), zerolog.Nop()); err != nil {
		t.Fatalf("RunExecution: %v", err)
	}
	if tr, _ := got["trace"].(map[string]interface{}); tr["chain_id"] != "chn_x" {
		t.Fatalf("node saw trigger data %v", got)
	}
	if gotType != TriggerTypeOrgTool {
		t.Fatalf("node saw trigger type %q, want %q", gotType, TriggerTypeOrgTool)
	}
}
