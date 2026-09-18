package workflow

import (
	"context"
	"testing"

	"github.com/rs/zerolog"

	"github.com/monoes/mono-agent/internal/fsconfine"
)

// rootProbeNode records the confinement root its context carries (C-46).
type rootProbeNode struct {
	root     *string
	confined *bool
}

func (rootProbeNode) Type() string { return "test.rootprobe" }
func (n rootProbeNode) Execute(ctx context.Context, in NodeInput, _ map[string]interface{}) ([]NodeOutput, error) {
	*n.root, *n.confined = fsconfine.Root(ctx)
	return []NodeOutput{{Handle: "main", Items: in.Items}}, nil
}

func runProbe(t *testing.T, triggerData map[string]interface{}) (string, bool) {
	t.Helper()
	var root string
	var confined bool
	reg := NewNodeTypeRegistry()
	reg.Register("test.rootprobe", func() NodeExecutor { return rootProbeNode{root: &root, confined: &confined} })
	wf := &Workflow{
		ID: "wf-confine",
		Nodes: []WorkflowNode{
			{ID: "t", WorkflowID: "wf-confine", Type: "trigger.manual", Name: "T"},
			{ID: "p", WorkflowID: "wf-confine", Type: "test.rootprobe", Name: "Probe"},
		},
		Connections: []WorkflowConnection{{SourceNodeID: "t", SourceHandle: "main", TargetNodeID: "p", TargetHandle: "main"}},
	}
	dag, err := BuildDAG(wf.Nodes, wf.Connections)
	if err != nil {
		t.Fatal(err)
	}
	exec := &WorkflowExecution{ID: "e-confine", WorkflowID: wf.ID, TriggerNodeID: "t", TriggerData: triggerData}
	if err := RunExecution(context.Background(), exec, wf, dag, reg, &stubStore{}, nil, NewExpressionEngine(), zerolog.Nop()); err != nil {
		t.Fatalf("RunExecution: %v", err)
	}
	return root, confined
}

func TestRunExecution_ConfinesNodesToOrgWorkdir(t *testing.T) {
	root, confined := runProbe(t, map[string]interface{}{
		"trigger_type": TriggerTypeOrgTool,
		"org":          map[string]interface{}{"name": "growth", "role": "writer", "workdir": "/srv/org/worktree"},
		"input":        map[string]interface{}{"path": "/etc/passwd"},
	})
	if !confined || root != "/srv/org/worktree" {
		t.Errorf("node context root = %q confined=%v, want /srv/org/worktree", root, confined)
	}
}

func TestRunExecution_NoOrgWorkdirLeavesNodesUnconfined(t *testing.T) {
	for name, td := range map[string]map[string]interface{}{
		"manual":           {},
		"org without path": {"org": map[string]interface{}{"name": "growth"}},
		"workdir in input": {"input": map[string]interface{}{"org": map[string]interface{}{"workdir": "/x"}}},
	} {
		if _, confined := runProbe(t, td); confined {
			t.Errorf("%s: node context is confined, want unconfined", name)
		}
	}
}
