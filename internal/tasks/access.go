package tasks

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

// AgentAccess is what the operator has delegated to AI agents on one profile's
// board (spec D33). Both switches are off until the operator turns them on, and
// they are independent: Approve does not imply View (an agent that names the
// Inbox in a list, or shows a task, already reads it; View only puts the Inbox
// in the default list and opens the whole-board read).
type AgentAccess struct {
	// View lets an agent see the Inbox without naming it, and the whole board.
	View bool `json:"view"`
	// Approve lets a named agent move Inbox tasks to Ready.
	Approve bool `json:"approve"`
}

// DelegatedApprovalNote is the note of the event of an approval an agent made
// on the operator's delegation: it marks the approval in the task's history.
const DelegatedApprovalNote = "approved (delegated: the operator lets agents approve)"

// agentAccessKey is the row of the settings table that holds a profile's access.
// The value is the enabled switches, comma separated ("view", "approve"); no row
// means both are off.
func agentAccessKey(profileID string) string { return "task_agent_access:" + profileID }

// AgentAccess reads what agents may do on the profile. It reads the database at
// each call and caches nothing, so that a deny takes effect on the next call. An
// unknown profile, or one with no row, has everything off.
func (s *Store) AgentAccess(ctx context.Context, profileID string) (AgentAccess, error) {
	var v string
	err := s.db.QueryRowContext(ctx, `SELECT value FROM settings WHERE key = ?`, agentAccessKey(profileID)).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return AgentAccess{}, nil
	}
	if err != nil {
		return AgentAccess{}, fmt.Errorf("tasks: reading the agents' access: %w", err)
	}
	var a AgentAccess
	for _, w := range strings.Split(v, ",") {
		switch strings.TrimSpace(w) {
		case "view":
			a.View = true
		case "approve":
			a.Approve = true
		}
	}
	return a, nil
}

// SetAgentAccess sets what agents may do on the profile. Only the operator may:
// an agent, a capture and an actor that was never set are refused, so no agent
// can grant itself access. The profile must exist. With both switches off the
// row is removed, which is the same as never having set it.
func (s *Store) SetAgentAccess(ctx context.Context, profileID string, a AgentAccess, actor Actor) error {
	if actor.Kind != Human {
		return operatorOnly("change what agents may do on the board")
	}
	if _, err := s.Profile(ctx, profileID); err != nil {
		return err
	}
	var on []string
	if a.View {
		on = append(on, "view")
	}
	if a.Approve {
		on = append(on, "approve")
	}
	var err error
	if len(on) == 0 {
		_, err = s.db.ExecContext(ctx, `DELETE FROM settings WHERE key = ?`, agentAccessKey(profileID))
	} else {
		_, err = s.db.ExecContext(ctx,
			`INSERT INTO settings (key, value) VALUES (?, ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value`,
			agentAccessKey(profileID), strings.Join(on, ","))
	}
	if err != nil {
		return fmt.Errorf("tasks: saving the agents' access: %w", err)
	}
	return nil
}

// agentMay says whether the actor is an agent the operator has given the right
// that pick selects, on the profile, read now. It fails closed: when the setting
// cannot be read, the answer is no and the caller gives the refusal it always gave.
func (s *Store) agentMay(ctx context.Context, profileID string, actor Actor, pick func(AgentAccess) bool) bool {
	if actor.Kind != Agent {
		return false
	}
	a, err := s.AgentAccess(ctx, profileID)
	return err == nil && pick(a)
}
