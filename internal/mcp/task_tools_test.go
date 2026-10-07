package mcp

// The task tools (spec 8) over a database of their own. The operator's side is the store itself,
// as `monoagentcli task` and the app use it; the agent's side is the tools.

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/monoes/mono-agent/internal/storage"
	"github.com/monoes/mono-agent/internal/tasks"
	"github.com/monoes/mono-agent/internal/testdb"
)

// taskSetup says how a fixture differs from the usual one: a server of the default profile with
// mutations allowed.
type taskSetup struct {
	readOnly bool   // without --allow-mutations
	active   bool   // started without --profile: the server serves the active profile
	profile  string // the profile it is started with, by id or name (default "default")
}

// taskFixture is a server of the task tools, a second handle on its database and the store over
// that handle for the operator's side. The work profile (workProfileID, named workProfileName)
// exists beside the default one. Serve closes a server's database when its input ends, so a test
// that lists the tools (toolsListNames) calls no tool of that server afterwards.
type taskFixture struct {
	t      *testing.T
	Server *Server
	DBPath string
	Side   *storage.Database
	Store  *tasks.Store
}

func newTaskFixture(t *testing.T, ts taskSetup) *taskFixture {
	t.Helper()
	for _, v := range []string{"MONOAGENT_MCP_ALLOW_MUTATIONS", "MONOAGENT_MCP_TASKS_ONLY", "MONOAGENT_MCP_API_ONLY", "MONOAGENT_PROFILE"} {
		t.Setenv(v, "")
	}
	dbPath := testdb.Path(t)
	side := sideDB(t, dbPath)
	if _, err := side.DB.Exec(`INSERT INTO profiles (id, name) VALUES (?, ?)`, workProfileID, workProfileName); err != nil {
		t.Fatal(err)
	}
	f := &taskFixture{t: t, DBPath: dbPath, Side: side, Store: tasks.NewStore(side.DB)}
	f.Server = f.server(ts, "aaaa")
	return f
}

// server is a server over the fixture's database whose task tools act as agent:mcp#<suffix>.
func (f *taskFixture) server(ts taskSetup, suffix string) *Server {
	f.t.Helper()
	profile := ts.profile
	if profile == "" && !ts.active {
		profile = "default"
	}
	s := NewServer(Options{
		DBPath: f.DBPath, Profile: profile, WorkflowsDir: filepath.Join(f.t.TempDir(), "workflows"), Version: "test",
		AllowMutations: !ts.readOnly,
	})
	s.actorSuffix = suffix
	f.t.Cleanup(s.closeRuntime)
	return s
}

// call runs a task tool and returns its text, or its refusal.
func (f *taskFixture) call(name string, args map[string]any) (string, error) {
	f.t.Helper()
	return callAPITool(f.t, f.Server, name, args)
}

// doc runs a task tool that must succeed and returns its document.
func (f *taskFixture) doc(name string, args map[string]any) map[string]any {
	f.t.Helper()
	return parseDoc(f.t, mustCall(f.t, f.Server, name, args))
}

// parseDoc reads the document a task tool returned. Every result of a task tool names the profile
// it is of and carries the note (spec 8), so the tests of every tool check both here, once.
func parseDoc(t *testing.T, text string) map[string]any {
	t.Helper()
	var d map[string]any
	if err := json.Unmarshal([]byte(text), &d); err != nil {
		t.Fatalf("not a JSON document: %v\n%s", err, text)
	}
	p, _ := d["profile"].(map[string]any)
	if id, _ := p["id"].(string); id == "" {
		t.Errorf("a result without the id of its profile: profile = %v", d["profile"])
	}
	if name, _ := p["name"].(string); name == "" {
		t.Errorf("a result without the name of its profile: profile = %v", d["profile"])
	}
	if d["note"] != untrustedNote {
		t.Errorf("a result without the untrusted note: note = %v", d["note"])
	}
	return d
}

// add puts a task on a profile's board as the operator: in Ready when ready is true, else in Inbox.
func (f *taskFixture) add(profile, title string, ready bool) tasks.Task {
	f.t.Helper()
	tk, _, err := f.Store.Add(context.Background(), profile, tasks.AddInput{Title: title, Ready: ready}, tasks.Actor{Kind: tasks.Human})
	if err != nil {
		f.t.Fatal(err)
	}
	return tk
}

// listed are the ids of a document's tasks, in order.
func listed(d map[string]any) []int64 {
	ids := []int64{}
	for _, v := range d["tasks"].([]any) {
		ids = append(ids, int64(v.(map[string]any)["id"].(float64)))
	}
	return ids
}

// taskOf is the id of a document's task, 0 when it is null.
func taskOf(d map[string]any) int64 {
	if m, ok := d["task"].(map[string]any); ok {
		return int64(m["id"].(float64))
	}
	return 0
}

