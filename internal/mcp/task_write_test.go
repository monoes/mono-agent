package mcp

// An agent's verbs over the task tools (spec 5.1 and 8): it claims a ready task, reports
// progress and hands the task back; it adds to Inbox only, at most 20 an hour; it never does the
// operator's part; and nothing it sends changes who it is or which board it works.

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/monoes/mono-agent/internal/tasks"
)

// count runs a COUNT query on the fixture's database.
func (f *taskFixture) count(query string, args ...any) int {
	f.t.Helper()
	var n int
	if err := f.Side.DB.QueryRow(query, args...).Scan(&n); err != nil {
		f.t.Fatal(err)
	}
	return n
}

func TestAnAgentClaimsWorksAndHandsBackATask(t *testing.T) {
	f := newTaskFixture(t, taskSetup{})
	first := f.add("default", "first", true)
	second := f.add("default", "second", true)
	me := f.Server.taskActor().Name

	d := f.doc("task_claim", map[string]any{"next": true})
	tk := d["task"].(map[string]any)
	if taskOf(d) != first.ID || tk["status"] != "in_progress" || tk["claim"].(map[string]any)["by"] != me {
		t.Fatalf("task_claim next: %v", d)
	}
	steps := fmt.Sprint(d["next_steps"])
	for _, want := range []string{"task_comment", "task_finish", `"question"`, "task_release", fmt.Sprint(first.ID)} {
		if !strings.Contains(steps, want) {
			t.Errorf("next_steps do not say %s: %s", want, steps)
		}
	}
	d = f.doc("task_comment", map[string]any{"id": first.ID, "text": "halfway"})
	if le := d["task"].(map[string]any)["last_event"].(map[string]any); le["kind"] != "comment" || le["actor"] != me {
		t.Errorf("task_comment: %v", d)
	}
	d = f.doc("task_finish", map[string]any{"id": first.ID, "result": "sent the fix"})
	if tk := d["task"].(map[string]any); tk["status"] != "review" || tk["claim"] != nil {
		t.Errorf("task_finish: %v", d)
	}
	_, events, err := f.Store.Get(context.Background(), "default", first.ID)
	if err != nil {
		t.Fatal(err)
	}
	var kinds []string
	for _, e := range events {
		kinds = append(kinds, e.Kind)
		if e.Kind != "created" && e.Actor != me {
			t.Errorf("%s was written by %s, want %s", e.Kind, e.Actor, me)
		}
	}
	if got := strings.Join(kinds, ","); got != "created,claimed,comment,result" {
		t.Errorf("history %s", got)
	}

	f.doc("task_claim", map[string]any{"id": second.ID})
	d = f.doc("task_release", map[string]any{"id": second.ID, "note": "needs the VPN"})
	if tk := d["task"].(map[string]any); tk["status"] != "ready" || tk["claim"] != nil {
		t.Errorf("task_release: %v", d)
	}
	if d := f.doc("task_claim", map[string]any{"next": true}); taskOf(d) != second.ID {
		t.Fatalf("the released task is ready again: %v", d)
	}
	f.doc("task_finish", map[string]any{"id": second.ID, "question": "which VPN?"})
	d = f.doc("task_claim", map[string]any{"next": true})
	if d["task"] != nil || !strings.HasPrefix(fmt.Sprint(d["next_steps"]), "[Nothing is ready") {
		t.Errorf("nothing to take: %v", d)
	}
}

func TestTaskClaimTakesIDOrNextAndBoundsTheLease(t *testing.T) {
	f := newTaskFixture(t, taskSetup{})
	for _, args := range []map[string]any{{}, {"id": 1, "next": true}, {"next": false}} {
		if _, err := f.call("task_claim", args); err == nil || !strings.HasPrefix(err.Error(), "invalid_input: give id") {
			t.Errorf("%v: %v, want id or next, one of the two", args, err)
		}
	}
	for minutes, want := range map[int64]time.Duration{
		0:         30 * time.Minute,
		-5:        30 * time.Minute,
		1440:      24 * time.Hour,
		1441:      24 * time.Hour, // the store clamps this one too: without the bound only the next row fails
		307445735: 24 * time.Hour, // multiplied by a minute in nanoseconds this wraps to 26 seconds
	} {
		tk := f.add("default", fmt.Sprintf("lease %d", minutes), true)
		args := map[string]any{"id": tk.ID}
		if minutes != 0 {
			args["lease_minutes"] = minutes
		}
		f.doc("task_claim", args)
		got, _, err := f.Store.Get(context.Background(), "default", tk.ID)
		if err != nil || got.Claim == nil {
			t.Fatalf("lease %d: %+v, %v", minutes, got, err)
		}
		if off := time.Until(got.Claim.Until) - want; off < -time.Minute || off > time.Minute {
			t.Errorf("lease_minutes %d: the claim ends in %s, want %s", minutes, time.Until(got.Claim.Until).Round(time.Second), want)
		}
	}
	tk := f.add("default", "lease as text", true)
	f.doc("task_claim", map[string]any{"id": tk.ID, "lease_minutes": "60"})
	if got, _, err := f.Store.Get(context.Background(), "default", tk.ID); err != nil || got.Claim == nil ||
		time.Until(got.Claim.Until) < 59*time.Minute || time.Until(got.Claim.Until) > 61*time.Minute {
		t.Errorf(`lease_minutes "60": %+v, %v; want a claim of an hour`, got.Claim, err)
	}
}

