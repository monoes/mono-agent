package orggrant

import (
	"context"
	"crypto/subtle"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// DefaultAPIAddr is where `monoagentcli daemon` serves the HTTP API and
// the endpoint receiver unless --api-addr says otherwise.
const DefaultAPIAddr = "127.0.0.1:9322"

// RotationGrace is how long a rotated-away endpoint id keeps working, so a
// delivery already in flight during rotation is not lost.
const RotationGrace = 5 * time.Minute

// EndpointRow is one org_endpoints row.
type EndpointRow struct {
	ID             string
	ProfileID      string
	OrgName        string
	RoleID         string
	WorkflowID     string
	CredentialFile string
	RotatedFrom    string
	CreatedAt      time.Time
	RevokedAt      *time.Time
}

// EndpointURL is the URL monomind POSTs to for id.
func EndpointURL(apiAddr, id string) string {
	if apiAddr == "" {
		apiAddr = DefaultAPIAddr
	}
	return "http://" + apiAddr + "/org-endpoint/" + id
}

// EndpointIDFromURL extracts the ep_ id from an endpoint URL, or "".
func EndpointIDFromURL(u string) string {
	i := strings.LastIndex(u, "/org-endpoint/")
	if i < 0 {
		return ""
	}
	id := strings.TrimSuffix(u[i+len("/org-endpoint/"):], "/")
	if !ValidEndpointID(id) {
		return ""
	}
	return id
}

const endpointCols = `id, profile_id, org_name, role_id, workflow_id, credential_file, rotated_from, created_at, revoked_at`

// usableEndpoint matches every row LookupEndpoint would still accept: a
// live one, or a rotated-away one whose grace window has not run out. Every
// revoke must cover both, or a leaked id keeps starting runs after its role
// or org is gone. Takes one bound parameter, the current time. The string
// comparison is safe here: a grace window is RotationGrace long, so a
// revoked_at still in the future differs from now in a fixed-width field
// well before RFC3339Nano's variable-length fraction.
const usableEndpoint = `(revoked_at IS NULL OR revoked_at > ?)`

func scanEndpoint(sc interface{ Scan(...interface{}) error }) (*EndpointRow, error) {
	var e EndpointRow
	var cred, rotated, revoked sql.NullString
	var created string
	if err := sc.Scan(&e.ID, &e.ProfileID, &e.OrgName, &e.RoleID, &e.WorkflowID, &cred, &rotated, &created, &revoked); err != nil {
		return nil, err
	}
	e.CredentialFile = cred.String
	e.RotatedFrom = rotated.String
	e.CreatedAt = parseTime(created)
	if revoked.Valid && revoked.String != "" {
		t := parseTime(revoked.String)
		e.RevokedAt = &t
	}
	return &e, nil
}

// CreateEndpoint registers a new live endpoint for an automation role,
// revoking any previous endpoint of that role — one still inside a rotation
// grace window included, since the new id replaces it outright.
func (s *Store) CreateEndpoint(ctx context.Context, profileID, org, role, workflowID string) (*EndpointRow, error) {
	if profileID == "" || org == "" || role == "" || workflowID == "" {
		return nil, fmt.Errorf("orggrant: profile, org, role, and workflow are required")
	}
	now := nowString(s.now())
	if _, err := s.db.ExecContext(ctx, `UPDATE org_endpoints SET revoked_at = ? WHERE profile_id = ? AND org_name = ? AND role_id = ? AND `+usableEndpoint, now, profileID, org, role, now); err != nil {
		return nil, err
	}
	id := NewEndpointID()
	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO org_endpoints (id, profile_id, org_name, role_id, workflow_id, created_at) VALUES (?, ?, ?, ?, ?, ?)`,
		id, profileID, org, role, workflowID, now); err != nil {
		return nil, fmt.Errorf("orggrant: insert endpoint: %w", err)
	}
	return s.GetEndpoint(ctx, id)
}

// RotateEndpoint replaces the role's live endpoint id with a new one. The
// old id is revoked with a RotationGrace window: LookupEndpoint still
// accepts it until then.
func (s *Store) RotateEndpoint(ctx context.Context, profileID, org, role string) (*EndpointRow, error) {
	cur, err := s.LiveEndpoint(ctx, profileID, org, role)
	if err != nil {
		return nil, err
	}
	now := s.now()
	id := NewEndpointID()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `UPDATE org_endpoints SET revoked_at = ? WHERE id = ?`, nowString(now.Add(RotationGrace)), cur.ID); err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO org_endpoints (id, profile_id, org_name, role_id, workflow_id, credential_file, rotated_from, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		id, cur.ProfileID, cur.OrgName, cur.RoleID, cur.WorkflowID, nullIfEmpty(cur.CredentialFile), cur.ID, nowString(now)); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return s.GetEndpoint(ctx, id)
}

func nullIfEmpty(s string) interface{} {
	if s == "" {
		return nil
	}
	return s
}

// GetEndpoint returns an endpoint row by id, revoked or not.
func (s *Store) GetEndpoint(ctx context.Context, id string) (*EndpointRow, error) {
	e, err := scanEndpoint(s.db.QueryRowContext(ctx, `SELECT `+endpointCols+` FROM org_endpoints WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return e, err
}

// LookupEndpoint authenticates an inbound endpoint id: it returns the row
// when id is live or still inside its rotation grace window. The final
// comparison is constant-time so response timing does not leak how much of
// a guessed id matched an indexed row.
func (s *Store) LookupEndpoint(ctx context.Context, id string) (*EndpointRow, error) {
	if !ValidEndpointID(id) {
		return nil, ErrNotFound
	}
	e, err := s.GetEndpoint(ctx, id)
	if err != nil {
		return nil, err
	}
	if subtle.ConstantTimeCompare([]byte(e.ID), []byte(id)) != 1 {
		return nil, ErrNotFound
	}
	if e.RevokedAt != nil && !e.RevokedAt.After(s.now()) {
		return nil, ErrNotFound
	}
	return e, nil
}

// LiveEndpoint returns the role's live endpoint (not in a rotation grace
// window).
func (s *Store) LiveEndpoint(ctx context.Context, profileID, org, role string) (*EndpointRow, error) {
	e, err := scanEndpoint(s.db.QueryRowContext(ctx,
		`SELECT `+endpointCols+` FROM org_endpoints WHERE profile_id = ? AND org_name = ? AND role_id = ? AND revoked_at IS NULL ORDER BY created_at DESC LIMIT 1`,
		profileID, org, role))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return e, err
}

// ListEndpoints returns live endpoints of an org.
func (s *Store) ListEndpoints(ctx context.Context, profileID, org string) ([]EndpointRow, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+endpointCols+` FROM org_endpoints WHERE profile_id = ? AND org_name = ? AND revoked_at IS NULL ORDER BY created_at`,
		profileID, org)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []EndpointRow
	for rows.Next() {
		e, err := scanEndpoint(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *e)
	}
	return out, rows.Err()
}

// EndpointsForWorkflow returns live endpoints (any profile) that run
// workflowID.
func (s *Store) EndpointsForWorkflow(ctx context.Context, workflowID string) ([]EndpointRow, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+endpointCols+` FROM org_endpoints WHERE workflow_id = ? AND revoked_at IS NULL ORDER BY created_at`, workflowID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []EndpointRow
	for rows.Next() {
		e, err := scanEndpoint(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *e)
	}
	return out, rows.Err()
}

// RevokeEndpoint revokes the role's endpoints immediately, a rotated-away
// one still inside its grace window included.
func (s *Store) RevokeEndpoint(ctx context.Context, profileID, org, role string) error {
	now := nowString(s.now())
	_, err := s.db.ExecContext(ctx, `UPDATE org_endpoints SET revoked_at = ? WHERE profile_id = ? AND org_name = ? AND role_id = ? AND `+usableEndpoint,
		now, profileID, org, role, now)
	return err
}
