package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/monoes/mono-agent/internal/tasks"
)

// moreLine is what a list says when it left tasks out; n is "3", or "1500+" at the store's maximum.
func moreLine(n string) string {
	return fmt.Sprintf("... %s more (--limit shows more, up to %d)", n, tasks.MaxListLimit)
}

// A column the board cut says so under its cards, with the status that lists the rest, whichever
// column it is (today only Done is cut, by --done-limit).
func TestTaskPrintBoardNamesTheStatusOfEveryCutColumn(t *testing.T) {
	card := func(id int64, title string) tasks.Task { return tasks.Task{ID: id, Title: title} }
	var out bytes.Buffer
	printBoard(&out, tasks.Board{
		Profile: tasks.Profile{ID: "p", Name: "Work"},
		Rev:     9,
		Counts:  tasks.Counts{Inbox: 1, Ready: 4, InProgress: 3, Review: 2, Done: 7},
		Tasks: map[tasks.Status][]tasks.Task{
			tasks.StatusInbox:      {card(1, "a")},
			tasks.StatusReady:      {card(2, "b"), card(3, "c")},
			tasks.StatusInProgress: {card(4, "d")},
			tasks.StatusReview:     {card(5, "e"), card(6, "f")},
			tasks.StatusDone:       {card(7, "g"), card(8, "h")},
		},
	})
	want := `Profile: Work (revision 9)

INBOX (1)
  #1  a

READY (4)
  #2  b
  #3  c
  ... 2 more (task list --status ready)

IN PROGRESS (3)
  #4  d
  ... 2 more (task list --status in_progress)

REVIEW (2)
  #5  e
  #6  f

DONE (7)
  #7  g
  #8  h
  ... 5 more (task list --status done)
`
	if out.String() != want {
		t.Errorf("the board:\n%s\nwant:\n%s", out.String(), want)
	}
}

// A limit is the number of tasks shown, and the store's default when it is not above 0; one above
// what the store gives shows all there is, so it needs no clamp.
func TestTaskListWindowIsTheLimitOrTheStoresDefault(t *testing.T) {
	for _, c := range []struct{ limit, want int }{
		{0, tasks.DefaultListLimit}, {-1, tasks.DefaultListLimit}, {-1 << 30, tasks.DefaultListLimit},
		{1, 1}, {2, 2}, {tasks.DefaultListLimit, tasks.DefaultListLimit}, {tasks.DefaultListLimit + 1, tasks.DefaultListLimit + 1},
		{tasks.MaxListLimit, tasks.MaxListLimit}, {tasks.MaxListLimit + 1, tasks.MaxListLimit + 1}, {1 << 30, 1 << 30},
	} {
		if got := listWindow(c.limit); got != c.want {
			t.Errorf("listWindow(%d) = %d, want %d", c.limit, got, c.want)
		}
	}
}

func TestTaskCutListKeepsTheFirstTasksAndCountsTheRest(t *testing.T) {
	many := func(n int) []tasks.Task {
		ts := make([]tasks.Task, n)
		for i := range ts {
			ts[i].ID = int64(i + 1)
		}
		return ts
	}
	for _, c := range []struct {
		name         string
		have, shown  int
		wantKept     int
		wantMore     string
		wantLastKept int64
	}{
		{"nothing to show", 0, 5, 0, "", 0},
		{"fewer than the window", 3, 5, 3, "", 3},
		{"exactly the window", 5, 5, 5, "", 5},
		{"one more", 6, 5, 5, "1", 5},
		{"two more", 7, 5, 5, "2", 5},
		{"the default window and two more", 502, tasks.DefaultListLimit, tasks.DefaultListLimit, "2", tasks.DefaultListLimit},
		// The store gives 2000 at most: when it gave that many there may be more (the archive is not limited).
		{"just under what the store gives", tasks.MaxListLimit - 1, 500, 500, "1499", 500},
		{"all the store gives", tasks.MaxListLimit, 500, 500, "1500+", 500},
		{"all the store gives, all shown", tasks.MaxListLimit, tasks.MaxListLimit, tasks.MaxListLimit, "", tasks.MaxListLimit},
	} {
		kept, more := cutList(many(c.have), c.shown)
		var last int64
		if len(kept) > 0 {
			last = kept[len(kept)-1].ID
		}
		if len(kept) != c.wantKept || more != c.wantMore || last != c.wantLastKept {
			t.Errorf("%s: cutList(%d tasks, %d) kept %d (the last #%d) and says %q; want %d (#%d) and %q",
				c.name, c.have, c.shown, len(kept), last, more, c.wantKept, c.wantLastKept, c.wantMore)
		}
	}
}

