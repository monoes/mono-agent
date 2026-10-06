package tasks

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func TestAddLandsInInboxAndRecordsIt(t *testing.T) {
	s, db, c := newTestStore(t)
	task, created, err := s.Add(bg, "default", AddInput{Title: "  Fix   the flaky test  "}, human)
	if err != nil || !created {
		t.Fatalf("add: created %v, err %v", created, err)
	}
	if task.ID == 0 || task.Status != StatusInbox || task.Title != "Fix the flaky test" || task.Source.Kind != SourceCLI || task.ProfileID != "default" {
		t.Errorf("task: %+v", task)
	}
	if !task.CreatedAt.Equal(c.t) || task.Claim != nil {
		t.Errorf("times or claim: %+v", task)
	}
	if task.LastEvent == nil || task.LastEvent.Kind != "created" || task.LastEvent.Actor != "you" {
		t.Errorf("last event: %+v", task.LastEvent)
	}
	if n := countWhere(t, db, "task_events", "task_id = ? AND kind = 'created' AND to_status = 'inbox'", task.ID); n != 1 {
		t.Errorf("created events: %d", n)
	}
	var rev int
	if err := db.QueryRow(`SELECT rev FROM task_board_rev WHERE profile_id = 'default'`).Scan(&rev); err != nil || rev != 1 {
		t.Errorf("revision %d, err %v", rev, err)
	}
}

func TestAddReadyIsForTheOperatorOnly(t *testing.T) {
	s, _, _ := newTestStore(t)
	task, _, err := s.Add(bg, "default", AddInput{Title: "go", Ready: true}, human)
	if err != nil || task.Status != StatusReady {
		t.Fatalf("operator with Ready: %+v, %v", task, err)
	}
	for _, a := range []Actor{bot("b"), {Kind: Capture, Name: SourceOS}} {
		if _, _, err := s.Add(bg, "default", AddInput{Title: "go", Ready: true, SourceKind: SourceOS}, a); !errors.Is(err, ErrOperatorOnly) {
			t.Errorf("%+v adding to Ready: %v, want ErrOperatorOnly", a, err)
		}
	}
}

func TestSourceKindRules(t *testing.T) {
	cases := []struct {
		name      string
		actor     Actor
		requested string
		want      string // "" means refused
	}{
		{"operator default", human, "", SourceCLI},
		{"operator cli", human, SourceCLI, SourceCLI},
		{"operator app", human, SourceApp, SourceApp},
		{"operator may not claim chrome", human, SourceChrome, ""},
		{"agent default", bot("b"), "", SourceAgent},
		{"agent with the cli default", bot("b"), SourceCLI, SourceAgent},
		{"agent may not claim os", bot("b"), SourceOS, ""},
		{"agent may not claim chrome", bot("b"), SourceChrome, ""},
		{"os capture", Actor{Kind: Capture, Name: SourceOS}, SourceOS, SourceOS},
		{"chrome capture by name", Actor{Kind: Capture, Name: SourceChrome}, "", SourceChrome},
		{"capture claiming cli", Actor{Kind: Capture, Name: SourceOS}, SourceCLI, ""},
	}
	s, _, _ := newTestStore(t)
	for _, c := range cases {
		task, _, err := s.Add(bg, "default", AddInput{Title: "t", SourceKind: c.requested}, c.actor)
		if c.want == "" {
			if !errors.Is(err, ErrInvalid) {
				t.Errorf("%s: err %v, want ErrInvalid", c.name, err)
			}
			continue
		}
		if err != nil || task.Source.Kind != c.want {
			t.Errorf("%s: kind %q, err %v, want %q", c.name, task.Source.Kind, err, c.want)
		}
	}
}

func TestAddRefusesAnUnknownProfile(t *testing.T) {
	s, db, _ := newTestStore(t)
	if _, _, err := s.Add(bg, "no-such-profile", AddInput{Title: "t"}, human); !errors.Is(err, ErrInvalid) {
		t.Fatalf("unknown profile: %v, want ErrInvalid", err)
	}
	if n := countWhere(t, db, "tasks", "1 = 1"); n != 0 {
		t.Errorf("%d tasks were written", n)
	}
}

func TestAddRefusesTextThatCleansToNothing(t *testing.T) {
	s, db, _ := newTestStore(t)
	for _, in := range []AddInput{
		{Text: " \x1b\x00 \t\n"},
		{Title: "\x07 \x1b"},
		{Text: "\U000E0049\U0000202e"},
		{},
	} {
		if _, _, err := s.Add(bg, "default", in, human); !errors.Is(err, ErrInvalid) {
			t.Errorf("%+v: err %v, want ErrInvalid", in, err)
		}
	}
	if n := countWhere(t, db, "tasks", "1 = 1"); n != 0 {
		t.Errorf("%d empty tasks were stored", n)
	}
}

