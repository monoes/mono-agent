package summary

import (
	"context"

	"github.com/monoes/mono-agent/internal/tasks"
)

// TasksSection is the profile's task board at a glance (task board spec,
// section 9): its open columns, the claims past their lease and the task an
// agent would take next. Done is the operator's closed work and is left out.
type TasksSection struct {
	Inbox      int       `json:"inbox"`
	Ready      int       `json:"ready"`
	InProgress int       `json:"in_progress"`
	Review     int       `json:"review"`
	Stale      int       `json:"stale"`
	Next       *NextTask `json:"next"`
	Error      string    `json:"error,omitempty"`
}

// NextTask is the task `task next` shows: the top of Ready, else the claim whose
// lease ran out first.
type NextTask struct {
	ID    int64  `json:"id"`
	Title string `json:"title"`
}

// tasksSection reads the board through internal/tasks, as `task next` does. A
// stale claim is judged by the wall clock: the store has no other.
func tasksSection(ctx context.Context, o Options) *TasksSection {
	s := &TasksSection{}
	if o.DB == nil {
		s.Error = noDB
		return s
	}
	store := tasks.NewStore(o.DB)
	c, err := store.Counts(ctx, o.ProfileID)
	if err != nil {
		s.Error = err.Error()
		return s
	}
	s.Inbox, s.Ready, s.InProgress, s.Review, s.Stale = c.Inbox, c.Ready, c.InProgress, c.Review, c.Stale
	next, err := store.Next(ctx, o.ProfileID, tasks.Actor{Kind: tasks.Agent}, false, 0)
	if err != nil {
		s.Error = err.Error()
		return s
	}
	if next != nil {
		s.Next = &NextTask{ID: next.ID, Title: next.Title}
	}
	return s
}
