package orggrant

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
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
		from, ok, err := s.raiseTier(ctx, g.ID)
		if err != nil {
			return raised, err
		}
		if !ok {
			continue
		}
		raised = append(raised, TierRaise{
			GrantID: g.ID, Profile: g.ProfileID, Org: g.OrgName, Role: g.RoleID, Alias: t.Alias,
			From: from, To: orgdesign.TierIrreversible, Outbound: outbound,
		})
	}
	return raised, nil
}

// raiseTier sets a live grant's stored tier to irreversible with a
// compare-and-set on tools_json, so a concurrent change to the grant's
// other fields (an `org grant` limits update) is re-read and kept, not
// overwritten. It returns the tier it replaced, and false when the grant is
// gone or already irreversible.
func (s *Store) raiseTier(ctx context.Context, grantID string) (string, bool, error) {
	for attempt := 0; attempt < 5; attempt++ {
		var old string
		err := s.db.QueryRowContext(ctx,
			`SELECT tools_json FROM org_grants WHERE id = ? AND revoked_at IS NULL`, grantID).Scan(&old)
		if errors.Is(err, sql.ErrNoRows) {
			return "", false, nil
		}
		if err != nil {
			return "", false, fmt.Errorf("orggrant: raise tier: %w", err)
		}
		var tools []Tool
		if err := json.Unmarshal([]byte(old), &tools); err != nil || len(tools) != 1 {
			return "", false, fmt.Errorf("orggrant: raise tier: grant %s: unreadable tools", grantID)
		}
		from := tools[0].Tier
		if from == orgdesign.TierIrreversible || !orgdesign.ValidTier(from) {
			return "", false, nil
		}
		tools[0].Tier = orgdesign.TierIrreversible
		next, err := json.Marshal(tools)
		if err != nil {
			return "", false, err
		}
		res, err := s.db.ExecContext(ctx,
			`UPDATE org_grants SET tools_json = ?, updated_at = ? WHERE id = ? AND revoked_at IS NULL AND tools_json = ?`,
			string(next), nowString(s.now()), grantID, old)
		if err != nil {
			return "", false, fmt.Errorf("orggrant: raise tier: %w", err)
		}
		if n, _ := res.RowsAffected(); n == 1 {
			return from, true, nil
		}
	}
	return "", false, fmt.Errorf("orggrant: raise tier: grant %s kept changing", grantID)
}