func TestTaskPrintTaskTableEndsWithWhatItLeftOut(t *testing.T) {
	p := tasks.Profile{ID: "p", Name: "Work"}
	one := []tasks.Task{{ID: 1, Title: "a", Status: tasks.StatusInbox, Source: tasks.Source{Kind: "cli"}, CreatedAt: time.Now()}}
	var out bytes.Buffer
	printTaskTable(&out, p, one, "")
	if strings.Contains(out.String(), "...") {
		t.Errorf("a table that left nothing out: %q", out.String())
	}
	out.Reset()
	printTaskTable(&out, p, one, "3")
	lines := strings.Split(strings.TrimRight(out.String(), "\n"), "\n")
	if got := lines[len(lines)-1]; got != moreLine("3") {
		t.Errorf("the last line of a table that left 3 out is %q, want %q", got, moreLine("3"))
	}
}

// The list asks the store for all it gives and keeps the first tasks of --limit: it can then
// say how many it left out, and say nothing when it left none.
func TestTaskListSaysHowManyTasksItLeftOut(t *testing.T) {
	db := newTaskTestDB(t)
	seedTaskRows(t, db, taskSeed{title: "one"}, taskSeed{title: "two"}, taskSeed{title: "three"})
	for _, c := range []struct {
		name  string
		args  []string
		shown []string
		more  string // the line under the table, "" for none
	}{
		{"a limit above what there is", []string{"--limit", "5"}, []string{"one", "two", "three"}, ""},
		{"a limit of what there is", []string{"--limit", "3"}, []string{"one", "two", "three"}, ""},
		{"one over", []string{"--limit", "2"}, []string{"one", "two"}, moreLine("1")},
		{"two over", []string{"--limit", "1"}, []string{"one"}, moreLine("2")},
		{"no limit", nil, []string{"one", "two", "three"}, ""},
		{"an agent that names the inbox", []string{"--as", "bot", "--status", "inbox", "--limit", "2"}, []string{"one", "two"}, moreLine("1")},
		{"a filter that leaves the rest out", []string{"--status", "inbox", "--source", "cli", "--limit", "2"}, []string{"one", "two"}, moreLine("1")},
	} {
		args := append([]string{"list"}, c.args...)
		var got listJSON
		mustTaskJSON(t, db, "default", &got, "", args...)
		if !slices.Equal(titlesOf(got), c.shown) {
			t.Errorf("%s: list --json shows %q, want %q", c.name, titlesOf(got), c.shown)
		}
		raw, _, _ := runTask(t, db, "default", true, "", args...)
		var doc map[string]json.RawMessage
		if err := json.Unmarshal([]byte(raw), &doc); err != nil || len(doc) != 2 || doc["profile"] == nil || doc["tasks"] == nil {
			t.Errorf("%s: the JSON document is still {profile, tasks}: %v, %s", c.name, err, raw)
		}
		text, _, err := runTask(t, db, "default", false, "", args...)
		if err != nil {
			t.Fatal(err)
		}
		lines := strings.Split(strings.TrimRight(text, "\n"), "\n")
		last, rows := lines[len(lines)-1], len(lines)-2 // the profile line and the header
		if c.more != "" {
			rows--
		}
		if rows != len(c.shown) || (c.more != "" && last != c.more) || (c.more == "" && strings.Contains(text, "...")) {
			t.Errorf("%s: list as text has %d rows and ends with %q; want %d rows and %q:\n%s", c.name, rows, last, len(c.shown), c.more, text)
		}
	}
}

