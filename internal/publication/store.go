// Package publication stores published content, scoped to a profile.
package publication

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"
)

var ErrNotFound = errors.New("publication not found")
var ErrInvalidInput = errors.New("invalid publication input")

type Entry struct {
	ID             string   `json:"id"`
	ProfileID      string   `json:"profile_id"`
	Kind           string   `json:"kind"`
	Platform       string   `json:"platform"`
	Title          string   `json:"title"`
	Body           string   `json:"body"`
	URL            string   `json:"url"`
	RemoteID       string   `json:"remote_id"`
	ParentURL      string   `json:"parent_url"`
	Account        string   `json:"account"`
	WorkflowID     string   `json:"workflow_id"`
	ExecutionID    string   `json:"execution_id"`
	NodeID         string   `json:"node_id"`
	AgentID        string   `json:"agent_id"`
	OrgID          string   `json:"org_id"`
	RoleID         string   `json:"role_id"`
	PublishedAt    string   `json:"published_at"`
	RecordedAt     string   `json:"recorded_at"`
	IdempotencyKey string   `json:"idempotency_key"`
	Media          []string `json:"media"`
}
type Filter struct {
	Search     string `json:"search"`
	Platform   string `json:"platform"`
	Kind       string `json:"kind"`
	WorkflowID string `json:"workflow_id"`
	AgentID    string `json:"agent_id"`
	Since      string `json:"since"`
	Until      string `json:"until"`
	Limit      int    `json:"limit"`
	Offset     int    `json:"offset"`
	// Cursor continues after the last entry of a previous page (see ListPage); it excludes Offset.
	Cursor string `json:"cursor"`
}
type Store struct {
	db        *sql.DB
	profileID string
}

func NewStore(db *sql.DB, profileID string) *Store { return &Store{db: db, profileID: profileID} }
func invalid(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalidInput, fmt.Sprintf(format, args...))
}
func date(value string) (string, error) {
	t, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		t, err = time.Parse("2006-01-02", value)
	}
	if err != nil {
		return "", invalid("date must be RFC3339 or YYYY-MM-DD")
	}
	return t.UTC().Format(time.RFC3339Nano), nil
}

// tsLayout is the fixed-width UTC form stored in published_ts; it sorts as text.
const tsLayout = "2006-01-02T15:04:05.000Z"

func sortTS(t time.Time) string { return t.UTC().Format(tsLayout) }

func parseTS(value string) time.Time {
	t, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return time.Time{}
	}
	return t
}

// encodeCursor takes ts verbatim from the row's stored published_ts column, so
// the keyset comparison can never disagree with how the row was ordered.
// published_ts is sortTS(parseTS(published_at)); an unparseable published_at is
// stored as the zero time, "0001-01-01T00:00:00.000Z" (migration 064 does the same).
func encodeCursor(ts, id string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(ts + "|" + id))
}

func decodeCursor(c string) (ts, id string, err error) {
	raw, err := base64.RawURLEncoding.DecodeString(c)
	parts := strings.SplitN(string(raw), "|", 2)
	if err != nil || len(parts) != 2 || parts[1] == "" {
		return "", "", invalid("cursor is not valid")
	}
	return parts[0], parts[1], nil
}

const columns = "id, profile_id, kind, platform, title, body, url, remote_id, parent_url, account, workflow_id, execution_id, node_id, agent_id, org_id, role_id, published_at, recorded_at, idempotency_key, media"

type scanner interface{ Scan(...any) error }