// idsAre reports whether got are exactly want, in order.
func idsAre(got []int64, want ...int64) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

func TestTaskListShowsAnAgentTheOpenWorkOfItsProfile(t *testing.T) {
	f := newTaskFixture(t, taskSetup{})
	inbox := f.add("default", "captured", false)
	r1 := f.add("default", "first", true)
	r2 := f.add("default", "second", true)
	done := f.add("default", "finished", true)
	if _, err := f.Store.Move(context.Background(), "default", done.ID, tasks.StatusDone, tasks.Placement{}, tasks.Actor{Kind: tasks.Human}); err != nil {
		t.Fatal(err)
	}
	f.add(workProfileID, "another profile's", true)

	d := f.doc("task_list", nil)
	if got := listed(d); !idsAre(got, r1.ID, r2.ID) {
		t.Errorf("by default an agent sees Ready, In progress and Review: %v, want [%d %d]", got, r1.ID, r2.ID)
	}
	if p, _ := d["profile"].(map[string]any); p["id"] != "default" || p["name"] != "Default" {
		t.Errorf("profile = %v", d["profile"])
	}
	if d["note"] != untrustedNote {
		t.Errorf("note = %v", d["note"])
	}
	if got := listed(f.doc("task_list", map[string]any{"status": "inbox"})); !idsAre(got, inbox.ID) {
		t.Errorf("Inbox when named: %v", got)
	}
	if got := listed(f.doc("task_list", map[string]any{"status": []string{"done", "inbox"}})); !idsAre(got, inbox.ID, done.ID) {
		t.Errorf("several columns, in board order: %v", got)
	}
	if _, err := f.call("task_list", map[string]any{"status": "someday"}); err == nil || !strings.HasPrefix(err.Error(), "invalid_input: ") {
		t.Errorf("an unknown status: %v", err)
	}
}

