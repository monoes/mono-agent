//go:build !windows

package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/monoes/mono-agent/internal/orgsign"
	"github.com/monoes/mono-agent/internal/tasks"
)

// addTestTask adds a task to a profile's board in process, as the operator.
func addTestTask(t *testing.T, a *App, profileID, title string) {
	t.Helper()
	_, _, err := tasks.NewStore(a.db).Add(context.Background(), profileID, tasks.AddInput{Title: title}, tasks.Actor{Kind: tasks.Human})
	if err != nil {
		t.Fatal(err)
	}
}

// taskCLIFake installs a monoagentcli stand-in that logs each call as one
// line with every argument followed by "|" (so where an argument ends shows),
// saves its standard input, prints out and exits with code. It returns the
// paths of the log and of the saved standard input.
func taskCLIFake(t *testing.T, out string, code int) (argsLog, stdinFile string) {
	t.Helper()
	dir := t.TempDir()
	argsLog = filepath.Join(dir, "args.log")
	stdinFile = filepath.Join(dir, "stdin")
	outFile := filepath.Join(dir, "out.json")
	if err := os.WriteFile(outFile, []byte(out), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MONOAGENTCLI_BIN", fakeCLI(t,
		"for a in \"$@\"; do printf '%s|' \"$a\"; done >> '"+argsLog+"'\n"+
			"echo >> '"+argsLog+"'\n"+
			"cat > '"+stdinFile+"'\n"+
			"cat '"+outFile+"'\n"+
			"exit "+strconv.Itoa(code)+"\n"))
	return argsLog, stdinFile
}

func newTaskTestApp(t *testing.T) *App {
	t.Helper()
	a := newTestApp(t)
	a.ctx = context.Background()
	a.setActiveProfileID("work")
	return a
}