func scan(row scanner) (*Entry, error) {
	var e Entry
	var media string
	err := row.Scan(&e.ID, &e.ProfileID, &e.Kind, &e.Platform, &e.Title, &e.Body, &e.URL, &e.RemoteID, &e.ParentURL, &e.Account, &e.WorkflowID, &e.ExecutionID, &e.NodeID, &e.AgentID, &e.OrgID, &e.RoleID, &e.PublishedAt, &e.RecordedAt, &e.IdempotencyKey, &media)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if err = json.Unmarshal([]byte(media), &e.Media); err != nil {
		return nil, err
	}
	if e.Media == nil {
		e.Media = []string{}
	}
	return &e, nil
}
func (s *Store) Get(ctx context.Context, id string) (*Entry, error) {
	if s.db == nil {
		return nil, errors.New("publication database unavailable")
	}
	return scan(s.db.QueryRowContext(ctx, "SELECT "+columns+" FROM publications WHERE profile_id=? AND id=?", s.profileID, id))
}
func (s *Store) Register(ctx context.Context, e Entry) (*Entry, error) {
	if s.db == nil {
		return nil, errors.New("publication database unavailable")
	}
	for i, raw := range []string{e.URL, e.ParentURL} {
		if i == 1 && raw != "" && (!strings.ContainsAny(raw, ":/\r\n\t ") || strings.HasPrefix(raw, "urn:") || strings.HasPrefix(raw, "at://")) {
			continue
		}
		if raw != "" {
			u, err := url.Parse(raw)
			if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil {
				return nil, invalid("publication URLs must be HTTP(S) URLs without credentials")
			}
		}
	}
	e.ProfileID = s.profileID
	e.Platform = strings.ToLower(strings.TrimSpace(e.Platform))
	e.Kind = strings.ToLower(strings.TrimSpace(e.Kind))
	if e.Platform == "" || e.Kind == "" {
		return nil, invalid("platform and kind are required")
	}
	switch e.Kind {
	case "post", "comment", "reply", "article", "video", "other":
	default:
		return nil, invalid("unsupported kind %q", e.Kind)
	}
	if strings.TrimSpace(e.Body) == "" && strings.TrimSpace(e.Title) == "" && strings.TrimSpace(e.URL) == "" && len(e.Media) == 0 {
		return nil, invalid("body, title, url or media is required")
	}
	var err error
	if e.PublishedAt == "" {
		e.PublishedAt = time.Now().UTC().Format(time.RFC3339Nano)
	} else if e.PublishedAt, err = date(e.PublishedAt); err != nil {
		return nil, err
	}
	e.RecordedAt = time.Now().UTC().Format(time.RFC3339Nano)
	e.ID = "pub-" + uuid.NewString()
	if e.Media == nil {
		e.Media = []string{}
	}
	media, err := json.Marshal(e.Media)
	if err != nil {
		return nil, err
	}
	result, err := s.db.ExecContext(ctx, "INSERT INTO publications ("+columns+", published_ts) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT DO NOTHING", e.ID, e.ProfileID, e.Kind, e.Platform, e.Title, e.Body, e.URL, e.RemoteID, e.ParentURL, e.Account, e.WorkflowID, e.ExecutionID, e.NodeID, e.AgentID, e.OrgID, e.RoleID, e.PublishedAt, e.RecordedAt, e.IdempotencyKey, string(media), sortTS(parseTS(e.PublishedAt)))
	if err != nil {
		return nil, err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return nil, err
	}
	if n > 0 {
		return &e, nil
	}
	// A duplicate returns the original record, keeping its content and timestamps.
	if e.IdempotencyKey != "" {
		existing, err := scan(s.db.QueryRowContext(ctx, "SELECT "+columns+" FROM publications WHERE profile_id=? AND idempotency_key=?", s.profileID, e.IdempotencyKey))
		if err == nil {
			return existing, nil
		}
		if !errors.Is(err, ErrNotFound) {
			return nil, err
		}
	}
	if e.RemoteID != "" {
		return scan(s.db.QueryRowContext(ctx, "SELECT "+columns+" FROM publications WHERE profile_id=? AND platform=? AND account=? AND kind=? AND remote_id=?", s.profileID, e.Platform, e.Account, e.Kind, e.RemoteID))
	}
	if e.URL != "" {
		return scan(s.db.QueryRowContext(ctx, "SELECT "+columns+" FROM publications WHERE profile_id=? AND platform=? AND account=? AND kind=? AND url=? AND remote_id=''", s.profileID, e.Platform, e.Account, e.Kind, e.URL))
	}
	return nil, fmt.Errorf("publication registration conflict")
}

// List returns one page; see ListPage for keyset paging.
func (s *Store) List(ctx context.Context, f Filter) ([]Entry, error) {
	entries, _, err := s.ListPage(ctx, f)
	return entries, err
}

