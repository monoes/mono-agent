package summary

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/monoes/mono-agent/internal/tasks"
)

// The tasks section is one profile's board at a glance (task board spec, section 9). Its stale
// claims are judged by the wall clock, the store's only clock, so the fixtures use real times.
func TestTasksSection(t *testing.T) {
	db := testDB(t)
	exec(t, db, `INSERT INTO profiles (id, name) VALUES ('other', 'Other')`)
	store := tasks.NewStore(db.DB)
	ctx := context.Background()
	human, bot := tasks.Actor{Kind: tasks.Human}, tasks.Actor{Kind: tasks.Agent, Name: "bot"}
	add := func(profile, title string, ready bool) tasks.Task {
		t.Helper()
		tk, _, err := store.Add(ctx, profile, tasks.AddInput{Title: title, Ready: ready}, human)
		if err != nil {
			t.Fatal(err)
		}
		return tk
	}
	claim := func(tk tasks.Task) {
		t.Helper()
		if _, err := store.Claim(ctx, "default", tk.ID, bot, 0); err != nil {
			t.Fatal(err)
		}
	}
	section := func(profile string) *TasksSection {
		t.Helper()
		return Build(ctx, Options{DB: db.DB, ProfileID: profile, Now: now, Sections: map[string]bool{"tasks": true}}).Tasks
	}

	if s := section("default"); s == nil || s.Error != "" || s.Next != nil || s.Inbox+s.Ready+s.InProgress+s.Review+s.Stale != 0 {
		t.Fatalf("an empty board: %+v", s)
	}
	if b, _ := json.Marshal(section("default")); !strings.Contains(string(b), `"next":null`) {
		t.Errorf("no next task is null, not left out: %s", b)
	}

	add("default", "captured", false)
	top := add("default", "top of ready", true)
	second := add("default", "second", true)
	held := add("default", "held", true)
	stale := add("default", "stale", true)
	reviewed := add("default", "reviewed", true)
	add("other", "another profile's", true)
	claim(held)
	claim(stale)
	claim(reviewed)
	if _, err := store.Finish(ctx, "default", reviewed.ID, tasks.Outcome{Result: "done"}, bot); err != nil {
		t.Fatal(err)
	}
	exec(t, db, fmt.Sprintf(`UPDATE tasks SET claim_until = '2000-01-01T00:00:00Z' WHERE id = %d`, stale.ID))

	s := section("default")
	if s.Error != "" || s.Inbox != 1 || s.Ready != 2 || s.InProgress != 2 || s.Review != 1 || s.Stale != 1 {
		t.Errorf("counts = %+v", s)
	}
	if s.Next == nil || s.Next.ID != top.ID || s.Next.Title != "top of ready" {
		t.Errorf("next = %+v, want the top of Ready #%d", s.Next, top.ID)
	}
	claim(top)
	claim(second)
	if s := section("default"); s.Next == nil || s.Next.ID != stale.ID {
		t.Errorf("with nothing ready, next = %+v, want the stale claim #%d, as `task next` shows", s.Next, stale.ID)
	}
	if s := section("other"); s.Ready != 1 || s.Inbox != 0 || s.InProgress != 0 || s.Next == nil {
		t.Errorf("the other profile's board: %+v", s)
	}
	if s := Build(ctx, Options{Sections: map[string]bool{"tasks": true}}).Tasks; s.Error != noDB {
		t.Errorf("without a database: %+v", s)
	}
}
