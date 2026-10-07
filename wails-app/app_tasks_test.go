//go:build !windows

package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
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
		"move to archived":    a.TaskMove(3, "archived", "", 0),
		"an unknown place":    a.TaskMove(3, "ready", "middle", 0),
		"before no card":      a.TaskMove(3, "ready", "before", 0),
		"after itself":        a.TaskMove(3, "ready", "after", 3),
		"approve nothing":     a.TaskApprove(nil, false),
		"archive id 0":        a.TaskArchive([]int64{0}),
		"unarchive nothing":   a.TaskUnarchive([]int64{}),
		"a comment of spaces": a.TaskComment(3, " \n"),
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
	t.Setenv("CLAUDECODE", "1")
	if got := a.TaskAgentShell(); got != "CLAUDECODE" {
		t.Fatalf("CLAUDECODE set: %q", got)
	}
}
