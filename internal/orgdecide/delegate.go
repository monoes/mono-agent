package orgdecide

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/monoes/mono-agent/internal/orgdesign"
	"github.com/monoes/mono-agent/internal/orggrant"
)

// Decision tools a boss or parent decider role holds (plan §7.7).
const (
	ToolDecisionList    = "decision_list"
	ToolDecisionResolve = "decision_resolve"
)

// Delegation statuses.
const (
	DelegationPending  = "pending"
	DelegationResolved = "resolved"
	DelegationExpired  = "expired"
)

// Delegation is an item handed to a decider role.
type Delegation struct {
	ID          string    `json:"id"`
	ProfileID   string    `json:"-"`
	OrgName     string    `json:"org"`
	Item        Item      `json:"item"`
	Level       string    `json:"level"`
	DeciderOrg  string    `json:"-"`
	DeciderRole string    `json:"-"`
	Status      string    `json:"status"`
	CreatedAt   time.Time `json:"created_at"`
	DeadlineAt  time.Time `json:"deadline_at"`
}

// storedItem keeps the fields json:"-" hides on Item.
type storedItem struct {
	Item
	Action    string `json:"action"`
	RequestID string `json:"request_id"`
	Text      string `json:"text"`
	Name      string `json:"name"`
	Hash      string `json:"hash"`
}

func encodeItem(it Item) string {
	b, _ := json.Marshal(storedItem{Item: it, Action: it.Action, RequestID: it.RequestID, Text: it.Text, Name: it.Name, Hash: it.Hash})
	return string(b)
}

func decodeItem(s string) Item {
	var si storedItem
	_ = json.Unmarshal([]byte(s), &si)
	it := si.Item
	it.Action, it.RequestID, it.Text, it.Name, it.Hash = si.Action, si.RequestID, si.Text, si.Name, si.Hash
	return it
}

// Delegate records an item handed to deciderOrg:deciderRole.
func (s *Store) Delegate(ctx context.Context, profileID, org, level, deciderOrg, deciderRole string, it Item, deadline time.Time) (*Delegation, error) {
	d := &Delegation{ID: "dec_" + strings.ReplaceAll(uuid.NewString(), "-", "")[:20], ProfileID: profileID, OrgName: org, Item: it,
		Level: level, DeciderOrg: deciderOrg, DeciderRole: deciderRole, Status: DelegationPending, CreatedAt: s.now().UTC(), DeadlineAt: deadline.UTC()}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO org_delegations (id, profile_id, org_name, item_kind, item_ref, item_json, level, decider_org, decider_role, status, created_at, deadline_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, 'pending', ?, ?)`,
		d.ID, profileID, org, it.Kind, it.Ref, encodeItem(it), level, deciderOrg, deciderRole,
		d.CreatedAt.Format(time.RFC3339Nano), d.DeadlineAt.Format(time.RFC3339Nano))
	if err != nil {
		return nil, fmt.Errorf("orgdecide: delegate: %w", err)
	}
	return d, nil
}

const delegationCols = `id, profile_id, org_name, item_json, level, decider_org, decider_role, status, created_at, deadline_at`

func scanDelegation(sc interface{ Scan(...interface{}) error }) (*Delegation, error) {
	var d Delegation
	var item, created, deadline string
	if err := sc.Scan(&d.ID, &d.ProfileID, &d.OrgName, &item, &d.Level, &d.DeciderOrg, &d.DeciderRole, &d.Status, &created, &deadline); err != nil {
		return nil, err
	}
	d.Item = decodeItem(item)
	d.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
	d.DeadlineAt, _ = time.Parse(time.RFC3339Nano, deadline)
	return &d, nil
}

func (s *Store) queryDelegations(ctx context.Context, q string, args ...interface{}) ([]Delegation, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+delegationCols+` FROM org_delegations WHERE `+q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Delegation{}
	for rows.Next() {
		d, err := scanDelegation(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *d)
	}
	return out, rows.Err()
}

// PendingFor lists items waiting on a decider role.
func (s *Store) PendingFor(ctx context.Context, profileID, deciderOrg, deciderRole string) ([]Delegation, error) {
	return s.queryDelegations(ctx, `profile_id = ? AND decider_org = ? AND decider_role = ? AND status = 'pending' ORDER BY created_at`, profileID, deciderOrg, deciderRole)
}

// PendingDelegations lists every pending delegation of a profile.
func (s *Store) PendingDelegations(ctx context.Context, profileID string) ([]Delegation, error) {
	return s.queryDelegations(ctx, `profile_id = ? AND status = 'pending' ORDER BY created_at`, profileID)
}

// GetDelegation returns one delegation, or nil.
func (s *Store) GetDelegation(ctx context.Context, id string) (*Delegation, error) {
	d, err := scanDelegation(s.db.QueryRowContext(ctx, `SELECT `+delegationCols+` FROM org_delegations WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return d, err
}

// CloseDelegation marks a pending delegation resolved or expired; it
// reports false when it was no longer pending (another resolver won).
func (s *Store) CloseDelegation(ctx context.Context, id, status string) (bool, error) {
	res, err := s.db.ExecContext(ctx, `UPDATE org_delegations SET status = ?, resolved_at = ? WHERE id = ? AND status = 'pending'`,
		status, s.now().UTC().Format(time.RFC3339Nano), id)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n == 1, nil
}

// DeciderRole resolves who decides for an org with a boss or parent
// decider: the org's root role, or the Initiator (root role) of the holding
// org that lists it as a child.
func DeciderRole(a *Autonomy, doc *orgdesign.Doc, all []*orgdesign.Doc) (org, role string, err error) {
	switch a.Decider.Kind {
	case orgdesign.DeciderBoss:
		if doc == nil {
			return "", "", errors.New("org file unreadable")
		}
		r, ok := doc.RootRole()
		if !ok {
			return "", "", errors.New("org has no single root role")
		}
		return doc.Name, r.ID, nil
	case orgdesign.DeciderParent:
		owner := orgdesign.HoldingOwner(all, a.OrgName)
		for _, d := range all {
			if d.Name == owner {
				if r, ok := d.RootRole(); ok {
					return d.Name, r.ID, nil
				}
			}
		}
		return "", "", errors.New("no holding org lists this org as a child")
	}
	return "", "", fmt.Errorf("decider %q is not a role", a.Decider.Kind)
}

// GrantDecisionTools gives a decider role decision_list/decision_resolve
// over org, merged into its org-tool grant. Called by the explicit
// `org autonomy set --decider boss|parent` command only.
func GrantDecisionTools(ctx context.Context, grants *orggrant.Store, profileID, deciderOrg, deciderRole, org string) error {
	_, err := grants.MergeOrgTools(ctx, profileID, deciderOrg, deciderRole, []orggrant.OrgTool{
		{Tool: ToolDecisionList, Orgs: []string{org}},
		{Tool: ToolDecisionResolve, Orgs: []string{org}},
	})
	return err
}

func containsString(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}
