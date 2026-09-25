package orgbridge

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"time"

	"github.com/monoes/mono-agent/internal/orggrant"
)

// Ask is one org.ask node waiting for (or holding) its reply.
type Ask struct {
	ID             string
	ProfileID      string
	OrgName        string
	RoleID         string // role asked
	EndpointRoleID string // automation role the question was sent from
	ExecutionID    string
	NodeID         string
	// Question is the text the org.ask node sent ("" for asks recorded
	// before migration 045). Only asks with a question are offered to the
	// Jev reply matcher.
	Question   string
	Status     string // waiting | replied | timed_out
	Reply      map[string]interface{}
	CreatedAt  time.Time
	DeadlineAt time.Time
	RepliedAt  *time.Time
}

// Ask statuses.
const (
	AskWaiting  = "waiting"
	AskReplied  = "replied"
	AskTimedOut = "timed_out"
)

// NewAskID returns "ask_" + 16 base32 chars.
func NewAskID() string { return "ask_" + orggrant.NewChainID()[4:20] }

// askRefRe finds an ask id in a reply's subject or body.
var askRefRe = regexp.MustCompile(`\bask:(ask_[a-z2-7]{16})\b`)

// AskRef extracts the ask id a reply refers to, from its subject then body.
func AskRef(subject, body string) string {
	for _, s := range []string{subject, body} {
		if m := askRefRe.FindStringSubmatch(s); m != nil {
			return m[1]
		}
	}
	return ""
}

// AskStore reads and writes org_asks.
type AskStore struct {
	db  *sql.DB
	now func() time.Time
}

// NewAskStore returns an AskStore over db.
func NewAskStore(db *sql.DB) *AskStore { return &AskStore{db: db, now: time.Now} }

// Create records a new waiting ask.
func (s *AskStore) Create(ctx context.Context, a Ask) error {
	now := s.now().UTC()
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO org_asks (id, profile_id, org_name, role_id, endpoint_role_id, execution_id, node_id, question, status, created_at, deadline_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, 'waiting', ?, ?)`,
		a.ID, a.ProfileID, a.OrgName, a.RoleID, a.EndpointRoleID, a.ExecutionID, a.NodeID,
		sql.NullString{String: a.Question, Valid: a.Question != ""},
		now.Format(time.RFC3339Nano), a.DeadlineAt.UTC().Format(time.RFC3339Nano))
	return err
}

const askCols = `id, profile_id, org_name, role_id, endpoint_role_id, execution_id, node_id, question, status, reply_json, created_at, deadline_at, replied_at`

func scanAsk(sc interface{ Scan(...interface{}) error }) (*Ask, error) {
	var a Ask
	var question, reply, replied sql.NullString
	var created, deadline string
	if err := sc.Scan(&a.ID, &a.ProfileID, &a.OrgName, &a.RoleID, &a.EndpointRoleID, &a.ExecutionID, &a.NodeID, &question, &a.Status, &reply, &created, &deadline, &replied); err != nil {
		return nil, err
	}
	a.Question = question.String
	if reply.Valid && reply.String != "" {
		_ = json.Unmarshal([]byte(reply.String), &a.Reply)
	}
	a.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
	a.DeadlineAt, _ = time.Parse(time.RFC3339Nano, deadline)
	if replied.Valid && replied.String != "" {
		t, _ := time.Parse(time.RFC3339Nano, replied.String)
		a.RepliedAt = &t
	}
	return &a, nil
}

// ForNode returns the most recent ask of an execution's node, or nil.
func (s *AskStore) ForNode(ctx context.Context, executionID, nodeID string) (*Ask, error) {
	a, err := scanAsk(s.db.QueryRowContext(ctx,
		`SELECT `+askCols+` FROM org_asks WHERE execution_id = ? AND node_id = ? ORDER BY created_at DESC LIMIT 1`,
		executionID, nodeID))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return a, err
}

// Get returns an ask by id, or nil.
func (s *AskStore) Get(ctx context.Context, id string) (*Ask, error) {
	a, err := scanAsk(s.db.QueryRowContext(ctx, `SELECT `+askCols+` FROM org_asks WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return a, err
}

// ListWaiting returns every waiting ask.
func (s *AskStore) ListWaiting(ctx context.Context) ([]Ask, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+askCols+` FROM org_asks WHERE status = 'waiting' ORDER BY created_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Ask
	for rows.Next() {
		a, err := scanAsk(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *a)
	}
	return out, rows.Err()
}

// ListWaitingFor returns up to limit waiting asks of one profile, org and
// automation role that recorded a question, most recent first — the
// candidates a token-less reply may answer.
func (s *AskStore) ListWaitingFor(ctx context.Context, profileID, org, endpointRoleID string, limit int) ([]Ask, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+askCols+` FROM org_asks
		WHERE status = 'waiting' AND profile_id = ? AND org_name = ? AND endpoint_role_id = ?
		  AND question IS NOT NULL AND question <> ''
		ORDER BY created_at DESC LIMIT ?`, profileID, org, endpointRoleID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Ask
	for rows.Next() {
		a, err := scanAsk(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *a)
	}
	return out, rows.Err()
}

// MarkReplied stores the reply of a waiting ask; it reports false when the
// ask was no longer waiting (a duplicate or late reply).
func (s *AskStore) MarkReplied(ctx context.Context, id string, reply map[string]interface{}) (bool, error) {
	b, err := json.Marshal(reply)
	if err != nil {
		return false, err
	}
	res, err := s.db.ExecContext(ctx,
		`UPDATE org_asks SET status = 'replied', reply_json = ?, replied_at = ? WHERE id = ? AND status = 'waiting'`,
		string(b), s.now().UTC().Format(time.RFC3339Nano), id)
	if err != nil {
		return false, fmt.Errorf("orgbridge: store ask reply: %w", err)
	}
	n, _ := res.RowsAffected()
	return n == 1, nil
}

// MarkTimedOut expires a waiting ask; it reports whether it was waiting.
func (s *AskStore) MarkTimedOut(ctx context.Context, id string) (bool, error) {
	res, err := s.db.ExecContext(ctx, `UPDATE org_asks SET status = 'timed_out' WHERE id = ? AND status = 'waiting'`, id)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n == 1, nil
}
