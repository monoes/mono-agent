package tasks

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Store reads and writes boards. Every method takes the profile id.
type Store struct {
	db  *sql.DB
	now func() time.Time
}

// NewStore returns a Store over a migrated database.
func NewStore(db *sql.DB) *Store { return &Store{db: db, now: time.Now} }

// dbx is what *sql.DB and *sql.Conn both offer, so a helper runs in or out of
// a transaction.
type dbx interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

func (s *Store) stamp() string { return s.now().UTC().Format(timeFmt) }

// tx runs fn in a BEGIN IMMEDIATE transaction on a connection of its own: the
// write lock is taken first, so processes that read and then write never
// interleave (the pattern of vault.Register). An error from fn rolls back.
func (s *Store) tx(ctx context.Context, fn func(x dbx) error) error {
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return fmt.Errorf("tasks: connection: %w", err)
	}
	defer conn.Close()
	if _, err := conn.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		return fmt.Errorf("tasks: begin: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_, _ = conn.ExecContext(context.Background(), "ROLLBACK")
		}
	}()
	if err := fn(conn); err != nil {
		return err
	}
	if _, err := conn.ExecContext(ctx, "COMMIT"); err != nil {
		return fmt.Errorf("tasks: commit: %w", err)
	}
	committed = true
	return nil
}

// snapshot runs fn in one read transaction, so what it reads agrees.
func (s *Store) snapshot(ctx context.Context, fn func(x dbx) error) error {
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return fmt.Errorf("tasks: connection: %w", err)
	}
	defer conn.Close()
	if _, err := conn.ExecContext(ctx, "BEGIN"); err != nil {
		return fmt.Errorf("tasks: begin: %w", err)
	}
	defer func() { _, _ = conn.ExecContext(context.Background(), "ROLLBACK") }()
	return fn(conn)
}

// Profile returns the profile or an ErrInvalid ("unknown profile").
func (s *Store) Profile(ctx context.Context, profileID string) (Profile, error) {
	return s.profileOf(ctx, s.db, profileID)
}

func (s *Store) profileOf(ctx context.Context, x dbx, profileID string) (Profile, error) {
	var p Profile
	err := x.QueryRowContext(ctx, `SELECT id, name FROM profiles WHERE id = ?`, profileID).Scan(&p.ID, &p.Name)
	if errors.Is(err, sql.ErrNoRows) {
		return Profile{}, invalid("unknown profile %q", profileID)
	}
	if err != nil {
		return Profile{}, fmt.Errorf("tasks: reading the profile: %w", err)
	}
	return p, nil
}

// event writes one row of a task's history.
func (s *Store) event(ctx context.Context, x dbx, taskID int64, at, actor, kind, from, to, note string) error {
	_, err := x.ExecContext(ctx,
		`INSERT INTO task_events (task_id, at, actor, kind, from_status, to_status, note) VALUES (?,?,?,?,?,?,?)`,
		taskID, at, actor, kind, from, to, note)
	if err != nil {
		return fmt.Errorf("tasks: writing an event: %w", err)
	}
	return nil
}

// bump moves the profile's board revision; every write transaction does it once.
func (s *Store) bump(ctx context.Context, x dbx, profileID string) error {
	_, err := x.ExecContext(ctx,
		`INSERT INTO task_board_rev (profile_id, rev) VALUES (?, 1) ON CONFLICT(profile_id) DO UPDATE SET rev = rev + 1`, profileID)
	if err != nil {
		return fmt.Errorf("tasks: bumping the revision: %w", err)
	}
	return nil
}

// atTop says where a card new to a column goes: newest first where the order
// is chronological, at the end of a queue otherwise.
func atTop(st Status) bool { return st == StatusInbox || st == StatusReview || st == StatusDone }

// edgePosition is a position above every other card of the column (top) or
// below every one (bottom), ignoring the card except.
func (s *Store) edgePosition(ctx context.Context, x dbx, profileID string, status Status, top bool, except int64) (int64, error) {
	var lo, hi sql.NullInt64
	err := x.QueryRowContext(ctx,
		`SELECT MIN(position), MAX(position) FROM tasks WHERE profile_id = ? AND status = ? AND id <> ?`,
		profileID, string(status), except).Scan(&lo, &hi)
	if err != nil {
		return 0, fmt.Errorf("tasks: reading a column: %w", err)
	}
	switch {
	case !lo.Valid:
		return positionGap, nil
	case top:
		return lo.Int64 - positionGap, nil
	}
	return hi.Int64 + positionGap, nil
}

