package orgbridge

import (
	"context"
	"encoding/json"

	"github.com/monoes/mono-agent/internal/orgdesign"
	"github.com/monoes/mono-agent/internal/workflow"
)

// LimitsFor reads run_config.max_hops / max_repeats from an org file. The
// org JSON is writable by any role whose fileWrite reaches .monomind/, so
// these are requests, not guarantees: Limits.withDefaults clamps them to
// MaxHopsCeiling / MaxRepeatsCeiling before Admit uses them. An org that
// cannot be read gives the defaults.
func LimitsFor(root, org string) Limits {
	var lim Limits
	if root == "" || !orgdesign.ValidOrgName(org) {
		return lim
	}
	doc, err := orgdesign.Load(root, org)
	if err != nil {
		return lim
	}
	if v, ok := doc.RunConfig["max_hops"]; ok {
		_ = json.Unmarshal(v, &lim.MaxHops)
	}
	if v, ok := doc.RunConfig["max_repeats"]; ok {
		_ = json.Unmarshal(v, &lim.MaxRepeats)
	}
	return lim
}

// eventItems renders ev as a trigger.org run's trigger data and admits the
// run through the ledger (org_event, or event_start on a fresh chain), so
// the run's chain and hop are the ledger's, not only the event's. It reports false when the run
// must not start: the ledger refused it (the chain is at its hop limit, or
// the workflow is past TriggerRepeats) or could not record it (fail closed,
// as the webhook server does).
//
// Without this a trigger.org run took its hop from the event (the role's
// current hop) and nothing recorded it, so a loop R → tool event → T →
// webhook → back to R by a path the ledger does not record ran at one hop
// forever: every return was a webhook_in at the same hop. Admitted, each
// round's T run starts above the previous round's webhook_in row.
//
// The chain is, in order:
//   - the event's own trace (a message's trace line, a granted call's tool
//     event; eventTrace);
//   - for any other tool event (Bash, file reads, …), the role's chain
//     monomind stamps into its data, but only when a run of this workflow
//     started that chain (Ledger.StartedChain): the role is on it because
//     of something this workflow's run did, so the event is a loop coming
//     back. An audit workflow on a role's tool events is not put on the
//     role's own chain, and does not climb it (#124);
//   - else a fresh chain, as a webhook run gets, so a loop that starts here
//     is counted from its second round.
func (s *TriggerSource) eventItems(ctx context.Context, profileID, root, workflowID string, ev Event) ([]workflow.Item, bool) {
	items := TriggerItems(ev)
	ledger := NewLedger(s.DB)
	tr, ok := eventTrace(ev)
	if !ok {
		if dt, dok := toolDataTrace(ev); dok {
			on, err := ledger.StartedChain(ctx, profileID, dt.ChainID, workflowID)
			if err != nil {
				s.logf("trigger.org: %s: workflow %s: %v", ev.Org, workflowID, err)
				return nil, false
			}
			if on {
				tr = dt
			}
		}
	}
	// A fresh chain is recorded as event_start, so StartedChain can tell it
	// from a run that continued someone else's chain: a granted call's
	// event can reach the ledger before the call itself does, and then an
	// audit workflow's org_event row would be the first on the role's chain.
	dir := DirOrgEvent
	if tr.ChainID == "" {
		dir = DirEventStart
	}
	adm, err := ledger.Admit(ctx, Call{
		ProfileID: profileID, Trace: tr, OriginOrg: ev.Org, Direction: dir,
		OrgName: ev.Org, WorkflowID: workflowID, RunID: ev.Run,
	}, LimitsFor(root, ev.Org))
	if err != nil {
		s.logf("trigger.org: %s: workflow %s: %v", ev.Org, workflowID, err)
		return nil, false
	}
	if !adm.OK() {
		s.logf("trigger.org: %s: workflow %s not started: %s", ev.Org, workflowID, adm.Reason)
		return nil, false
	}
	items[0].JSON["trace"] = map[string]interface{}{"chain_id": adm.Trace.ChainID, "hop": adm.Trace.Hop}
	return items, true
}

func (s *TriggerSource) logf(format string, args ...interface{}) {
	if s.Logf != nil {
		s.Logf(format, args...)
	}
}