// task_claim gives the task whole (the agent is about to work it); the progress verbs give it with
// its notes cut, as task_list does (Ruling 2).
func TestTheProgressVerbsCutLongNotesAndClaimDoesNot(t *testing.T) {
	f := newTaskFixture(t, taskSetup{})
	notes := strings.Repeat("n", 1500)
	tk, _, err := f.Store.Add(context.Background(), "default", tasks.AddInput{Title: "long", Notes: notes, Ready: true}, tasks.Actor{Kind: tasks.Human})
	if err != nil {
		t.Fatal(err)
	}
	notesOf := func(d map[string]any) string { return d["task"].(map[string]any)["notes_untrusted"].(string) }
	if got := notesOf(f.doc("task_claim", map[string]any{"id": tk.ID})); got != notes {
		t.Errorf("task_claim cut the notes of the task it gives: %d characters", len(got))
	}
	if got := notesOf(f.doc("task_comment", map[string]any{"id": tk.ID, "text": "on it"})); !strings.Contains(got, "[cut at 1000 characters") {
		t.Errorf("task_comment returned the notes whole: %d characters", len(got))
	}
}

// A claim on a task whose history is full (2,000 events: the lead's P1 rule) is refused with limit,
// and the tool tells the agent to leave the task to the operator, or to finish or release it if it
// holds it (the lead's ruling Q3).
func TestAClaimOnAFullHistoryIsLeftToTheOperator(t *testing.T) {
	f := newTaskFixture(t, taskSetup{})
	full := f.add("default", "claimed and released too often", true)
	stamp := time.Now().UTC().Format("2006-01-02T15:04:05Z")
	tx, err := f.Side.DB.Begin()
	if err != nil {
		t.Fatal(err)
	}
	for i := 1; i < 2000; i++ { // with its created event: 2,000
		if _, err := tx.Exec(`INSERT INTO task_events (task_id, at, actor, kind, from_status, to_status, note) VALUES (?, ?, 'bot', 'released', 'in_progress', 'ready', '')`,
			full.ID, stamp); err != nil {
			t.Fatal(err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if _, err := f.call("task_claim", map[string]any{"id": full.ID}); err == nil || !strings.HasPrefix(err.Error(), "limit: ") {
		t.Errorf("a claim on a full history: %v, want limit", err)
	}
	for _, tl := range taskTools() {
		if tl.name != "task_claim" {
			continue
		}
		for _, want := range []string{"leave the task to the operator", "finish or release it"} {
			if !strings.Contains(tl.description, want) {
				t.Errorf("task_claim's description does not say %q about limit", want)
			}
		}
	}
}

// A server whose profile was deleted since it started refuses with invalid_input "unknown profile"
// (Ruling 8): the board went with the profile.
func TestAServerWhoseProfileWasDeletedRefuses(t *testing.T) {
	f := newTaskFixture(t, taskSetup{})
	work := f.server(taskSetup{profile: workProfileName}, "bbbb")
	f.add(workProfileID, "theirs", true)
	mustCall(t, work, "task_list", nil) // the server resolves its profile at its first call
	if _, err := f.Side.DB.Exec(`DELETE FROM profiles WHERE id = ?`, workProfileID); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		name string
		args map[string]any
	}{{"task_list", nil}, {"task_claim", map[string]any{"next": true}}} {
		_, err := callAPITool(t, work, c.name, c.args)
		if err == nil || !strings.HasPrefix(err.Error(), "invalid_input: ") || !strings.Contains(err.Error(), "unknown profile") {
			t.Errorf("%s after the profile was deleted: %v", c.name, err)
		}
	}
}

// Every task tool acts as an agent (spec 5.1). Each check below fails if the tools acted as the
// operator: the operator's tasks are cli tasks with no hourly limit, and the operator may comment
// on any task.
func TestTheTaskToolsActAsAnAgent(t *testing.T) {
	f := newTaskFixture(t, taskSetup{})
	ready := f.add("default", "approved", true)
	inbox := f.add("default", "not yet read", false)

	// What an agent sends cannot make it someone else, put a task elsewhere than Inbox or reach
	// another profile: no tool takes such an argument.
	for _, args := range []map[string]any{
		{"title": "x", "ready": true}, {"title": "x", "status": "ready"}, {"title": "x", "source": "os"},
		{"title": "x", "profile": workProfileID}, {"title": "x", "as": "you"},
	} {
		if _, err := f.call("task_add", args); err == nil || !strings.HasPrefix(err.Error(), "invalid_input: ") {
			t.Errorf("task_add %v: %v, want it refused", args, err)
		}
	}
	if n := f.count(`SELECT COUNT(*) FROM tasks`); n != 2 {
		t.Fatalf("refused calls added tasks: %d on the board", n)
	}

	d := f.doc("task_add", map[string]any{"title": "Found a bug", "notes": "in the parser"})
	if tk := d["task"].(map[string]any); tk["status"] != "inbox" || tk["source_kind"] != "agent" || tk["profile_id"] != "default" || d["created"] != true {
		t.Errorf("an agent's task: %v", d)
	}
	for i := 2; i <= tasks.AgentTasksPerHour; i++ {
		f.doc("task_add", map[string]any{"title": fmt.Sprintf("finding %d", i)})
	}
	if _, err := f.call("task_add", map[string]any{"title": "one too many"}); err == nil || !strings.HasPrefix(err.Error(), "limit: ") {
		t.Errorf("task %d of the hour: %v, want limit", tasks.AgentTasksPerHour+1, err)
	}

	for _, c := range []struct {
		name string
		args map[string]any
		code string
	}{
		{"task_claim", map[string]any{"id": inbox.ID}, "not_ready: "},
		{"task_comment", map[string]any{"id": ready.ID, "text": "a note"}, "not_claimant: "},
		{"task_finish", map[string]any{"id": ready.ID, "result": "done"}, "not_claimant: "},
		{"task_release", map[string]any{"id": ready.ID}, "not_claimant: "},
	} {
		if _, err := f.call(c.name, c.args); err == nil || !strings.HasPrefix(err.Error(), c.code) {
			t.Errorf("%s %v: %v, want %s", c.name, c.args, err, c.code)
		}
	}

	// Another session of the same client is another claimant.
	f.doc("task_claim", map[string]any{"id": ready.ID})
	other := f.server(taskSetup{}, "bbbb")
	for _, c := range []struct {
		name string
		args map[string]any
	}{
		{"task_comment", map[string]any{"id": ready.ID, "text": "mine now"}},
		{"task_finish", map[string]any{"id": ready.ID, "result": "done"}},
		{"task_release", map[string]any{"id": ready.ID}},
	} {
		if _, err := callAPITool(t, other, c.name, c.args); err == nil || !strings.HasPrefix(err.Error(), "not_claimant: ") {
			t.Errorf("another session's %s: %v, want not_claimant", c.name, err)
		}
	}
}

func TestTheTaskVerbsNeedAllowMutations(t *testing.T) {
	f := newTaskFixture(t, taskSetup{readOnly: true})
	ready := f.add("default", "approved", true)
	verbs := []string{"task_claim", "task_comment", "task_finish", "task_release", "task_add"}
	for _, name := range verbs {
		if _, err := f.call(name, map[string]any{"id": ready.ID}); err == nil || !strings.Contains(err.Error(), "--allow-mutations") {
			t.Errorf("%s on a read-only server: %v", name, err)
		}
	}
	if tk, _, err := f.Store.Get(context.Background(), "default", ready.ID); err != nil || tk.Status != tasks.StatusReady {
		t.Errorf("a refused call changed the task: %+v, %v", tk, err)
	}
	// None deletes anything, so a host need not ask as it does for a destructive call.
	for _, name := range verbs {
		for _, def := range toolDefinitions(true) {
			if def["name"] == name {
				if a, _ := def["annotations"].(map[string]bool); a["readOnlyHint"] || a["destructiveHint"] || len(a) != 2 {
					t.Errorf("%s: annotations %v, want readOnlyHint and destructiveHint false", name, def["annotations"])
				}
			}
		}
	}
	names := toolsListNames(t, f.Server) // last: it closes this server's database
	for _, name := range verbs {
		if names[name] {
			t.Errorf("a read-only server lists %s", name)
		}
	}
}