func placeholders(n int) string { return strings.TrimSuffix(strings.Repeat("?,", n), ",") }

const taskCols = `id, profile_id, title, notes, status, position, source_kind, source_url, source_title, source_app, claimed_by, claim_until, created_at, updated_at`

type rowScanner interface{ Scan(dest ...any) error }

// scanTask reads a row of taskCols. A claim is stale when its lease has run out.
func (s *Store) scanTask(row rowScanner) (Task, error) {
	var t Task
	var status, claimedBy, until, created, updated string
	if err := row.Scan(&t.ID, &t.ProfileID, &t.Title, &t.Notes, &status, &t.Position,
		&t.Source.Kind, &t.Source.URL, &t.Source.Title, &t.Source.App, &claimedBy, &until, &created, &updated); err != nil {
		return Task{}, err
	}
	t.Status = Status(status)
	t.CreatedAt, _ = time.Parse(timeFmt, created)
	t.UpdatedAt, _ = time.Parse(timeFmt, updated)
	if claimedBy != "" {
		u, _ := time.Parse(timeFmt, until)
		t.Claim = &Claim{By: claimedBy, Until: u, Stale: !u.After(s.now())}
	}
	return t, nil
}

// getTx reads one task of the profile, with its last event.
func (s *Store) getTx(ctx context.Context, x dbx, profileID string, id int64) (Task, error) {
	t, err := s.scanTask(x.QueryRowContext(ctx, `SELECT `+taskCols+` FROM tasks WHERE profile_id = ? AND id = ?`, profileID, id))
	if errors.Is(err, sql.ErrNoRows) {
		return Task{}, fmt.Errorf("%w: #%d", ErrNotFound, id)
	}
	if err != nil {
		return Task{}, fmt.Errorf("tasks: reading #%d: %w", id, err)
	}
	ts := []Task{t}
	if err := s.attachLast(ctx, x, ts); err != nil {
		return Task{}, err
	}
	return ts[0], nil
}

// attachLast fills LastEvent of every task with one query.
func (s *Store) attachLast(ctx context.Context, x dbx, ts []Task) error {
	if len(ts) == 0 {
		return nil
	}
	index := make(map[int64]int, len(ts))
	args := make([]any, 0, len(ts))
	for i, t := range ts {
		index[t.ID] = i
		args = append(args, t.ID)
	}
	rows, err := x.QueryContext(ctx,
		`SELECT task_id, actor, kind, at FROM task_events
		 WHERE id IN (SELECT MAX(id) FROM task_events WHERE task_id IN (`+placeholders(len(ts))+`) GROUP BY task_id)`, args...)
	if err != nil {
		return fmt.Errorf("tasks: reading last events: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var (
			id int64
			le LastEvent
			at string
		)
		if err := rows.Scan(&id, &le.Actor, &le.Kind, &at); err != nil {
			return fmt.Errorf("tasks: reading last events: %w", err)
		}
		le.At, _ = time.Parse(timeFmt, at)
		ts[index[id]].LastEvent = &le
	}
	return rows.Err()
}

// sourceKindFor decides the stored source kind. The actor decides what is
// allowed: an agent's tasks are always agent tasks (it may not call itself a
// capture, which would skip the hourly limit), a capture names its surface, the
// operator is cli or app.
func sourceKindFor(actor Actor, requested string) (string, error) {
	switch actor.Kind {
	case Agent:
		if requested == "" || requested == SourceCLI || requested == SourceAgent {
			return SourceAgent, nil
		}
		return "", invalid("an agent's tasks are agent tasks: source %q is for captures", requested)
	case Capture:
		kind := requested
		if kind == "" {
			kind = actor.Name
		}
		if kind != SourceChrome && kind != SourceOS {
			return "", invalid("a capture comes from chrome or os, not %q", kind)
		}
		return kind, nil
	case Human:
		switch requested {
		case "", SourceCLI:
			return SourceCLI, nil
		case SourceApp:
			return SourceApp, nil
		}
		return "", invalid("source %q is for captures and agents: the operator's tasks come from cli or app", requested)
	}
	return "", invalid("unknown actor")
}

