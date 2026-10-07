package mcp

// The read tools of the task board (spec 8) through the tools themselves, where task_tools_test.go
// has the fixture and the first tests: what a list cuts and one task does not, the claim task_next
// offers, a server whose profile was deleted.

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/monoes/mono-agent/internal/tasks"
)

// A list cuts each task's notes where the view says and names task_get for the rest, since two
// hundred tasks with 64 KiB of notes each would fill a model's context; one task, by task_get or
// task_next, comes whole (plan ruling 2).
func TestTaskListCutsLongNotesWhereTaskGetAndTaskNextDoNot(t *testing.T) {
	f := newTaskFixture(t, taskSetup{})
	notes := strings.Repeat("0123456789", 150) // 1,500 characters
	tk, _, err := f.Store.Add(context.Background(), "default", tasks.AddInput{Title: "with long notes", Notes: notes, Ready: true}, tasks.Actor{Kind: tasks.Human})
	if err != nil {
		t.Fatal(err)
	}
	notesOf := func(task map[string]any) string {
		s, _ := task["notes_untrusted"].(string)
		return s
	}

	inList := notesOf(f.doc("task_list", nil)["tasks"].([]any)[0].(map[string]any))
	if !strings.HasPrefix(inList, notes[:listNotesRunes]) || strings.HasPrefix(inList, notes[:listNotesRunes+1]) || !strings.Contains(inList, "task_get") {
		t.Errorf("task_list's notes are %d characters, want the first %d and a notice that task_get has the rest", len(inList), listNotesRunes)
	}
	for name, args := range map[string]map[string]any{"task_get": {"id": tk.ID}, "task_next": nil} {
		if got := notesOf(f.doc(name, args)["task"].(map[string]any)); got != notes {
			t.Errorf("%s: the notes are %d characters, want all %d", name, len(got), len(notes))
		}
	}
}

// task_next offers what a claim would take: the top of Ready, else a claim whose lease has run out,
// shown as the claim is (a plain by, stale: true). A claim that is still held is offered to nobody.
// The rows are written as another agent's claim leaves them.
func TestTaskNextOffersAStaleClaimOfAnotherAgentAndNeverALiveOne(t *testing.T) {
	f := newTaskFixture(t, taskSetup{})
	tk := f.add("default", "held by another agent", true)
	holdUntil := func(until time.Time) {
		t.Helper()
		if _, err := f.Side.DB.Exec(`UPDATE tasks SET status = 'in_progress', claimed_by = 'agent:other#1111', claim_until = ? WHERE id = ?`,
			until.UTC().Format("2006-01-02T15:04:05Z"), tk.ID); err != nil {
			t.Fatal(err)
		}
	}

	holdUntil(time.Now().Add(time.Hour))
	if d := f.doc("task_next", nil); d["task"] != nil {
		t.Errorf("a claim that is held for another hour was offered: %v", d["task"])
	}

	holdUntil(time.Now().Add(-time.Hour))
	d := f.doc("task_next", nil)
	if taskOf(d) != tk.ID {
		t.Fatalf("a claim whose lease ran out was not offered: %v", d)
	}
	if claim, _ := d["task"].(map[string]any)["claim"].(map[string]any); claim["by"] != "agent:other#1111" || claim["stale"] != true {
		t.Errorf("claim = %v, want by agent:other#1111 and stale true", claim)
	}
	if cur, _, err := f.Store.Get(context.Background(), "default", tk.ID); err != nil || cur.Status != tasks.StatusInProgress || cur.Claim == nil || cur.Claim.By != "agent:other#1111" {
		t.Errorf("looking took the claim: %+v, %v", cur, err)
	}
}

// A server's profile can be deleted while it runs. Every call then ends in the same refusal, the
// store's "unknown profile" as invalid_input (spec 4.2 says not_found; the store answers invalid_input
// for every verb, and the tools pass its code on), and that says nothing of the tasks the board held.
func TestTaskToolsRefuseTheProfileOfAServerThatWasDeleted(t *testing.T) {
	f := newTaskFixture(t, taskSetup{profile: workProfileName})
	const title = "title of a board that is gone"
	held := f.add(workProfileID, title, true)
	if got := listed(f.doc("task_list", nil)); !idsAre(got, held.ID) {
		t.Fatalf("before its profile is deleted the server serves its board: %v", got)
	}
	if _, err := f.Side.DB.Exec(`DELETE FROM profiles WHERE id = ?`, workProfileID); err != nil {
		t.Fatal(err)
	}
	for name, args := range map[string]map[string]any{"task_list": nil, "task_get": {"id": held.ID}, "task_next": nil} {
		_, err := f.call(name, args)
		if err == nil || !strings.HasPrefix(err.Error(), "invalid_input: ") || !strings.Contains(err.Error(), "unknown profile") || strings.Contains(err.Error(), title) {
			t.Errorf("%s after its profile was deleted: %v, want invalid_input: unknown profile, and no task text", name, err)
		}
	}
}
