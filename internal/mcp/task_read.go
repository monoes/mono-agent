package mcp

import (
	"context"
	"encoding/json"

	"github.com/monoes/mono-agent/internal/tasks"
)

const (
	taskListDefault = 50  // task_list's limit when none is given
	taskListMax     = 200 // and the most it returns
)

// taskReadTools only read the board: they are in every server, the default
// read-only one included (spec 8).
func taskReadTools() []tool {
	return []tool{
		{
			name: "task_list",
			description: taskBoardIntro +
				"Lists the tasks of this server's profile, by column and then by position: {profile, tasks, note}, each task " +
				"{id, profile_id, title_untrusted, notes_untrusted, status, position, source_kind, source_url_untrusted, source_title_untrusted, " +
				"source_app_untrusted, claim: {by, until, stale} or null, last_event: {actor, kind, at} or null, created_at, updated_at}. " +
				"Notes longer than 1000 characters are cut here; task_get has all of them. " +
				"status: a column or several (inbox, ready, in_progress, review, done, archived), as \"ready,review\" or a list; " +
				"omitted, it is ready, in_progress and review, and inbox, done and archived are listed only when named. " +
				"limit: at most this many (default 50, at most 200). " +
				"Refusals: invalid_input (an unknown status, an argument this tool does not take).",
			schema: objSchema(map[string]interface{}{
				"status": map[string]interface{}{
					"anyOf": []interface{}{
						map[string]interface{}{"type": "string"},
						map[string]interface{}{"type": "array", "items": map[string]interface{}{"type": "string"}},
					},
					"description": "Columns to list: inbox, ready, in_progress, review, done, archived (default: ready, in_progress and review)",
				},
				"limit": intParam("At most this many tasks (default 50, at most 200)"),
			}),
			annotations: map[string]bool{"readOnlyHint": true, "idempotentHint": true},
			handler:     toolTaskList,
		},
		{
			name: "task_get",
			description: taskBoardIntro +
				"One task of this server's profile with its history: {profile, task, events, note}; the task as task_list describes it, " +
				"with all of its notes, and events [{id, at, actor, kind, from_status, to_status, note_untrusted}], oldest first " +
				"(kinds: created, edited, moved, claimed, reclaimed, comment, question, result, released, archived, unarchived). " +
				"id: the task's number. Refusals: not_found (no such task in this profile; a task of another profile is not found either), invalid_input.",
			schema: objSchema(map[string]interface{}{
				"id": intParam("The task's number, as task_list shows it"),
			}, "id"),
			annotations: map[string]bool{"readOnlyHint": true, "idempotentHint": true},
			handler:     toolTaskGet,
		},
		{
			name: "task_next",
			description: taskBoardIntro +
				"The task task_claim with next: true would take now, without claiming it: {profile, task or null, note}. " +
				"It is the top of Ready, else a claim whose lease has run out; never an Inbox task. " +
				"Two agents that look may see the same task: only a claim gives it to you. No arguments.",
			schema:      objSchema(nil),
			annotations: map[string]bool{"readOnlyHint": true, "idempotentHint": true},
			handler:     toolTaskNext,
		},
	}
}

func toolTaskList(ctx context.Context, s *Server, args json.RawMessage) (interface{}, error) {
	var a struct {
		Status statusArg `json:"status"`
		Limit  numberArg `json:"limit"`
	}
	if err := decodeTaskArgs(args, &a); err != nil {
		return nil, err
	}
	limit := int(a.Limit)
	switch {
	case limit <= 0:
		limit = taskListDefault
	case limit > taskListMax:
		limit = taskListMax
	}
	store, p, actor, err := s.taskBoard(ctx)
	if err != nil {
		return nil, err
	}
	ts, err := store.List(ctx, p.ID, tasks.Filter{Statuses: a.Status, Limit: limit}, actor)
	if err != nil {
		return nil, taskToolErr(err)
	}
	return taskListResult{Profile: p, Tasks: listViews(ts), Note: untrustedNote}, nil
}

func toolTaskGet(ctx context.Context, s *Server, args json.RawMessage) (interface{}, error) {
	var a struct {
		ID taskIDArg `json:"id"`
	}
	if err := decodeTaskArgs(args, &a); err != nil {
		return nil, err
	}
	id, err := a.ID.need()
	if err != nil {
		return nil, err
	}
	store, p, _, err := s.taskBoard(ctx)
	if err != nil {
		return nil, err
	}
	t, events, err := store.Get(ctx, p.ID, id)
	if err != nil {
		return nil, taskToolErr(err)
	}
	return taskGetResult{Profile: p, Task: viewOf(t), Events: eventViews(events), Note: untrustedNote}, nil
}

func toolTaskNext(ctx context.Context, s *Server, args json.RawMessage) (interface{}, error) {
	if err := decodeTaskArgs(args, &struct{}{}); err != nil {
		return nil, err
	}
	store, p, actor, err := s.taskBoard(ctx)
	if err != nil {
		return nil, err
	}
	t, err := store.Next(ctx, p.ID, actor, false, 0)
	if err != nil {
		return nil, taskToolErr(err)
	}
	return taskResult{Profile: p, Task: viewPtr(t), Note: untrustedNote}, nil
}
