package agent

import (
	"context"
	"testing"

	"github.com/monoes/mono-agent/internal/monomind"
	"github.com/monoes/mono-agent/internal/monomind/sandboxtest"
	"github.com/monoes/mono-agent/internal/workflow"
)

// agent.ask turns ask for the sandbox, in the shared agent-ask workspace,
// through whichever path monomind offers; the item records the verdict.
func TestAskNodeSandbox(t *testing.T) {
	for _, m := range sandboxtest.Kinds {
		argsLog := sandboxtest.Install(t, m, "hello")
		out, err := (&AskNode{}).Execute(context.Background(), workflow.NodeInput{
			Items: []workflow.Item{{JSON: map[string]interface{}{}}},
		}, map[string]interface{}{"runtime": "codex", "prompt": "say hi"})
		if err != nil {
			t.Fatalf("%s monomind: %v", m, err)
		}
		sandboxtest.Check(t, argsLog, m, sandboxtest.Workspace(monomind.WorkspaceAgentAsk))
		want := sandboxtest.Status(m)
		if got := out[0].Items[0].JSON["_agent_sandbox"]; got != want {
			t.Errorf("%s monomind: _agent_sandbox = %v, want %s", m, got, want)
		}
	}
}
