package tasks

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"strings"
)

// Edit changes a task's title and notes. Only the operator edits: what the
// operator approved is what an agent reads. A text that is what it was after
// cleaning is no change, so what a read returned can be sent back without
// leaving a trace.
func (s *Store) Edit(ctx context.Context, profileID string, id int64, e Edit, actor Actor) (Task, error) {
	if actor.Kind != Human {
		return Task{}, operatorOnly("edit a task")
	}
	if e.Title == nil && e.Notes == nil {
		return Task{}, invalid("nothing to change: give a title or notes")
	}
	var out Task
	err := s.tx(ctx, func(x dbx) error {
		if _, err := s.profileOf(ctx, x, profileID); err != nil {
			return err
		}
		cur, err := s.getTx(ctx, x, profileID, id)
		if err != nil {
			return err
		}
		title, notes := cur.Title, cur.Notes
		var changed []string
		if e.Title != nil {
			t := cleanTitle(*e.Title)
			if t == "" {
				return invalid("the title cannot be empty")
			}
			if t != title {
				title = t
				changed = append(changed, "title")
			}
		}
		if e.Notes != nil {
			n := cutBytes(cleanText(*e.Notes), MaxNotesBytes)
			if n != notes {
				notes = n
				changed = append(changed, "notes")
			}
		}
		if len(changed) == 0 {
			out = cur
			return nil
		}
		stamp := s.stamp()
		if _, err := x.ExecContext(ctx, `UPDATE tasks SET title = ?, notes = ?, updated_at = ? WHERE id = ? AND profile_id = ?`,
			title, notes, stamp, id, profileID); err != nil {
			return fmt.Errorf("tasks: editing #%d: %w", id, err)
		}
		if err := s.event(ctx, x, id, stamp, actor.Label(), "edited", "", "", strings.Join(changed, ", ")); err != nil {
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

// columnOf lists the cards of one column of the profile, top to bottom, as their
// ids and their positions. The card except (0 for none) is left out.
func (s *Store) columnOf(ctx context.Context, x dbx, profileID string, st Status, except int64) ([]int64, []int64, error) {
	rows, err := x.QueryContext(ctx,
		`SELECT id, position FROM tasks WHERE profile_id = ? AND status = ? AND id <> ? ORDER BY position, id`,
		profileID, string(st), except)
	if err != nil {
		return nil, nil, fmt.Errorf("tasks: reading a column: %w", err)
	}
	defer rows.Close()
	var ids, pos []int64
	for rows.Next() {
		var id, p int64
		if err := rows.Scan(&id, &p); err != nil {
			return nil, nil, fmt.Errorf("tasks: reading a column: %w", err)
		}
		ids, pos = append(ids, id), append(pos, p)
	}
	if err := rows.Err(); err != nil {
		return nil, nil, fmt.Errorf("tasks: reading a column: %w", err)
	}
	return ids, pos, nil
}

// archiveEnd is the position one gap below the last card of the archive. The archive
// is the one column that has no limit, and edgePosition, which asks for the first and
// the last position together, reads all of it: this asks for the last only, which the
// index answers at once, so that archiving a column does not read the whole archive
// again for each card. The card being archived is not in the archive yet, so no card
// is left out. Like every end it is refused, with errPositionRange, where the last
// card leaves no room for a gap.
func (s *Store) archiveEnd(ctx context.Context, x dbx, profileID string) (int64, error) {
	var hi sql.NullInt64
	err := x.QueryRowContext(ctx,
		`SELECT MAX(position) FROM tasks WHERE profile_id = ? AND status = ?`, profileID, string(StatusArchived)).Scan(&hi)
	if err != nil {
		return 0, fmt.Errorf("tasks: reading the archive: %w", err)
	}
	if !hi.Valid {
		return positionGap, nil
	}
	return gapBelow(hi.Int64)
}

// placeIn returns the position that puts card id where p says in column `to`
// (the card itself does not count). The top, the bottom and no placement need
// only the end of the column. A card put before or after another takes the
// midpoint of its two neighbours; when they leave no integer between them the
// whole column is renumbered, in the caller's transaction, and the order stays.
func (s *Store) placeIn(ctx context.Context, x dbx, profileID string, id int64, to Status, p Placement) (int64, error) {
	if p.set() > 1 {
		return 0, invalid("use only one of before, after, top and bottom")
	}
	if p.Before == 0 && p.After == 0 {
		if to == StatusArchived && !p.Top {
			return s.archiveEnd(ctx, x, profileID)
		}
		return s.edgePosition(ctx, x, profileID, to, p.Top || (!p.Bottom && atTop(to)), id)
	}
	ref := p.Before
	if p.After != 0 {
		ref = p.After
	}
	if ref == id {
		return 0, invalid("a task cannot be placed before or after itself")
	}
	ids, pos, err := s.columnOf(ctx, x, profileID, to, id)
	if err != nil {
		return 0, err
	}
	idx := slices.Index(ids, ref)
	if idx < 0 {
		return 0, invalid("task #%d is not in %s", ref, to)
	}
	if p.After != 0 {
		idx++
	}
	switch {
	case idx == 0:
		return gapAbove(pos[0])
	case idx == len(ids):
		return gapBelow(pos[idx-1])
	case pos[idx]-pos[idx-1] >= 2:
		return pos[idx-1] + (pos[idx]-pos[idx-1])/2, nil
	}
	// No room between the neighbours: the k-th card of the column, the new one
	// included, goes to position (k+1)*positionGap.
	for i, oid := range ids {
		rank := int64(i + 1)
		if i >= idx {
			rank++ // the slot at idx is the new card's
		}
		if _, err := x.ExecContext(ctx, `UPDATE tasks SET position = ? WHERE id = ? AND profile_id = ?`, rank*positionGap, oid, profileID); err != nil {
			return 0, fmt.Errorf("tasks: renumbering a column: %w", err)
		}
	}
	return int64(idx+1) * positionGap, nil
}

// moveTx moves a card in the caller's transaction and writes the event of kind. Only a
// move within In progress keeps a claim: any other move ends it, whatever the card's status
// was (a reader shows a claim on any row that has one), and says so in a released event that
// comes before the move's own, so that the move is the last event of the card. A card that
// comes back from the archive counts against the limit of open tasks, as an added one does.
func (s *Store) moveTx(ctx context.Context, x dbx, profileID string, cur Task, to Status, p Placement, actor Actor, kind, note string) error {
	if cur.Status == StatusArchived && to != StatusArchived {
		if err := s.checkAddLimits(ctx, x, profileID, SourceCLI); err != nil { // the size check: the hourly limit is the agents'
			return err
		}
	}
	pos, err := s.placeIn(ctx, x, profileID, cur.ID, to, p)
	if err != nil {
		return err
	}
	stamp := s.stamp()
	keep := cur.Claim != nil && cur.Status == StatusInProgress && to == StatusInProgress
	claimedBy, until := "", ""
	if keep {
		claimedBy, until = cur.Claim.By, cur.Claim.Until.UTC().Format(timeFmt)
	}
	if cur.Claim != nil && !keep {
		ended := "the claim of " + echo(cur.Claim.By) + " ended: the operator " + kind + " the card"
		if err := s.event(ctx, x, cur.ID, stamp, actor.Label(), "released", string(cur.Status), string(to), ended); err != nil {
			return err
		}
	}
	if _, err := x.ExecContext(ctx,
		`UPDATE tasks SET status = ?, position = ?, claimed_by = ?, claim_until = ?, updated_at = ? WHERE id = ? AND profile_id = ?`,
		string(to), pos, claimedBy, until, stamp, cur.ID, profileID); err != nil {
		return fmt.Errorf("tasks: moving #%d: %w", cur.ID, err)
	}
	return s.event(ctx, x, cur.ID, stamp, actor.Label(), kind, string(cur.Status), string(to), note)
}

// Move puts a task in one of the five columns, where p says. With no placement a
// card new to the column goes to its default end; a card that is in the column
// already stays where it is, and nothing is written (a placement, even one that
// changes no order, is a reorder: it writes a moved event and moves the revision).
func (s *Store) Move(ctx context.Context, profileID string, id int64, to Status, p Placement, actor Actor) (Task, error) {
	if actor.Kind != Human {
		return Task{}, operatorOnly("move a task")
	}
	if !to.IsBoard() {
		return Task{}, invalid("move to one of inbox, ready, in_progress, review or done (archiving is its own command)")
	}
	var out Task
	err := s.tx(ctx, func(x dbx) error {
		if _, err := s.profileOf(ctx, x, profileID); err != nil {
			return err
		}
		cur, err := s.getTx(ctx, x, profileID, id)
		if err != nil {
			return err
		}
		if cur.Status == to && p.set() == 0 {
			out = cur
			return nil
		}
		if err := s.moveTx(ctx, x, profileID, cur, to, p, actor, "moved", ""); err != nil {
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

// each runs a verb on the tasks named, in one transaction, and returns them as they
// are afterwards, in the order of the ids. A task named twice is refused up front.
// Every task is read and checked in the order the ids are given, so a refusal names
// the first task that is wrong; then apply runs on each of them, from the first to
// the last or, with reverse, from the last to the first. Any failure undoes all of
// it; the revision moves once for the whole call.
func (s *Store) each(ctx context.Context, profileID string, ids []int64, reverse bool, check func(cur Task) error, apply func(x dbx, cur Task) error) ([]Task, error) {
	if len(ids) == 0 {
		return nil, invalid("name at least one task")
	}
	named := make(map[int64]bool, len(ids))
	for _, id := range ids {
		if named[id] {
			return nil, invalid("task #%d is named twice", id)
		}
		named[id] = true
	}
	var out []Task
	err := s.tx(ctx, func(x dbx) error {
		if _, err := s.profileOf(ctx, x, profileID); err != nil {
			return err
		}
		cards := make([]Task, 0, len(ids))
		for _, id := range ids {
			cur, err := s.getTx(ctx, x, profileID, id)
			if err != nil {
				return err
			}
			if err := check(cur); err != nil {
				return err
			}
			cards = append(cards, cur)
		}
		for i := range cards {
			cur := cards[i]
			if reverse {
				cur = cards[len(cards)-1-i]
			}
			if err := apply(x, cur); err != nil {
				return err
			}
		}
		if err := s.bump(ctx, x, profileID); err != nil {
			return err
		}
		out = make([]Task, 0, len(ids))
		for _, id := range ids {
			t, err := s.getTx(ctx, x, profileID, id)
			if err != nil {
				return err
			}
			out = append(out, t)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// Approve moves Inbox tasks to Ready, to the bottom of the queue or, with top, the
// top of it, in the order the ids are given either way. A task that is not in
// Inbox refuses the whole call, and the refusal names the first one in that order.
// With top the group is placed from the last id to the first, each on the top of
// the queue: the first id ends on the very top, and only the end of the column is
// read, so the queue is never renumbered (each id put after the one before it
// would halve the room above the old top every time).
func (s *Store) Approve(ctx context.Context, profileID string, ids []int64, top bool, actor Actor) ([]Task, error) {
	if actor.Kind != Human {
		return nil, operatorOnly("approve a task")
	}
	p := Placement{Bottom: true}
	if top {
		p = Placement{Top: true}
	}
	return s.each(ctx, profileID, ids, top,
		func(cur Task) error {
			if cur.Status != StatusInbox {
				return invalid("task #%d is %s, not inbox: only an inbox task is approved", cur.ID, cur.Status)
			}
			return nil
		},
		func(x dbx, cur Task) error {
			return s.moveTx(ctx, x, profileID, cur, StatusReady, p, actor, "moved", "approved")
		})
}

// Archive hides tasks from the board, keeping them.
func (s *Store) Archive(ctx context.Context, profileID string, ids []int64, actor Actor) ([]Task, error) {
	if actor.Kind != Human {
		return nil, operatorOnly("archive a task")
	}
	return s.each(ctx, profileID, ids, false,
		func(cur Task) error {
			if cur.Status == StatusArchived {
				return invalid("task #%d is already archived", cur.ID)
			}
			return nil
		},
		func(x dbx, cur Task) error {
			return s.moveTx(ctx, x, profileID, cur, StatusArchived, Placement{Bottom: true}, actor, "archived", "")
		})
}

// ArchiveStatus archives every task of one column, in the order it is in, and
// returns how many. An empty column writes nothing.
func (s *Store) ArchiveStatus(ctx context.Context, profileID string, status Status, actor Actor) (int, error) {
	if actor.Kind != Human {
		return 0, operatorOnly("archive tasks")
	}
	if !status.IsBoard() {
		return 0, invalid("archive a whole column of inbox, ready, in_progress, review or done")
	}
	n := 0
	err := s.tx(ctx, func(x dbx) error {
		if _, err := s.profileOf(ctx, x, profileID); err != nil {
			return err
		}
		ids, _, err := s.columnOf(ctx, x, profileID, status, 0)
		if err != nil {
			return err
		}
		for _, id := range ids {
			cur, err := s.getTx(ctx, x, profileID, id)
			if err != nil {
				return err
			}
			if err := s.moveTx(ctx, x, profileID, cur, StatusArchived, Placement{Bottom: true}, actor, "archived", ""); err != nil {
				return err
			}
		}
		if len(ids) == 0 {
			return nil
		}
		n = len(ids)
		return s.bump(ctx, x, profileID)
	})
	if err != nil {
		return 0, err
	}
	return n, nil
}

// Unarchive returns tasks to the column they were archived from; a task that
// was in progress comes back as ready, since nobody holds it now, and one whose
// history does not say where it came from goes to Inbox.
func (s *Store) Unarchive(ctx context.Context, profileID string, ids []int64, actor Actor) ([]Task, error) {
	if actor.Kind != Human {
		return nil, operatorOnly("unarchive a task")
	}
	return s.each(ctx, profileID, ids, false,
		func(cur Task) error {
			if cur.Status != StatusArchived {
				return invalid("task #%d is %s, not archived", cur.ID, cur.Status)
			}
			return nil
		},
		func(x dbx, cur Task) error {
			to := StatusInbox
			var from string
			err := x.QueryRowContext(ctx,
				`SELECT from_status FROM task_events WHERE task_id = ? AND kind = 'archived' ORDER BY id DESC LIMIT 1`, cur.ID).Scan(&from)
			switch {
			case errors.Is(err, sql.ErrNoRows):
			case err != nil:
				return fmt.Errorf("tasks: reading where #%d was archived from: %w", cur.ID, err)
			default:
				if st, perr := ParseStatus(from); perr == nil && st.IsBoard() {
					to = st
				}
			}
			if to == StatusInProgress {
				to = StatusReady
			}
			return s.moveTx(ctx, x, profileID, cur, to, Placement{}, actor, "unarchived", "")
		})
}
