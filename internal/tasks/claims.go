package tasks

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// noteRenewed is the note of the claimed event of a claim that renews the lease of the agent that holds
// the task.
const noteRenewed = "renewed"

// clampLease is the lease an agent gets for the one it asked for: 30 minutes when it asked for none
// (zero or less) and 24 hours at most. Any Duration may be passed, the most negative and the largest
// included: the result stays between the two bounds, so adding it to a time cannot overflow.
func clampLease(d time.Duration) time.Duration {
	switch {
	case d <= 0:
		return DefaultLease
	case d > MaxLease:
		return MaxLease
	}
	return d
}

// leaseEnd is when a lease of d that begins at now ends, as a stored time. A time is kept to the second,
// so the end is rounded up to the next whole second: a lease is never shorter than the one asked for,
// and a short one is not over at the second it began in, where anybody could take the task at once.
func leaseEnd(now time.Time, d time.Duration) time.Time {
	end := now.Add(d)
	if end.Nanosecond() != 0 {
		end = end.Truncate(time.Second).Add(time.Second)
	}
	return end
}

// later is the later of two times: a renewal keeps the lease it would otherwise shorten.
func later(a, b time.Time) time.Time {
	if b.After(a) {
		return b
	}
	return a
}

// needName refuses what is not an agent with a usable name: a claim is held under a name, the same one
// for the whole task, and an event shows it, so it may not be a label the store writes for someone else
// (checkAgentName).
func needName(a Actor) error {
	switch {
	case a.Kind != Agent:
		return invalid("only an agent claims, comments on, finishes or releases a task; the operator moves the cards")
	case a.Name == "":
		return invalid("an agent needs a name: pass --as NAME, the same one for the whole task")
	}
	return checkAgentName(a.Name)
}

// pickNext is the id of the task Next would take: the top of Ready, else the stale claim whose lease ended
// first. A task that holds MaxEventsToClaim events is passed over, because a claim of it is refused:
// offered, it would stay at the head of the queue and block every task behind it. 0 means there is none.
func (s *Store) pickNext(ctx context.Context, x dbx, profileID string) (int64, error) {
	var id int64
	err := x.QueryRowContext(ctx,
		`SELECT id FROM tasks WHERE profile_id = ? AND status = 'ready'
		   AND (SELECT COUNT(*) FROM task_events WHERE task_id = tasks.id) < ?
		 ORDER BY position, id LIMIT 1`, profileID, MaxEventsToClaim).Scan(&id)
	if err == nil {
		return id, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return 0, fmt.Errorf("tasks: picking the next task: %w", err)
	}
	err = x.QueryRowContext(ctx,
		`SELECT id FROM tasks WHERE profile_id = ? AND status = 'in_progress' AND claimed_by <> '' AND claim_until <= ?
		   AND (SELECT COUNT(*) FROM task_events WHERE task_id = tasks.id) < ?
		 ORDER BY claim_until, id LIMIT 1`, profileID, s.stamp(), MaxEventsToClaim).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("tasks: picking the next task: %w", err)
	}
	return id, nil
}