// ListPage returns one page, newest first, and the cursor of the next page
// ("" on the last one). Pass it as Filter.Cursor; offsets still work without it.
func (s *Store) ListPage(ctx context.Context, f Filter) ([]Entry, string, error) {
	if s.db == nil {
		return nil, "", errors.New("publication database unavailable")
	}
	if f.Limit == 0 {
		f.Limit = 50
	}
	if f.Limit < 1 || f.Limit > 1000 || f.Offset < 0 {
		return nil, "", invalid("limit must be 1–1000 and offset nonnegative")
	}
	if f.Cursor != "" && f.Offset != 0 {
		return nil, "", invalid("cursor and offset cannot be combined")
	}
	where := []string{"profile_id=?"}
	args := []any{s.profileID}
	if f.Cursor != "" {
		ts, id, err := decodeCursor(f.Cursor)
		if err != nil {
			return nil, "", err
		}
		where = append(where, "(published_ts < ? OR (published_ts = ? AND id < ?))")
		args = append(args, ts, ts, id)
	}
	for _, v := range []struct{ col, value string }{{"platform", strings.ToLower(f.Platform)}, {"kind", strings.ToLower(f.Kind)}, {"workflow_id", f.WorkflowID}, {"agent_id", f.AgentID}} {
		if v.value != "" {
			where = append(where, v.col+"=?")
			args = append(args, v.value)
		}
	}
	var since, until string
	for _, v := range []struct{ op, value string }{{">=", f.Since}, {"<=", f.Until}} {
		if v.value != "" {
			d, err := date(v.value)
			if err != nil {
				return nil, "", err
			}
			if v.op == ">=" {
				since = d
			} else {
				until = d
			}
			op := v.op
			if v.op == "<=" && len(v.value) == 10 {
				t, _ := time.Parse("2006-01-02", v.value)
				d = t.AddDate(0, 0, 1).Format(time.RFC3339Nano)
				op = "<"
				until = d
			}
			where = append(where, "published_ts "+op+" ?")
			args = append(args, sortTS(parseTS(d)))
		}
	}
	if since != "" && until != "" {
		a, _ := time.Parse(time.RFC3339Nano, since)
		b, _ := time.Parse(time.RFC3339Nano, until)
		if a.After(b) {
			return nil, "", invalid("since must not follow until")
		}
	}
	if f.Search != "" {
		where = append(where, "(instr(lower(title),lower(?))>0 OR instr(lower(body),lower(?))>0 OR instr(lower(url),lower(?))>0 OR instr(lower(account),lower(?))>0)")
		for i := 0; i < 4; i++ {
			args = append(args, f.Search)
		}
	}
	// One extra row says whether another page follows.
	args = append(args, f.Limit+1, f.Offset)
	rows, err := s.db.QueryContext(ctx, "SELECT "+columns+" FROM publications WHERE "+strings.Join(where, " AND ")+" ORDER BY published_ts DESC, id DESC LIMIT ? OFFSET ?", args...)
	if err != nil {
		return nil, "", err
	}
	defer rows.Close()
	entries := []Entry{}
	for rows.Next() {
		e, err := scan(rows)
		if err != nil {
			return nil, "", err
		}
		entries = append(entries, *e)
	}
	if err := rows.Err(); err != nil {
		return nil, "", err
	}
	next := ""
	if len(entries) > f.Limit {
		entries = entries[:f.Limit]
		last := entries[f.Limit-1]
		var ts string
		if err := s.db.QueryRowContext(ctx, "SELECT published_ts FROM publications WHERE profile_id=? AND id=?", s.profileID, last.ID).Scan(&ts); err != nil {
			return nil, "", err
		}
		next = encodeCursor(ts, last.ID)
	}
	return entries, next, nil
}

// Delete removes one publication of this profile.
func (s *Store) Delete(ctx context.Context, id string) error {
	if s.db == nil {
		return errors.New("publication database unavailable")
	}
	res, err := s.db.ExecContext(ctx, "DELETE FROM publications WHERE profile_id=? AND id=?", s.profileID, id)
	if err != nil {
		return err
	}
	if n, err := res.RowsAffected(); err != nil {
		return err
	} else if n == 0 {
		return ErrNotFound
	}
	return nil
}
func (s *Store) Stats(ctx context.Context) (map[string]interface{}, error) {
	if s.db == nil {
		return nil, errors.New("publication database unavailable")
	}
	var total int
	var latest sql.NullString
	if err := s.db.QueryRowContext(ctx, "SELECT count(*), (SELECT published_at FROM publications WHERE profile_id=? ORDER BY published_ts DESC, id DESC LIMIT 1) FROM publications WHERE profile_id=?", s.profileID, s.profileID).Scan(&total, &latest); err != nil {
		return nil, err
	}
	result := map[string]interface{}{"total": total, "latest_published_at": latest.String}
	for _, col := range []string{"platform", "kind"} {
		rows, err := s.db.QueryContext(ctx, "SELECT "+col+", count(*) FROM publications WHERE profile_id=? GROUP BY "+col, s.profileID)
		if err != nil {
			return nil, err
		}
		counts := map[string]int{}
		for rows.Next() {
			var key string
			var n int
			if err := rows.Scan(&key, &n); err != nil {
				rows.Close()
				return nil, err
			}
			counts[key] = n
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return nil, err
		}
		result["by_"+col] = counts
	}
	return result, nil
}