// checkAddLimits refuses a task that would pass the board's size or the hourly
// limit on agent-created tasks.
func (s *Store) checkAddLimits(ctx context.Context, x dbx, profileID, kind string) error {
	var open int
	if err := x.QueryRowContext(ctx, `SELECT COUNT(*) FROM tasks WHERE profile_id = ? AND status <> 'archived'`, profileID).Scan(&open); err != nil {
		return fmt.Errorf("tasks: counting tasks: %w", err)
	}
	if open >= MaxOpenTasks {
		return limit("this profile already has %d open tasks: archive some first", MaxOpenTasks)
	}
	if kind != SourceAgent {
		return nil
	}
	since := s.now().UTC().Add(-time.Hour).Format(timeFmt)
	var recent int
	if err := x.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM tasks WHERE profile_id = ? AND source_kind = 'agent' AND created_at >= ?`, profileID, since).Scan(&recent); err != nil {
		return fmt.Errorf("tasks: counting agent tasks: %w", err)
	}
	if recent >= AgentTasksPerHour {
		return limit("agents may add %d tasks an hour to a profile: wait, or ask the operator", AgentTasksPerHour)
	}
	return nil
}

// Add creates a task in the profile's Inbox (or in Ready, for the operator who
// asks). With a ClientID it is idempotent: a second Add with the same key
// returns the task that exists and created is false.
func (s *Store) Add(ctx context.Context, profileID string, in AddInput, actor Actor) (Task, bool, error) {
	title, notes, err := deriveTitleNotes(in.Title, in.Notes, in.Text)
	if err != nil {
		return Task{}, false, err
	}
	status := StatusInbox
	if in.Ready {
		if actor.Kind != Human {
			return Task{}, false, operatorOnly("add a task straight to Ready")
		}
		status = StatusReady
	}
	kind, err := sourceKindFor(actor, in.SourceKind)
	if err != nil {
		return Task{}, false, err
	}
	if actor.Kind == Agent && actor.Name != "" && !nameRE.MatchString(actor.Name) {
		return Task{}, false, invalid("an agent name is 1-%d characters of letters, digits and . _ # @ : -", MaxNameLen)
	}
	clientID := strings.TrimSpace(in.ClientID)
	if clientID != "" && !clientIDRE.MatchString(clientID) {
		return Task{}, false, invalid("a client id is 1-%d characters of letters, digits, _ and -", MaxClientIDLen)
	}
	src := Source{
		Kind:  kind,
		URL:   cleanURL(in.SourceURL),
		Title: cutRunes(oneLine(in.SourceTitle), MaxSourceTitleRunes),
		App:   cutRunes(oneLine(in.SourceApp), MaxAppRunes),
	}

	var out Task
	var created bool
	err = s.tx(ctx, func(x dbx) error {
		if _, err := s.profileOf(ctx, x, profileID); err != nil {
			return err
		}
		if clientID != "" {
			var existing int64
			err := x.QueryRowContext(ctx, `SELECT id FROM tasks WHERE profile_id = ? AND client_id = ?`, profileID, clientID).Scan(&existing)
			if err == nil {
				out, err = s.getTx(ctx, x, profileID, existing)
				return err
			}
			if !errors.Is(err, sql.ErrNoRows) {
				return fmt.Errorf("tasks: looking up the client id: %w", err)
			}
		}
		if err := s.checkAddLimits(ctx, x, profileID, kind); err != nil {
			return err
		}
		pos, err := s.edgePosition(ctx, x, profileID, status, atTop(status), 0)
		if err != nil {
			return err
		}
		var cid any
		if clientID != "" {
			cid = clientID
		}
		stamp := s.stamp()
		res, err := x.ExecContext(ctx,
			`INSERT INTO tasks (profile_id, title, notes, status, position, source_kind, source_url, source_title, source_app, client_id, created_at, updated_at)
			 VALUES (?,?,?,?,?,?,?,?,?,?,?,?)`,
			profileID, title, notes, string(status), pos, src.Kind, src.URL, src.Title, src.App, cid, stamp, stamp)
		if err != nil {
			return fmt.Errorf("tasks: adding a task: %w", err)
		}
		id, err := res.LastInsertId()
		if err != nil {
			return fmt.Errorf("tasks: adding a task: %w", err)
		}
		if err := s.event(ctx, x, id, stamp, actor.Label(), "created", "", string(status), ""); err != nil {
			return err
		}
		if err := s.bump(ctx, x, profileID); err != nil {
			return err
		}
		out, err = s.getTx(ctx, x, profileID, id)
		created = err == nil
		return err
	})
	if err != nil {
		return Task{}, false, err
	}
	return out, created, nil
}
