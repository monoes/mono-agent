package agent

import (
	"context"
	"testing"

	"github.com/monoes/mono-agent/internal/monomind"
	"github.com/monoes/mono-agent/internal/monomind/sandboxtest"
	"github.com/monoes/mono-agent/internal/workflow"
)

// agent.ask turns ask for the sandbox, in the shared agent-ask workspace,
// only when monomind advertises it; the item records the verdict.
func TestAskNodeSandbox(t *testing.T) {
	for _, advertise := range []bool{true, false} {
		argsLog := sandboxtest.Install(t, advertise, "hello")
		out, err := (&AskNode{}).Execute(context.Background(), workflow.NodeInput{
			Items: []workflow.Item{{JSON: map[string]interface{}{}}},
		}, map[string]interface{}{"runtime": "codex", "prompt": "say hi"})
		if err != nil {
			t.Fatalf("advertise=%v: %v", advertise, err)
		}
		sandboxtest.Check(t, argsLog, advertise, sandboxtest.Workspace(monomind.WorkspaceAgentAsk))
		want := monomind.SandboxStatusNeedsMonomind
		if advertise {
			want = monomind.SandboxStatusSandboxed
		}
		if got := out[0].Items[0].JSON["_agent_sandbox"]; got != want {
			t.Errorf("advertise=%v: _agent_sandbox = %v, want %s", advertise, got, want)
		}
	}
}