func TestTaskListSaysSoWhenTheDefaultLimitCutsIt(t *testing.T) {
	db := newTaskTestDB(t)
	rows := make([]taskSeed, 0, tasks.DefaultListLimit+2)
	for i := 0; i < tasks.DefaultListLimit+2; i++ {
		rows = append(rows, taskSeed{title: fmt.Sprintf("task %03d", i)})
	}
	seedTaskRows(t, db, rows...)
	for _, c := range []struct {
		args  []string
		shown int
		more  string
	}{
		{nil, tasks.DefaultListLimit, moreLine("2")},
		{[]string{"--limit", "0"}, tasks.DefaultListLimit, moreLine("2")},
		{[]string{"--limit", "-3"}, tasks.DefaultListLimit, moreLine("2")},
		{[]string{"--limit", "501"}, 501, moreLine("1")},
		{[]string{"--limit", "502"}, 502, ""},
		{[]string{"--limit", "100000"}, 502, ""},
	} {
		args := append([]string{"list"}, c.args...)
		var got listJSON
		mustTaskJSON(t, db, "default", &got, "", args...)
		text, _, err := runTask(t, db, "default", false, "", args...)
		if err != nil {
			t.Fatal(err)
		}
		lines := strings.Split(strings.TrimRight(text, "\n"), "\n")
		said := ""
		if strings.HasPrefix(lines[len(lines)-1], "...") {
			said = lines[len(lines)-1]
		}
		if len(got.Tasks) != c.shown || said != c.more || strings.Count(text, "\n#") != c.shown {
			t.Errorf("task %s: %d tasks in the JSON and %d rows in the text, and the last line %q; want %d and %q",
				strings.Join(args, " "), len(got.Tasks), strings.Count(text, "\n#"), said, c.shown, c.more)
			continue
		}
		if got.Tasks[0].Title != "task 000" || got.Tasks[len(got.Tasks)-1].Title != fmt.Sprintf("task %03d", c.shown-1) {
			t.Errorf("task %s keeps the first tasks of the order, not these: %q to %q", strings.Join(args, " "), got.Tasks[0].Title, got.Tasks[len(got.Tasks)-1].Title)
		}
	}
}

// The store never gives more than MaxListLimit tasks, and the Archive is not limited: a list of
// it that came back full says so with a plus, instead of a count that may be too small.
func TestTaskListOfAFullArchiveSaysThereMayBeMore(t *testing.T) {
	db := newTaskTestDB(t)
	rows := make([]taskSeed, 0, tasks.MaxListLimit+1)
	for i := 0; i <= tasks.MaxListLimit; i++ {
		rows = append(rows, taskSeed{title: fmt.Sprintf("old %04d", i), status: "archived"})
	}
	seedTaskRows(t, db, rows...)
	text, _, err := runTask(t, db, "default", false, "", "list", "--status", "archived")
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimRight(text, "\n"), "\n")
	if last := lines[len(lines)-1]; last != moreLine("1500+") || len(lines) != 2+tasks.DefaultListLimit+1 {
		t.Errorf("a full archive: %d lines and the last is %q, want %d lines and %q", len(lines), last, 2+tasks.DefaultListLimit+1, moreLine("1500+"))
	}
	var got listJSON
	mustTaskJSON(t, db, "default", &got, "", "list", "--status", "archived", "--limit", "2000")
	if len(got.Tasks) != tasks.MaxListLimit {
		t.Errorf("--limit 2000: %d tasks", len(got.Tasks))
	}
}
