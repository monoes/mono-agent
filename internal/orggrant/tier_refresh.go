package orggrant

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/monoes/mono-agent/internal/orgdesign"
	"github.com/monoes/mono-agent/internal/workflow"
)

// FindingGrantTierRaised reports a grant whose stored tier was raised
// because its workflow gained an outbound node.
const FindingGrantTierRaised = "grant_tier_raised"

// TierRaise is one grant whose stored tier RaiseTiers raised.
type TierRaise struct {
	GrantID  string   `json:"grant_id"`
	Profile  string   `json:"profile"`
	Org      string   `json:"org"`
	Role     string   `json:"role"`
	Alias    string   `json:"alias"`
	From     string   `json:"from"`
	To       string   `json:"to"`
	Outbound []string `json:"outbound"`
}

func (r TierRaise) String() string {
	return fmt.Sprintf("raised the tier of %q (grant %s) from %s to %s: its workflow now has outbound nodes (%s)",
		r.Alias, r.GrantID, r.From, r.To, strings.Join(r.Outbound, ", "))
}

// WorkflowLoader reads a workflow as it is now; nil means it is gone.
type WorkflowLoader func(ctx context.Context, workflowID string) (*workflow.Workflow, error)

// RaiseTiers re-derives each automation grant's tier from its workflow as
// it is now and stores it when that is higher: a workflow that gained an
// outbound node makes its grants irreversible. It never lowers a tier —
// that takes a re-grant — and leaves a grant whose workflow cannot be read
// alone.
func (s *Store) RaiseTiers(ctx context.Context, grants []Grant, load WorkflowLoader) ([]TierRaise, error) {
	var raised []TierRaise
	for _, g := range grants {
		t := g.Automation()
		if t == nil || t.Tier == orgdesign.TierIrreversible || !orgdesign.ValidTier(t.Tier) {
			// An unknown stored tier already routes as irreversible.
			continue
		}
		wf, err := load(ctx, t.WorkflowID)
		if err != nil || wf == nil {
			continue
		}
		outbound := OutboundNodes(wf)
		if GrantTier(outbound) != orgdesign.TierIrreversible {
			continue
		}
		tool := *t
		tool.Tier = orgdesign.TierIrreversible
		toolsJSON, err := json.Marshal([]Tool{tool})
		if err != nil {
			return raised, err
		}
		if _, err := s.db.ExecContext(ctx,
			`UPDATE org_grants SET tools_json = ?, updated_at = ? WHERE id = ? AND revoked_at IS NULL`,
			string(toolsJSON), nowString(s.now()), g.ID); err != nil {
			return raised, fmt.Errorf("orggrant: raise tier: %w", err)
		}
		raised = append(raised, TierRaise{
			GrantID: g.ID, Profile: g.ProfileID, Org: g.OrgName, Role: g.RoleID, Alias: t.Alias,
			From: t.Tier, To: tool.Tier, Outbound: outbound,
		})
	}
	return raised, nil
}