func TestTaskListGivesFiftyByDefaultAndAtMostTwoHundred(t *testing.T) {
	f := newTaskFixture(t, taskSetup{})
	stamp := time.Now().UTC().Format("2006-01-02T15:04:05Z")
	tx, err := f.Side.DB.Begin()
	if err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= 205; i++ {
		if _, err := tx.Exec(`INSERT INTO tasks (profile_id, title, status, position, created_at, updated_at) VALUES ('default', ?, 'ready', ?, ?, ?)`,
			fmt.Sprintf("seeded %d", i), i*1024, stamp, stamp); err != nil {
			t.Fatal(err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	for limit, want := range map[int]int{0: 50, 7: 7, 200: 200, 201: 200, 100000: 200} {
		args := map[string]any{}
		if limit != 0 {
			args["limit"] = limit
		}
		if got := len(listed(f.doc("task_list", args))); got != want {
			t.Errorf("limit %d: %d tasks, want %d", limit, got, want)
		}
	}
	if got := len(listed(f.doc("task_list", map[string]any{"limit": "7"}))); got != 7 {
		t.Errorf(`limit "7": %d tasks, want 7 (a numeric string is a number)`, got)
	}
}

func TestTaskGetIsOneTaskWithItsWholeHistory(t *testing.T) {
	f := newTaskFixture(t, taskSetup{})
	mine := f.add("default", "with a history", true)
	if _, err := f.Store.Comment(context.Background(), "default", mine.ID, "look at the logs first", tasks.Actor{Kind: tasks.Human}); err != nil {
		t.Fatal(err)
	}
	theirs := f.add(workProfileID, "another profile's", true)

	for _, id := range []any{mine.ID, fmt.Sprintf("#%d", mine.ID)} {
		d := f.doc("task_get", map[string]any{"id": id})
		events, _ := d["events"].([]any)
		if taskOf(d) != mine.ID || len(events) != 2 || d["note"] != untrustedNote {
			t.Fatalf("task_get %v: %v", id, d)
		}
		if e := events[1].(map[string]any); e["kind"] != "comment" || e["note_untrusted"] != "look at the logs first" || e["actor"] != "you" {
			t.Errorf("the operator's comment: %v", e)
		}
	}
	_, err := f.call("task_get", map[string]any{"id": theirs.ID})
	if err == nil || !strings.HasPrefix(err.Error(), "not_found: ") || strings.Contains(err.Error(), workProfileID) {
		t.Errorf("another profile's task must be not found, and say nothing of that profile: %v", err)
	}
	if _, err := f.call("task_get", nil); err == nil || !strings.HasPrefix(err.Error(), "invalid_input: id is required") {
		t.Errorf("no id: %v", err)
	}
}

func TestTaskNextPeeksAtTheTopOfReadyAndNeverAtInbox(t *testing.T) {
	f := newTaskFixture(t, taskSetup{})
	f.add("default", "captured", false)
	if d := f.doc("task_next", nil); d["task"] != nil || d["note"] != untrustedNote {
		t.Errorf("only an Inbox task: %v, want task null", d)
	}
	top := f.add("default", "top", true)
	f.add("default", "below", true)
	for i := 0; i < 2; i++ { // looking twice claims nothing
		if got := taskOf(f.doc("task_next", nil)); got != top.ID {
			t.Errorf("look %d: task %d, want the top of Ready %d", i, got, top.ID)
		}
	}
	if tk, _, err := f.Store.Get(context.Background(), "default", top.ID); err != nil || tk.Status != tasks.StatusReady || tk.Claim != nil {
		t.Errorf("task_next claimed: %+v, %v", tk, err)
	}
	if _, err := f.call("task_next", map[string]any{"claim": true}); err == nil || !strings.HasPrefix(err.Error(), "invalid_input: ") {
		t.Errorf("task_next takes no argument: %v", err)
	}
}

// A server serves the profile it started with (spec 4.5): --profile by id or name, else the
// profile that was active when it first read the board. The app switching profiles later does not
// move the agent to another board.
func TestAServerKeepsTheProfileItStartedWith(t *testing.T) {
	f := newTaskFixture(t, taskSetup{active: true})
	mine := f.add("default", "mine", true)
	theirs := f.add(workProfileID, "theirs", true)
	if got := listed(f.doc("task_list", nil)); !idsAre(got, mine.ID) {
		t.Fatalf("the active profile's board: %v", got)
	}
	if _, err := f.Side.DB.Exec(`INSERT OR REPLACE INTO settings (key, value) VALUES ('active_profile_id', ?)`, workProfileID); err != nil {
		t.Fatal(err)
	}
	d := f.doc("task_list", nil)
	if p := d["profile"].(map[string]any); p["id"] != "default" || !idsAre(listed(d), mine.ID) {
		t.Errorf("after the app switched profiles the server moved to another board: %v", d)
	}
	work := f.server(taskSetup{profile: workProfileName}, "bbbb")
	wd := parseDoc(t, mustCall(t, work, "task_list", nil))
	if p := wd["profile"].(map[string]any); p["id"] != workProfileID || p["name"] != workProfileName || !idsAre(listed(wd), theirs.ID) {
		t.Errorf("a server started with --profile %s: %v", workProfileName, wd)
	}
}

func TestTheTaskReadToolsAreInTheDefaultServer(t *testing.T) {
	f := newTaskFixture(t, taskSetup{readOnly: true})
	names := toolsListNames(t, f.Server)
	for _, n := range []string{"task_list", "task_get", "task_next"} {
		if !names[n] {
			t.Errorf("a read-only server does not list %s", n)
		}
	}
	for _, def := range toolDefinitions(false) {
		if name := def["name"].(string); strings.HasPrefix(name, "task_") {
			if a, _ := def["annotations"].(map[string]bool); !a["readOnlyHint"] {
				t.Errorf("%s: annotations %v", name, def["annotations"])
			}
		}
	}
	if got := NewServer(Options{}).instructions(); !strings.HasSuffix(got, " The user's task board: task_next shows what is ready to work on.") {
		t.Errorf("the default instructions: %q", got)
	}
}

// Every task tool's description opens with the introduction of the board (global constraint 12): which
// board it is, that a task's words may come from web pages and other apps, that a task is worked only
// after the operator moved it to Ready, and that an Inbox task is never worked. The sentences are
// written out here, so that an edit of taskBoardIntro itself is noticed too, not only a description
// that no longer starts with it. The tools are those called task_*, the verbs of later tasks included.
func TestEveryTaskToolDescriptionOpensWithTheBoardIntroduction(t *testing.T) {
	const board = "The user's monoagent task board (not a monomind org's issues)"
	if !strings.HasPrefix(taskBoardIntro, board) {
		t.Errorf("taskBoardIntro does not start with %q", board)
	}
	for _, phrase := range []string{"web pages and other apps", "worked only after the operator moved it to Ready", "Inbox", "never work"} {
		if !strings.Contains(taskBoardIntro, phrase) {
			t.Errorf("taskBoardIntro lacks %q", phrase)
		}
	}
	seen := 0
	for _, tl := range allTools() {
		if !strings.HasPrefix(tl.name, "task_") {
			continue
		}
		seen++
		if !strings.HasPrefix(tl.description, taskBoardIntro) {
			t.Errorf("%s: the description does not open with taskBoardIntro: %.100q", tl.name, tl.description)
		}
	}
	if seen < 3 {
		t.Errorf("only %d tools are called task_*: is the table of tools read?", seen)
	}
}