// Next returns the task an agent should work next. Without claim it only looks, in one snapshot (two
// agents that look may see the same task), for the operator and for any agent, named or not; a capture
// may not read the board. With claim it picks and claims in one write transaction, so two agents never
// get the same one, and it needs an agent with a name. A look offers what a claim would take. A nil
// task and a nil error mean there is nothing to do.
func (s *Store) Next(ctx context.Context, profileID string, actor Actor, claim bool, lease time.Duration) (*Task, error) {
	if !claim {
		switch actor.Kind {
		case Human, Agent:
		case Capture:
			return nil, operatorOnly("read the board")
		default:
			return nil, invalid("unknown actor")
		}
		var out *Task
		err := s.snapshot(ctx, func(x dbx) error {
			if _, err := s.profileOf(ctx, x, profileID); err != nil {
				return err
			}
			id, err := s.pickNext(ctx, x, profileID)
			if err != nil || id == 0 {
				return err
			}
			t, err := s.getTx(ctx, x, profileID, id)
			if err != nil {
				return err
			}
			out = &t
			return nil
		})
		if err != nil {
			return nil, err
		}
		return out, nil
	}
	if err := needName(actor); err != nil {
		return nil, err
	}
	var out *Task
	err := s.tx(ctx, func(x dbx) error {
		if _, err := s.profileOf(ctx, x, profileID); err != nil {
			return err
		}
		id, err := s.pickNext(ctx, x, profileID)
		if err != nil || id == 0 {
			return err
		}
		t, err := s.claimTx(ctx, x, profileID, id, actor, lease)
		if err != nil {
			return err
		}
		if err := s.bump(ctx, x, profileID); err != nil {
			return err
		}
		out = &t
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// Claim takes a Ready task for an agent, or a task whose claim has gone stale, or renews the lease of a
// task the agent holds already; never one that another agent holds, nor one the operator works on.
func (s *Store) Claim(ctx context.Context, profileID string, id int64, actor Actor, lease time.Duration) (Task, error) {
	if err := needName(actor); err != nil {
		return Task{}, err
	}
	var out Task
	err := s.tx(ctx, func(x dbx) error {
		if _, err := s.profileOf(ctx, x, profileID); err != nil {
			return err
		}
		t, err := s.claimTx(ctx, x, profileID, id, actor, lease)
		if err != nil {
			return err
		}
		if err := s.bump(ctx, x, profileID); err != nil {
			return err
		}
		out = t
		return nil
	})
	if err != nil {
		return Task{}, err
	}
	return out, nil
}

// claimOf says what a claim of the task by the agent called name would be at now: the kind and the note
// of the event it writes, or why it is refused. A task is claimed when it is Ready (a claim left on a
// Ready row is overwritten), when it is In progress under the same name (a renewal), and when it is In
// progress under another name whose lease has ended; the lease that ends exactly now has ended.
func claimOf(cur Task, name string, now time.Time) (kind, note string, refused error) {
	switch {
	case cur.Status == StatusReady:
		return "claimed", "", nil
	case cur.Status != StatusInProgress:
		return "", "", notReady("task #%d is %s: only a ready task can be claimed", cur.ID, cur.Status)
	case cur.Claim == nil:
		return "", "", notReady("task #%d is being worked on by the operator", cur.ID)
	case cur.Claim.By == name:
		return "claimed", noteRenewed, nil
	case !cur.Claim.Until.After(now):
		return "reclaimed", "the claim of " + echo(cur.Claim.By) + " had expired", nil
	}
	return "", "", &ClaimedError{By: cur.Claim.By, Until: cur.Claim.Until}
}

// claimTx claims task id in the caller's write transaction. One conditional UPDATE decides: it takes the
// task only when it is Ready, or In progress with a lease that has ended or under the same name, and has
// fewer than MaxEventsToClaim events; and only a task of the profile. The task is read first for what the
// UPDATE writes (the place in the column, the lease that a renewal never shortens) and for the event, and
// read again when the UPDATE takes nothing, to say why. If the two do not agree, the UPDATE took what the
// rules refuse or refused what they allow, and the error undoes the transaction.
func (s *Store) claimTx(ctx context.Context, x dbx, profileID string, id int64, actor Actor, lease time.Duration) (Task, error) {
	cur, err := s.getTx(ctx, x, profileID, id)
	if err != nil {
		return Task{}, err
	}
	now := s.now().UTC()
	stamp := now.Format(timeFmt)
	name := actor.Label()
	kind, note, refused := claimOf(cur, name, now)
	until, pos := leaseEnd(now, clampLease(lease)), cur.Position
	switch {
	case cur.Status == StatusReady:
		if pos, err = s.edgePosition(ctx, x, profileID, StatusInProgress, false, id); err != nil {
			return Task{}, err
		}
	case note == noteRenewed:
		until = later(cur.Claim.Until, until)
	}
	// The UPDATE is made whatever the read says: it, and not the read, decides what is taken.
	res, err := x.ExecContext(ctx,
		`UPDATE tasks SET status = 'in_progress', claimed_by = ?, claim_until = ?, position = ?, updated_at = ?
		 WHERE id = ? AND profile_id = ?
		   AND (status = 'ready'
		        OR (status = 'in_progress' AND claimed_by <> '' AND (claimed_by = ? OR claim_until <= ?)))
		   AND (SELECT COUNT(*) FROM task_events WHERE task_id = tasks.id) < ?`,
		name, until.Format(timeFmt), pos, stamp, id, profileID, name, stamp, MaxEventsToClaim)
	if err != nil {
		return Task{}, fmt.Errorf("tasks: claiming #%d: %w", id, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return Task{}, fmt.Errorf("tasks: claiming #%d: %w", id, err)
	}
	switch {
	case n == 0:
		return Task{}, s.claimRefusal(ctx, x, profileID, id, name, now)
	case n > 1:
		return Task{}, fmt.Errorf("tasks: claiming #%d: the UPDATE took %d tasks", id, n)
	case refused != nil:
		return Task{}, fmt.Errorf("tasks: claiming #%d: the UPDATE took a task that the rules refuse: %v", id, refused)
	}
	if err := s.event(ctx, x, id, stamp, name, kind, string(cur.Status), string(StatusInProgress), note); err != nil {
		return Task{}, err
	}
	return s.getTx(ctx, x, profileID, id)
}

// claimRefusal says why the UPDATE of a claim took nothing, from the task as it is now. A task that a claim
// would take but that holds MaxEventsToClaim events is refused for that, after the other refusals: a task
// that cannot be claimed anyway says why first.
func (s *Store) claimRefusal(ctx context.Context, x dbx, profileID string, id int64, name string, now time.Time) error {
	cur, err := s.getTx(ctx, x, profileID, id)
	if err != nil {
		return err
	}
	if _, _, refused := claimOf(cur, name, now); refused != nil {
		return refused
	}
	n, err := s.eventCount(ctx, x, id)
	if err != nil {
		return err
	}
	if n >= MaxEventsToClaim {
		return limit("task #%d already has %d events: leave it to the operator (the agent that holds it can still finish or release it)", id, n)
	}
	return fmt.Errorf("tasks: claiming #%d: a task the rules allow was not taken", id)
}

// eventCount is how many events a task holds.
func (s *Store) eventCount(ctx context.Context, x dbx, id int64) (int, error) {
	var n int
	if err := x.QueryRowContext(ctx, `SELECT COUNT(*) FROM task_events WHERE task_id = ?`, id).Scan(&n); err != nil {
		return 0, fmt.Errorf("tasks: counting the events of #%d: %w", id, err)
	}
	return n, nil
}

// heldBy returns the task when the agent holds it (In progress, claimed under its name, whether or not
// the lease has ended: nobody has taken it yet), else ErrNotClaimant.
func (s *Store) heldBy(ctx context.Context, x dbx, profileID string, id int64, actor Actor) (Task, error) {
	cur, err := s.getTx(ctx, x, profileID, id)
	if err != nil {
		return Task{}, err
	}
	if cur.Status != StatusInProgress || cur.Claim == nil || cur.Claim.By != actor.Label() {
		return Task{}, notClaimant("task #%d is not held by %s: claim it first", id, actor.Label())
	}
	return cur, nil
}

// Comment adds a note to a task's history. The operator comments on any task; an agent only on one it
// holds, and its comment renews the lease (never shortening it). A comment is refused once the task has
// MaxEventsPerTask events: a change of state is always recorded, a comment is not.
func (s *Store) Comment(ctx context.Context, profileID string, id int64, text string, actor Actor) (Task, error) {
	switch actor.Kind {
	case Agent:
		if err := needName(actor); err != nil {
			return Task{}, err
		}
	case Human:
	default:
		return Task{}, invalid("only the operator and agents comment")
	}
	text = cutBytes(cleanText(text), MaxCommentBytes)
	if text == "" {
		return Task{}, invalid("a comment needs text")
	}
	var out Task
	err := s.tx(ctx, func(x dbx) error {
		if _, err := s.profileOf(ctx, x, profileID); err != nil {
			return err
		}
		var cur Task
		var err error
		if actor.Kind == Agent {
			cur, err = s.heldBy(ctx, x, profileID, id, actor)
		} else {
			cur, err = s.getTx(ctx, x, profileID, id)
		}
		if err != nil {
			return err
		}
		n, err := s.eventCount(ctx, x, id)
		if err != nil {
			return err
		}
		if n >= MaxEventsPerTask {
			return limit("task #%d already has %d events: no more comments fit (an agent that holds it can finish or release it instead)", id, n)
		}
		now := s.now().UTC()
		stamp := now.Format(timeFmt)
		if actor.Kind == Agent {
			until := later(cur.Claim.Until, leaseEnd(now, DefaultLease))
			_, err = x.ExecContext(ctx, `UPDATE tasks SET claim_until = ?, updated_at = ? WHERE id = ? AND profile_id = ?`,
				until.Format(timeFmt), stamp, id, profileID)
		} else {
			_, err = x.ExecContext(ctx, `UPDATE tasks SET updated_at = ? WHERE id = ? AND profile_id = ?`, stamp, id, profileID)
		}
		if err != nil {
			return fmt.Errorf("tasks: commenting on #%d: %w", id, err)
		}
		if err := s.event(ctx, x, id, stamp, actor.Label(), "comment", "", "", text); err != nil {
			return err
		}
		if err := s.bump(ctx, x, profileID); err != nil {
			return err
		}
		out, err = s.getTx(ctx, x, profileID, id)
		return err
	})
	if err != nil {
		return Task{}, err
	}
	return out, nil
}

// Finish hands a held task back to the operator, in Review, with a result or a question (exactly one).
func (s *Store) Finish(ctx context.Context, profileID string, id int64, o Outcome, actor Actor) (Task, error) {
	if err := needName(actor); err != nil {
		return Task{}, err
	}
	result := cutBytes(cleanText(o.Result), MaxCommentBytes)
	question := cutBytes(cleanText(o.Question), MaxCommentBytes)
	if (result == "") == (question == "") {
		return Task{}, invalid("give exactly one of a result and a question")
	}
	kind, note := "result", result
	if question != "" {
		kind, note = "question", question
	}
	return s.handBack(ctx, profileID, id, actor, StatusReview, kind, note)
}

// Release gives a held task back to Ready, behind the others, so that the agent that gave it up is not
// offered it again at once.
func (s *Store) Release(ctx context.Context, profileID string, id int64, note string, actor Actor) (Task, error) {
	if err := needName(actor); err != nil {
		return Task{}, err
	}
	return s.handBack(ctx, profileID, id, actor, StatusReady, "released", cutBytes(cleanText(note), MaxCommentBytes))
}

// handBack ends the agent's claim: the task goes to `to` (Review at the top, Ready at the bottom), both
// claim columns are emptied, and the event says why.
func (s *Store) handBack(ctx context.Context, profileID string, id int64, actor Actor, to Status, kind, note string) (Task, error) {
	var out Task
	err := s.tx(ctx, func(x dbx) error {
		if _, err := s.profileOf(ctx, x, profileID); err != nil {
			return err
		}
		if _, err := s.heldBy(ctx, x, profileID, id, actor); err != nil {
			return err
		}
		pos, err := s.edgePosition(ctx, x, profileID, to, atTop(to), id)
		if err != nil {
			return err
		}
		stamp := s.stamp()
		if _, err := x.ExecContext(ctx,
			`UPDATE tasks SET status = ?, claimed_by = '', claim_until = '', position = ?, updated_at = ? WHERE id = ? AND profile_id = ?`,
			string(to), pos, stamp, id, profileID); err != nil {
			return fmt.Errorf("tasks: handing back #%d: %w", id, err)
		}
		if err := s.event(ctx, x, id, stamp, actor.Label(), kind, string(StatusInProgress), string(to), note); err != nil {
			return err
		}
		if err := s.bump(ctx, x, profileID); err != nil {
			return err
		}
		out, err = s.getTx(ctx, x, profileID, id)
		return err
	})
	if err != nil {
		return Task{}, err
	}
	return out, nil
}