func TestAddCleansWhatItStores(t *testing.T) {
	s, _, _ := newTestStore(t)
	chrome := Actor{Kind: Capture, Name: SourceChrome}
	task, _, err := s.Add(bg, "default", AddInput{
		Text:        "Reply to Sam\x1b[31m\nabout the invoice",
		SourceKind:  SourceChrome,
		SourceURL:   "https://user:pw@example.com/mail?id=7",
		SourceTitle: "  Inbox \n (3)  ",
		SourceApp:   "Google Chrome",
	}, chrome)
	if err != nil {
		t.Fatal(err)
	}
	if task.Title != "Reply to Sam[31m" || strings.ContainsRune(task.Notes, 0x1b) {
		t.Errorf("title %q notes %q: the escape byte must be gone", task.Title, task.Notes)
	}
	if !strings.Contains(task.Notes, "about the invoice") {
		t.Errorf("notes %q must keep the whole text", task.Notes)
	}
	if task.Source.URL != "https://example.com/mail?id=7" || task.Source.Title != "Inbox (3)" || task.Source.App != "Google Chrome" {
		t.Errorf("source %+v", task.Source)
	}
}

func TestClientIDMakesAddIdempotent(t *testing.T) {
	s, db, _ := newTestStore(t)
	other := addProfile(t, db, "p2")
	chrome := Actor{Kind: Capture, Name: SourceChrome}
	in := AddInput{Title: "once", ClientID: "c-1", SourceKind: SourceChrome}
	first, created, err := s.Add(bg, "default", in, chrome)
	if err != nil || !created {
		t.Fatalf("first: created %v, err %v", created, err)
	}
	second, created, err := s.Add(bg, "default", in, chrome)
	if err != nil || created || second.ID != first.ID {
		t.Fatalf("second: %+v created %v err %v, want the first task back", second, created, err)
	}
	if n := countWhere(t, db, "tasks", "profile_id = 'default'"); n != 1 {
		t.Errorf("%d tasks in the profile", n)
	}
	third, created, err := s.Add(bg, other, in, chrome)
	if err != nil || !created || third.ID == first.ID {
		t.Errorf("the same key in another profile: %+v created %v err %v", third, created, err)
	}
	if _, _, err := s.Add(bg, "default", AddInput{Title: "x", ClientID: "bad id!", SourceKind: SourceChrome}, chrome); !errors.Is(err, ErrInvalid) {
		t.Errorf("a malformed client id: %v", err)
	}
}

func TestAgentsMayAddTwentyTasksAnHour(t *testing.T) {
	s, _, c := newTestStore(t)
	add := func() error {
		_, _, err := s.Add(bg, "default", AddInput{Title: "from an agent"}, bot("b"))
		return err
	}
	for i := 0; i < AgentTasksPerHour; i++ {
		if err := add(); err != nil {
			t.Fatalf("task %d: %v", i+1, err)
		}
	}
	if err := add(); !errors.Is(err, ErrLimit) {
		t.Fatalf("task 21: %v, want ErrLimit", err)
	}
	if _, _, err := s.Add(bg, "default", AddInput{Title: "operator"}, human); err != nil {
		t.Errorf("the operator is not limited: %v", err)
	}
	c.advance(61 * time.Minute)
	if err := add(); err != nil {
		t.Errorf("an hour later: %v", err)
	}
}

func TestAProfileHoldsAtMostTwoThousandOpenTasks(t *testing.T) {
	s, db, _ := newTestStore(t)
	seedTasks(t, db, "default", MaxOpenTasks)
	if _, _, err := s.Add(bg, "default", AddInput{Title: "one too many"}, human); !errors.Is(err, ErrLimit) {
		t.Fatalf("task 2001: %v, want ErrLimit", err)
	}
	if _, err := db.Exec(`UPDATE tasks SET status = 'archived' WHERE id = (SELECT MIN(id) FROM tasks)`); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.Add(bg, "default", AddInput{Title: "room again"}, human); err != nil {
		t.Errorf("archived tasks do not count: %v", err)
	}
}

func TestNewTasksGoToTheTopOrBottomOfTheirColumn(t *testing.T) {
	s, _, _ := newTestStore(t)
	a, b := mustAdd(t, s, "default", "a", false), mustAdd(t, s, "default", "b", false)
	if b.Position >= a.Position {
		t.Errorf("inbox is newest first: a=%d b=%d", a.Position, b.Position)
	}
	r1, r2 := mustAdd(t, s, "default", "r1", true), mustAdd(t, s, "default", "r2", true)
	if r2.Position <= r1.Position {
		t.Errorf("ready is a queue: r1=%d r2=%d", r1.Position, r2.Position)
	}
}

func TestTaskTextIsStoredVerbatim(t *testing.T) {
	s, db, _ := newTestStore(t)
	title := `x'); DROP TABLE tasks; --`
	task := mustAdd(t, s, "default", title, false)
	var got string
	if err := db.QueryRow(`SELECT title FROM tasks WHERE id = ?`, task.ID).Scan(&got); err != nil || got != title {
		t.Fatalf("title %q, err %v", got, err)
	}
}
