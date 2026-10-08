// wails-app/app_tasks.go
//
// The Tasks tab's bindings (docs/mastermind/specs/2026-10-05-task-board-design.md
// §10). Each action runs `monoagentcli --profile <active> --json task …` and
// returns its stdout verbatim: the document, or the CLI's {"error","code"} on
// a refusal, so the page can tell operator_only from claimed or not_found.
// The board is the one read done in process (TaskBoard), as the watcher
// (app_tasks_watch.go) reads the revision: the CLI refuses `task board` to an
// agent-driven caller, and an app started from an agent's shell must still
// show its board, read-only.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/monoes/mono-agent/internal/orgsign"
	"github.com/monoes/mono-agent/internal/tasks"
)

// taskCLITimeout bounds one task command: each is a few small queries.
const taskCLITimeout = 30 * time.Second

// boardDoneLimit is how many Done cards the board shows (spec §10).
const boardDoneLimit = 50

// maxArgNotes caps the notes the app passes on the command line, counted in
// characters (runes), as notesTooLong says: Windows limits a command line to
// 32,767 characters in all; longer notes are saved with the CLI in a terminal.
const maxArgNotes = 30000

// notesTooLong is the refusal for notes over maxArgNotes, in TaskAdd and TaskEdit.
const notesTooLong = "these notes are too long to save from the app (over 30,000 characters); save them in a terminal with monoagentcli task"

// taskBoardStatuses are the columns a card can be moved to; archiving has
// its own binding.
var taskBoardStatuses = map[string]bool{"inbox": true, "ready": true, "in_progress": true, "review": true, "done": true}

// taskRefusal is the {"error","code"} document of input refused before the
// CLI runs.
func taskRefusal(msg string) string {
	return aiError(&codedError{msg: msg, code: "invalid_input"})
}

// taskCLI runs `monoagentcli --profile <active> --json task <args…>` with
// stdin when it is not empty, and returns stdout verbatim or the {"error"}
// shape (cliResultJSON keeps the CLI's own error document and its code).
func (a *App) taskCLI(stdin string, args ...string) string {
	cliBin, err := findMonoAgentCLI()
	if err != nil {
		return aiError(err)
	}
	parent := a.ctx
	if parent == nil {
		parent = context.Background()
	}
	ctx, cancel := context.WithTimeout(parent, taskCLITimeout)
	defer cancel()
	full := append([]string{"--profile", a.getActiveProfileID(), "--json", "task"}, args...)
	cmd := exec.CommandContext(ctx, cliBin, full...)
	suppressConsole(cmd)
	if stdin != "" {
		cmd.Stdin = strings.NewReader(stdin)
	}
	out, runErr := cmd.Output()
	return cliResultJSON(cliBin, out, runErr)
}

// TaskBoard is the active profile's whole board, read in one snapshot with
// Store.Board and returned as the document `task board --json` prints. It
// skips the CLI because `task board` refuses an agent-driven caller (the
// whole board would hand an agent every unreviewed Inbox card, spec §4.1),
// while an app started from an agent's shell must still show its board,
// read-only. The store refuses a whole-board read to every actor but the
// operator, so the read is made as the operator, always, the markers this
// app inherited included: the one who asks is the person at the window, the
// markers only say what started the app, and an actor taken from them, as
// the CLI takes it, would leave that tab with a refusal in place of its
// board. Every action below runs the CLI, whose guard refuses it there.
func (a *App) TaskBoard(doneLimit int) string {
	if a.db == nil {
		return aiError(errors.New("the database is not open yet"))
	}
	ctx := a.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	b, err := tasks.NewStore(a.db).Board(ctx, a.getActiveProfileID(), boardLimit(doneLimit), tasks.Actor{Kind: tasks.Human})
	if err != nil {
		return aiError(err)
	}
	out, err := json.Marshal(b)
	if err != nil {
		return aiError(err)
	}
	return string(out)
}

// boardLimit is the Done cards a board read asks for: Store.Board reads 0 as
// every Done card, which the app never wants, so a limit that is not
// positive is boardDoneLimit.
func boardLimit(n int) int {
	if n <= 0 {
		return boardDoneLimit
	}
	return n
}

// TaskShow is `task show ID`: one task with its history.
func (a *App) TaskShow(id int64) string {
	if id <= 0 {
		return taskRefusal("a task id is a positive number")
	}
	return a.taskCLI("", "show", strconv.FormatInt(id, 10))
}

// taskAddSpec is TaskAdd's argument: a title (with optional notes), or text
// whose first line the CLI makes the title, and whether the task goes
// straight to Ready (the operator's choice).
type taskAddSpec struct {
	Title string `json:"title"`
	Notes string `json:"notes"`
	Text  string `json:"text"`
	Ready bool   `json:"ready"`
}

