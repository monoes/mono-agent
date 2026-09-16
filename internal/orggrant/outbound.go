package orggrant

import (
	"sort"
	"strings"

	"github.com/monoes/mono-agent/internal/orgdesign"
	"github.com/monoes/mono-agent/internal/workflow"
)

// OutboundNodes lists the nodes of wf that act on the outside world:
// messaging (comm.*, except reads), third-party services (service.*),
// social actions (action.*, instagram.*), non-GET HTTP requests, remote
// shells and file transfer, and local command execution. A grant to a
// workflow with any of them defaults to approval "required" and tier
// irreversible (C-34). Deliberately over-inclusive: a false positive costs
// one extra decision, a false negative an unreviewed side effect.
func OutboundNodes(wf *workflow.Workflow) []string {
	if wf == nil {
		return nil
	}
	var out []string
	for _, n := range wf.Nodes {
		if n.Disabled {
			continue
		}
		if isOutboundNode(n) {
			label := n.Type
			if n.Name != "" {
				label = n.Name + " (" + n.Type + ")"
			}
			out = append(out, label)
		}
	}
	sort.Strings(out)
	return out
}

func isOutboundNode(n workflow.WorkflowNode) bool {
	t := n.Type
	switch {
	case strings.HasPrefix(t, "comm."):
		return !strings.HasSuffix(t, "_read")
	case strings.HasPrefix(t, "service."),
		strings.HasPrefix(t, "action."),
		strings.HasPrefix(t, "instagram."):
		return true
	case t == "http.ftp", t == "http.ssh", t == "system.execute_command":
		return true
	case t == "http.request":
		method, _ := n.Config["method"].(string)
		return method != "" && !strings.EqualFold(method, "GET") && !strings.EqualFold(method, "HEAD")
	}
	return false
}

// GrantTier is the decision tier a call to a granted automation gets when
// it needs a decision (plan §7.7): irreversible with outbound nodes,
// consequential otherwise.
func GrantTier(outbound []string) string {
	if len(outbound) > 0 {
		return orgdesign.TierIrreversible
	}
	return orgdesign.TierConsequential
}