// Every binding that runs the CLI runs `task …` for the active profile with
// its arguments exactly so: user text after "--" or in the --flag=value form,
// so text that starts with a dash is never read as a flag. Each returns
// stdout verbatim.
func TestTaskBindingsPassTheirArgumentsExactly(t *testing.T) {
	log, _ := taskCLIFake(t, `{"ok":true}`, 0)
	a := newTaskTestApp(t)
	for i, got := range []string{
		a.TaskShow(12),
		a.TaskAdd(`{"title":"-x fix the bug","notes":"--help me"}`),
		a.TaskAdd(`{"title":"urgent","ready":true}`),
		a.TaskEdit(12, `{"title":"--new"}`),
		a.TaskEdit(12, `{"notes":""}`),
		a.TaskMove(12, "ready", "", 0),
		a.TaskMove(12, "done", "top", 0),
		a.TaskMove(12, "inbox", "bottom", 0),
		a.TaskMove(12, "review", "before", 9),
		a.TaskMove(12, "in_progress", "after", 9),
		a.TaskApprove([]int64{3, 4}, false),
		a.TaskApprove([]int64{3}, true),
		a.TaskArchive([]int64{5}),
		a.TaskUnarchive([]int64{5, 6}),
		a.TaskComment(12, "-looks odd"),
	} {
		if got != `{"ok":true}` {
			t.Fatalf("call %d did not return the CLI's stdout verbatim: %q", i, got)
		}
	}
	p := "--profile|work|--json|task|"
	want := []string{
		p + "show|12|",
		p + "add|--source|app|--notes=--help me|--|-x fix the bug|",
		p + "add|--source|app|--ready|--|urgent|",
		p + "edit|12|--title=--new|",
		p + "edit|12|--notes=|",
		p + "move|12|ready|",
		p + "move|12|done|--top|",
		p + "move|12|inbox|--bottom|",
		p + "move|12|review|--before|9|",
		p + "move|12|in_progress|--after|9|",
		p + "approve|3|4|",
		p + "approve|3|--top|",
		p + "archive|5|",
		p + "unarchive|5|6|",
		p + "comment|12|--|-looks odd|",
	}
	if got := loggedArgs(t, log); strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("CLI calls:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

// Text of several lines goes on standard input: the CLI makes its first
// line the title and all of it the notes (spec 4.6).
func TestTaskAddSendsTextOnStandardInput(t *testing.T) {
	log, stdin := taskCLIFake(t, `{"created":true}`, 0)
	a := newTaskTestApp(t)
	text := "Reply to Sam\n--about the invoice"
	spec, _ := json.Marshal(map[string]any{"text": text, "ready": true})
	if got := a.TaskAdd(string(spec)); got != `{"created":true}` {
		t.Fatalf("TaskAdd = %q", got)
	}
	if got := loggedArgs(t, log); len(got) != 1 || got[0] != "--profile|work|--json|task|add|--source|app|--ready|--stdin|" {
		t.Fatalf("CLI call: %q", got)
	}
	if raw, err := os.ReadFile(stdin); err != nil || string(raw) != text {
		t.Fatalf("standard input %q (%v), want %q", raw, err, text)
	}
}

// Input no command could accept is refused here, as {"error","code"}, and
// the CLI never runs.
func TestTaskBindingsRefuseBadInputWithoutRunningTheCLI(t *testing.T) {
	log, _ := taskCLIFake(t, `{"ok":true}`, 0)
	a := newTaskTestApp(t)
	long := `{"notes":"` + strings.Repeat("x", maxArgNotes+1) + `"}`
	longAdd := `{"title":"a","notes":"` + strings.Repeat("x", maxArgNotes+1) + `"}`
	for name, got := range map[string]string{
		"a title of spaces":   a.TaskAdd(`{"title":"  "}`),
		"title and text":      a.TaskAdd(`{"title":"a","text":"b"}`),
		"notes with text":     a.TaskAdd(`{"text":"b","notes":"n"}`),
		"long notes on add":   a.TaskAdd(longAdd),
		"not JSON":            a.TaskAdd(`{`),
		"an edit of nothing":  a.TaskEdit(3, `{}`),
		"edit id 0":           a.TaskEdit(0, `{"title":"x"}`),
		"notes over the cap":  a.TaskEdit(3, long),
		"show id -1":          a.TaskShow(-1),
		"show id 0":           a.TaskShow(0),
		"move id 0":           a.TaskMove(0, "ready", "", 0),
		"move to archived":    a.TaskMove(3, "archived", "", 0),
		"an unknown place":    a.TaskMove(3, "ready", "middle", 0),
		"before no card":      a.TaskMove(3, "ready", "before", 0),
		"after itself":        a.TaskMove(3, "ready", "after", 3),
		"approve nothing":     a.TaskApprove(nil, false),
		"archive id 0":        a.TaskArchive([]int64{0}),
		"unarchive nothing":   a.TaskUnarchive([]int64{}),
		"a comment of spaces": a.TaskComment(3, " \n"),
		"a comment on id 0":   a.TaskComment(0, "x"),
	} {
		var doc struct{ Error, Code string }
		if err := json.Unmarshal([]byte(got), &doc); err != nil || doc.Error == "" || doc.Code != "invalid_input" {
			t.Errorf("%s: %q, want an invalid_input refusal", name, got)
		}
	}
	if _, err := os.Stat(log); !os.IsNotExist(err) {
		t.Fatalf("the CLI ran for refused input: %v", loggedArgs(t, log))
	}
}

// A refusal comes back as the CLI printed it, code and all, so the page can
// tell operator_only from claimed or not_found.
func TestTaskBindingsReturnTheCLIRefusalVerbatim(t *testing.T) {
	refusal := `{"code":"operator_only","error":"CLAUDECODE is set, so an agent is running this command: only the operator can move a task"}`
	taskCLIFake(t, refusal, 3)
	a := newTaskTestApp(t)
	if got := a.TaskMove(7, "ready", "", 0); got != refusal {
		t.Fatalf("TaskMove = %q", got)
	}
}

// The board is read in process, in the document `task board --json` prints,
// because the CLI refuses `task board` to an agent-driven caller: under an
// inherited CLAUDECODE the app still shows its board, while an action still
// runs the CLI, whose guard refuses it, and the page shows the banner. The
// store refuses a whole-board read to every actor but the operator, so the
// board coming back here means the binding asks as the operator: the marker
// says what started the app, not who is looking at its window.
func TestTaskBoardIsReadInProcessEvenUnderAnInheritedMarker(t *testing.T) {
	refusal := `{"code":"operator_only","error":"CLAUDECODE is set, so an agent is running this command: only the operator can move a task"}`
	log, _ := taskCLIFake(t, refusal, 3)
	t.Setenv("CLAUDECODE", "1")
	a := newTestApp(t)
	a.ctx = context.Background()
	addTestTask(t, a, "default", "first")
	addTestTask(t, a, "default", "second")
	// An agent asking for the same board is refused, so the board below was
	// asked for as the operator, not as the actor the marker would make.
	if _, err := tasks.NewStore(a.db).Board(a.ctx, "default", 0, tasks.Actor{Kind: tasks.Agent, Name: "bot"}); !errors.Is(err, tasks.ErrOperatorOnly) {
		t.Fatalf("the store's board for an agent = %v, want ErrOperatorOnly", err)
	}
	raw := a.TaskBoard(0)
	var top map[string]json.RawMessage
	if err := json.Unmarshal([]byte(raw), &top); err != nil || len(top) != 4 || top["profile"] == nil || top["rev"] == nil || top["counts"] == nil || top["tasks"] == nil {
		t.Fatalf("TaskBoard = %s (%v), want the four keys profile, rev, counts, tasks", raw, err)
	}
	var doc struct {
		Profile struct{ ID string }          `json:"profile"`
		Rev     int64                        `json:"rev"`
		Counts  struct{ Inbox int }          `json:"counts"`
		Tasks   map[string][]json.RawMessage `json:"tasks"`
	}
	if err := json.Unmarshal([]byte(raw), &doc); err != nil {
		t.Fatal(err)
	}
	if doc.Profile.ID != "default" || doc.Rev != 2 || doc.Counts.Inbox != 2 || len(doc.Tasks) != 5 || len(doc.Tasks["inbox"]) != 2 || doc.Tasks["done"] == nil {
		t.Fatalf("board = %+v (empty columns must be [], never null)", doc)
	}
	if _, err := os.Stat(log); !os.IsNotExist(err) {
		t.Fatalf("the board read ran the CLI: %v", loggedArgs(t, log))
	}
	if got := a.TaskMove(1, "ready", "", 0); got != refusal {
		t.Fatalf("a move under the marker = %q, want the CLI's operator_only refusal", got)
	}
	if got := a.TaskAgentShell(); got != "CLAUDECODE" {
		t.Fatalf("TaskAgentShell = %q: the page would not show the read-only banner", got)
	}
}

// The app never asks for every Done card: a limit that is not positive is
// the board's 50 (Store.Board reads 0 as all of them).
func TestTaskBoardLimitIsNeverEveryDoneCard(t *testing.T) {
	for in, want := range map[int]int{0: 50, -3: 50, 10: 10, 50: 50} {
		if got := boardLimit(in); got != want {
			t.Errorf("boardLimit(%d) = %d, want %d", in, got, want)
		}
	}
}

// The app's children inherit its environment: the binding names the agent
// marker (or MONOAGENT_ACTOR) the CLI would see, so the page can go
// read-only before anything is refused.
func TestTaskAgentShellNamesTheMarkerTheAppInherited(t *testing.T) {
	for _, m := range orgsign.AgentContextMarkers() {
		t.Setenv(m, "")
	}
	t.Setenv("MONOAGENT_ACTOR", "")
	a := newTaskTestApp(t)
	if got := a.TaskAgentShell(); got != "" {
		t.Fatalf("no marker: %q", got)
	}
	t.Setenv("MONOAGENT_ACTOR", "bot")
	if got := a.TaskAgentShell(); got != "MONOAGENT_ACTOR" {
		t.Fatalf("MONOAGENT_ACTOR set: %q", got)
	}
	// A value of only spaces is an agent that gave no name, to the CLI too
	// (spec D7: it refuses every operator action then); only an empty one,
	// as at the top, is as good as none.
	for _, blank := range []string{" ", "\t"} {
		t.Setenv("MONOAGENT_ACTOR", blank)
		if got := a.TaskAgentShell(); got != "MONOAGENT_ACTOR" {
			t.Fatalf("MONOAGENT_ACTOR %q: %q, want MONOAGENT_ACTOR", blank, got)
		}
	}
	t.Setenv("CLAUDECODE", "1")
	if got := a.TaskAgentShell(); got != "CLAUDECODE" {
		t.Fatalf("CLAUDECODE set: %q", got)
	}
}

// The notes cap is in characters, as its refusal says, not in bytes: notes of
// two- or three-byte letters are not refused at half or a third of it. Just
// under the cap goes to the CLI whole, just over is refused and the CLI never
// runs.
func TestTaskBindingsNotesLimitCountsCharacters(t *testing.T) {
	for _, c := range []struct{ name, letter string }{
		{"two bytes a letter", "\U000000e9"},
		{"three bytes a letter", "\U00003042"},
	} {
		t.Run(c.name, func(t *testing.T) {
			log, _ := taskCLIFake(t, `{"ok":true}`, 0)
			a := newTaskTestApp(t)
			under, over := strings.Repeat(c.letter, maxArgNotes), strings.Repeat(c.letter, maxArgNotes+1)
			spec := func(fields map[string]string) string {
				raw, err := json.Marshal(fields)
				if err != nil {
					t.Fatal(err)
				}
				return string(raw)
			}
			if got := a.TaskAdd(spec(map[string]string{"title": "t", "notes": under})); got != `{"ok":true}` {
				t.Fatalf("add with %d characters of notes: %.200q, want the CLI's answer", maxArgNotes, got)
			}
			if got := a.TaskEdit(12, spec(map[string]string{"notes": under})); got != `{"ok":true}` {
				t.Fatalf("edit with %d characters of notes: %.200q, want the CLI's answer", maxArgNotes, got)
			}
			p := "--profile|work|--json|task|"
			want := []string{p + "add|--source|app|--notes=" + under + "|--|t|", p + "edit|12|--notes=" + under + "|"}
			if got := loggedArgs(t, log); len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
				t.Fatalf("the CLI did not get the notes whole: %d calls logged", len(got))
			}
			for name, got := range map[string]string{
				"add":  a.TaskAdd(spec(map[string]string{"title": "t", "notes": over})),
				"edit": a.TaskEdit(12, spec(map[string]string{"notes": over})),
			} {
				var doc struct{ Error, Code string }
				if err := json.Unmarshal([]byte(got), &doc); err != nil || doc.Error != notesTooLong || doc.Code != "invalid_input" {
					t.Errorf("%s with %d characters of notes: %.200q, want the notes refusal", name, maxArgNotes+1, got)
				}
			}
			if got := loggedArgs(t, log); len(got) != 2 {
				t.Fatalf("the CLI ran for notes over the cap: %d calls logged", len(got))
			}
		})
	}
}

// taskBoardView is the part of the board document the board tests read.
type taskBoardView struct {
	Profile struct{ ID string }
	Counts  struct{ Inbox, Done int }
	Tasks   map[string][]struct{ Title string }
}

func decodeTaskBoard(t *testing.T, raw string) taskBoardView {
	t.Helper()
	var v taskBoardView
	if err := json.Unmarshal([]byte(raw), &v); err != nil || v.Profile.ID == "" {
		t.Fatalf("TaskBoard = %.300s (%v), want a board document", raw, err)
	}
	return v
}

// titles names a column's cards, sorted and joined, so a test says which cards
// a column holds without pinning their order.
func (v taskBoardView) titles(column string) string {
	var out []string
	for _, c := range v.Tasks[column] {
		out = append(out, c.Title)
	}
	sort.Strings(out)
	return strings.Join(out, ",")
}

// The board is the active profile's, read when it is asked for: one app
// answers for whichever profile is active at the moment, and for no other.
func TestTaskBoardIsTheActiveProfilesBoard(t *testing.T) {
	a := newTestApp(t)
	a.ctx = context.Background()
	if _, err := a.db.Exec(`INSERT INTO profiles (id, name) VALUES ('work', 'Work')`); err != nil {
		t.Fatal(err)
	}
	addTestTask(t, a, "default", "at home")
	addTestTask(t, a, "work", "for the client")
	addTestTask(t, a, "work", "for the audit")
	for _, c := range []struct{ profile, inbox string }{
		{"default", "at home"},
		{"work", "for the audit,for the client"},
		{"default", "at home"},
	} {
		a.setActiveProfileID(c.profile)
		v := decodeTaskBoard(t, a.TaskBoard(0))
		if v.Profile.ID != c.profile || v.titles("inbox") != c.inbox {
			t.Errorf("active profile %q: the board of %q with Inbox %q, want Inbox %q", c.profile, v.Profile.ID, v.titles("inbox"), c.inbox)
		}
	}
}

// Done shows the cards asked for, the most recent first, and the board's 50
// when no limit is asked for (Store.Board would read 0 as every card); the
// count still says how many there are.
func TestTaskBoardCutsDoneToTheLimit(t *testing.T) {
	a := newTestApp(t)
	a.ctx = context.Background()
	store, human := tasks.NewStore(a.db), tasks.Actor{Kind: tasks.Human}
	for i := 1; i <= 52; i++ {
		card, _, err := store.Add(a.ctx, "default", tasks.AddInput{Title: fmt.Sprintf("card %d", i)}, human)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := store.Move(a.ctx, "default", card.ID, tasks.StatusDone, tasks.Placement{}, human); err != nil {
			t.Fatal(err)
		}
	}
	for _, c := range []struct{ asked, shown int }{{0, 50}, {-3, 50}, {2, 2}, {60, 52}} {
		v := decodeTaskBoard(t, a.TaskBoard(c.asked))
		if len(v.Tasks["done"]) != c.shown || v.Counts.Done != 52 {
			t.Errorf("TaskBoard(%d): %d Done cards and counts.done %d, want %d and 52", c.asked, len(v.Tasks["done"]), v.Counts.Done, c.shown)
		}
	}
	v := decodeTaskBoard(t, a.TaskBoard(2))
	if len(v.Tasks["done"]) != 2 || v.Tasks["done"][0].Title != "card 52" || v.Tasks["done"][1].Title != "card 51" {
		t.Errorf("TaskBoard(2) Done = %+v, want the two most recent: card 52, then card 51", v.Tasks["done"])
	}
}

// Before startup has opened the database the board answers with an error
// document, and does not panic on the missing database.
func TestTaskBoardBeforeTheDatabaseIsOpen(t *testing.T) {
	raw := (&App{}).TaskBoard(0)
	var doc map[string]string
	if err := json.Unmarshal([]byte(raw), &doc); err != nil || len(doc) != 1 || doc["error"] == "" {
		t.Fatalf("TaskBoard without a database = %q (%v), want a lone {\"error\"} document", raw, err)
	}
}