// TaskAdd is `task add --source app`: a title as one argument after "--",
// so a title that starts with a dash stays a title, or text on standard
// input with --stdin.
func (a *App) TaskAdd(spec string) string {
	var s taskAddSpec
	if err := json.Unmarshal([]byte(spec), &s); err != nil {
		return taskRefusal("the task is not valid JSON: " + err.Error())
	}
	hasTitle, hasText := strings.TrimSpace(s.Title) != "", strings.TrimSpace(s.Text) != ""
	switch {
	case !hasTitle && !hasText:
		return taskRefusal("a task needs a title")
	case hasTitle && hasText:
		return taskRefusal("give a title or a text, not both")
	case hasText && s.Notes != "":
		return taskRefusal("give notes or text, not both")
	case utf8.RuneCountInString(s.Notes) > maxArgNotes:
		return taskRefusal(notesTooLong)
	}
	args := []string{"add", "--source", "app"}
	if s.Ready {
		args = append(args, "--ready")
	}
	if hasText {
		return a.taskCLI(s.Text, append(args, "--stdin")...)
	}
	if s.Notes != "" {
		args = append(args, "--notes="+s.Notes)
	}
	return a.taskCLI("", append(args, "--", s.Title)...)
}

// taskEditSpec is TaskEdit's argument: a field left out stays as it is, and
// empty notes clear them.
type taskEditSpec struct {
	Title *string `json:"title"`
	Notes *string `json:"notes"`
}

// TaskEdit is `task edit ID --title=T --notes=N`, in the = form so a value
// that starts with a dash stays a value.
func (a *App) TaskEdit(id int64, spec string) string {
	if id <= 0 {
		return taskRefusal("a task id is a positive number")
	}
	var s taskEditSpec
	if err := json.Unmarshal([]byte(spec), &s); err != nil {
		return taskRefusal("the change is not valid JSON: " + err.Error())
	}
	args := []string{"edit", strconv.FormatInt(id, 10)}
	if s.Title != nil {
		args = append(args, "--title="+*s.Title)
	}
	if s.Notes != nil {
		if utf8.RuneCountInString(*s.Notes) > maxArgNotes {
			return taskRefusal(notesTooLong)
		}
		args = append(args, "--notes="+*s.Notes)
	}
	if len(args) == 2 {
		return taskRefusal("nothing to change: give a title or notes")
	}
	return a.taskCLI("", args...)
}

// TaskMove is `task move ID STATUS` with a place in the column: where is ""
// (the column's default), "top", "bottom", "before" or "after", the last two
// with ref, another card of that column.
func (a *App) TaskMove(id int64, status, where string, ref int64) string {
	if id <= 0 {
		return taskRefusal("a task id is a positive number")
	}
	if !taskBoardStatuses[status] {
		return taskRefusal("unknown column " + strconv.Quote(status))
	}
	args := []string{"move", strconv.FormatInt(id, 10), status}
	switch where {
	case "":
	case "top", "bottom":
		args = append(args, "--"+where)
	case "before", "after":
		if ref <= 0 || ref == id {
			return taskRefusal(where + " needs another card of the column")
		}
		args = append(args, "--"+where, strconv.FormatInt(ref, 10))
	default:
		return taskRefusal("unknown place " + strconv.Quote(where))
	}
	return a.taskCLI("", args...)
}

// taskIDArgs renders ids as arguments; false for an empty list or an id that
// is not positive.
func taskIDArgs(ids []int64) ([]string, bool) {
	if len(ids) == 0 {
		return nil, false
	}
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		if id <= 0 {
			return nil, false
		}
		out = append(out, strconv.FormatInt(id, 10))
	}
	return out, true
}

// TaskApprove is `task approve ID…`: Inbox to the bottom of Ready, or the
// top with top.
func (a *App) TaskApprove(ids []int64, top bool) string {
	args, ok := taskIDArgs(ids)
	if !ok {
		return taskRefusal("give the ids of the tasks to approve")
	}
	args = append([]string{"approve"}, args...)
	if top {
		args = append(args, "--top")
	}
	return a.taskCLI("", args...)
}

// TaskArchive is `task archive ID…`.
func (a *App) TaskArchive(ids []int64) string {
	args, ok := taskIDArgs(ids)
	if !ok {
		return taskRefusal("give the ids of the tasks to archive")
	}
	return a.taskCLI("", append([]string{"archive"}, args...)...)
}

// TaskUnarchive is `task unarchive ID…`: each back to the column it left.
func (a *App) TaskUnarchive(ids []int64) string {
	args, ok := taskIDArgs(ids)
	if !ok {
		return taskRefusal("give the ids of the tasks to restore")
	}
	return a.taskCLI("", append([]string{"unarchive"}, args...)...)
}

// TaskComment is the operator's `task comment ID -- TEXT`.
func (a *App) TaskComment(id int64, text string) string {
	if id <= 0 {
		return taskRefusal("a task id is a positive number")
	}
	if strings.TrimSpace(text) == "" {
		return taskRefusal("a comment needs text")
	}
	return a.taskCLI("", "comment", strconv.FormatInt(id, 10), "--", text)
}

// TaskAgentShell names the agent-context marker in this app's environment,
// or MONOAGENT_ACTOR, or "". The CLI the app runs inherits it and then
// refuses every operator action (spec D7, §10 Errors), so the page shows the
// board read-only and says to open MonoAgent from the Dock or Finder.
// MONOAGENT_ACTOR counts as the CLI counts it (callerFor in
// cmd/monoagentcli/task.go): set to anything, a value of only spaces too (an
// agent that gave no name); an empty value is as good as none, as the markers
// are read.
func (a *App) TaskAgentShell() string {
	if m := orgsign.AgentContextMarker(); m != "" {
		return m
	}
	if os.Getenv("MONOAGENT_ACTOR") != "" {
		return "MONOAGENT_ACTOR"
	}
	return ""
}
