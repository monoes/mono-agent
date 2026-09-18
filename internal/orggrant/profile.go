package orggrant

import (
	"context"
	"fmt"
	"strings"
)

// ProfileRevocation counts what a profile's orgs still hold (ProfileFootprint)
// or what RevokeProfile took away.
type ProfileRevocation struct {
	Grants      int `json:"grants"`      // live org_grants rows
	Endpoints   int `json:"endpoints"`   // usable org_endpoints rows (live or in a rotation grace window)
	Autonomy    int `json:"autonomy"`    // org_autonomy rows
	Delegations int `json:"delegations"` // pending org_delegations rows
}

// profileScopes are the four statements RevokeProfile runs and
// ProfileFootprint counts, kept side by side so the preview and the revoke
// can never disagree about what "held by the profile" means. Every revoke
// binds ?1 = now and ?2 = the profile id; a count binds the profile id
// alone, or (now, profile id) when usesAt says it names usableEndpoint.
var profileScopes = []struct {
	count  string
	revoke string
	usesAt bool
	field  func(*ProfileRevocation) *int
}{
	{
		count:  `SELECT COUNT(*) FROM org_grants WHERE profile_id = ? AND revoked_at IS NULL`,
		revoke: `UPDATE org_grants SET revoked_at = ?1, updated_at = ?1 WHERE profile_id = ?2 AND revoked_at IS NULL`,
		field:  func(r *ProfileRevocation) *int { return &r.Grants },
	},
	{
		count:  `SELECT COUNT(*) FROM org_endpoints WHERE ` + usableEndpoint + ` AND profile_id = ?`,
		revoke: `UPDATE org_endpoints SET revoked_at = ?1 WHERE (revoked_at IS NULL OR revoked_at > ?1) AND profile_id = ?2`,
		usesAt: true,
		field:  func(r *ProfileRevocation) *int { return &r.Endpoints },
	},
	{
		count:  `SELECT COUNT(*) FROM org_autonomy WHERE profile_id = ?`,
		revoke: `DELETE FROM org_autonomy WHERE profile_id = ?2`,
		field:  func(r *ProfileRevocation) *int { return &r.Autonomy },
	},
	{
		count:  `SELECT COUNT(*) FROM org_delegations WHERE profile_id = ? AND status = 'pending'`,
		revoke: `UPDATE org_delegations SET status = 'expired', resolved_at = ?1 WHERE profile_id = ?2 AND status = 'pending'`,
		field:  func(r *ProfileRevocation) *int { return &r.Delegations },
	},
}

func requireProfileID(profileID string) error {
	if strings.TrimSpace(profileID) == "" {
		return fmt.Errorf("orggrant: profile id is required")
	}
	return nil
}

// ProfileFootprint counts what RevokeProfile would take away, without
// changing anything.
func (s *Store) ProfileFootprint(ctx context.Context, profileID string) (ProfileRevocation, error) {
	var out ProfileRevocation
	if err := requireProfileID(profileID); err != nil {
		return out, err
	}
	now := nowString(s.now())
	for _, sc := range profileScopes {
		args := []interface{}{profileID}
		if sc.usesAt {
			args = []interface{}{now, profileID}
		}
		if err := s.db.QueryRowContext(ctx, sc.count, args...).Scan(sc.field(&out)); err != nil {
			return ProfileRevocation{}, fmt.Errorf("orggrant: profile footprint: %w", err)
		}
	}
	return out, nil
}

// RevokeProfile is the enforcement half of deleting a profile (C-24): in one
// transaction it revokes every live grant and every usable endpoint of the
// profile (a rotated-away id inside its grace window included, so a leaked
// id stops starting runs at once), drops its autonomy rows so a profile
// re-created under the same id starts at mid again, and expires its pending
// delegations. The audit ledgers (org_decisions, org_bridge_calls) and
// org_asks stay, for the same reasons RevokeOrg keeps them. It never
// touches files: stopping the folder's `org serve` and removing the folder
// are the caller's job.
func (s *Store) RevokeProfile(ctx context.Context, profileID string) (ProfileRevocation, error) {
	var out ProfileRevocation
	if err := requireProfileID(profileID); err != nil {
		return out, err
	}
	now := nowString(s.now())
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return out, err
	}
	defer tx.Rollback()
	for _, sc := range profileScopes {
		res, err := tx.ExecContext(ctx, sc.revoke, now, profileID)
		if err != nil {
			return ProfileRevocation{}, fmt.Errorf("orggrant: revoke profile: %w", err)
		}
		n, err := res.RowsAffected()
		if err != nil {
			return ProfileRevocation{}, err
		}
		*sc.field(&out) = int(n)
	}
	if err := tx.Commit(); err != nil {
		return ProfileRevocation{}, err
	}
	return out, nil
}
