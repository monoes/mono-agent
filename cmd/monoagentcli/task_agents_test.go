package main

import (
	"strings"
	"testing"

	"github.com/monoes/mono-agent/internal/storage"
)

// `task agents` is the operator's switch for what AI agents may do with the Inbox
// (spec D33): off by default, per profile, and never changeable by an agent.

type agentsAccessJSON struct {
	Profile struct {
		ID string `json:"id"`
	} `json:"profile"`
	View    bool `json:"view"`
	Approve bool `json:"approve"`
}

func agentsShow(t *testing.T, db, profile string, extra ...string) agentsAccessJSON {
	t.Helper()
	var a agentsAccessJSON
	mustTaskJSON(t, db, profile, &a, "", append([]string{"agents", "show"}, extra...)...)
	return a
}

func agentsSet(t *testing.T, db, profile string, args ...string) {
	t.Helper()
	if _, _, err := runTask(t, db, profile, true, "", append([]string{"agents"}, args...)...); err != nil {
		t.Fatalf("task agents %s: %v", strings.Join(args, " "), err)
	}
}

func inboxIDs(t *testing.T, db, profile string, args ...string) []int64 {
	t.Helper()
	var l listJSON
	mustTaskJSON(t, db, profile, &l, "", append([]string{"list"}, args...)...)
	var ids []int64
	for _, task := range l.Tasks {
		if task.Status == "inbox" {
			ids = append(ids, task.ID)
		}
	}
	return ids
}

func TestAgentsAccessIsOffByDefaultAndRefusalsAreUnchanged(t *testing.T) {
	db := newTaskTestDB(t)
	ids := seedTaskRows(t, db, taskSeed{title: "captured"})
	if a := agentsShow(t, db, "default"); a.View || a.Approve {
		t.Fatalf("default: %+v", a)
	}
	doc := failedTaskJSON(t, db, "default", 3, "approve", id(ids[0]), "--as", "amy")
	if msg, _ := doc["error"].(string); doc["code"] != "operator_only" ||
		!strings.Contains(msg, "--as or MONOAGENT_ACTOR names an agent: only the operator can approve a task; run it in your own terminal") {
		t.Errorf("approve refusal changed: %v", doc)
	}
	doc = failedTaskJSON(t, db, "default", 3, "board", "--as", "amy")
	if msg, _ := doc["error"].(string); doc["code"] != "operator_only" || !strings.Contains(msg, "only the operator can show the board") || !strings.Contains(msg, "use task list instead") {
		t.Errorf("board refusal changed: %v", doc)
	}
	if got := inboxIDs(t, db, "default", "--as", "amy"); len(got) != 0 {
		t.Errorf("an agent's default list shows the Inbox: %v", got)
	}
	// The task is still in the Inbox.
	if opsShow(t, db, ids[0]).Task.Status != "inbox" {
		t.Error("the refused approval moved the task")
	}
}

func TestAgentsViewSwitchAlone(t *testing.T) {
	db := newTaskTestDB(t)
	ids := seedTaskRows(t, db, taskSeed{title: "captured"}, taskSeed{title: "go", status: "ready"})
	agentsSet(t, db, "default", "allow", "--view")
	if a := agentsShow(t, db, "default"); !a.View || a.Approve {
		t.Fatalf("after allow --view: %+v", a)
	}
	if got := inboxIDs(t, db, "default", "--as", "amy"); len(got) != 1 || got[0] != ids[0] {
		t.Errorf("an agent's default list with view: %v", got)
	}
	var board struct {
		Tasks map[string][]taskJSON `json:"tasks"`
	}
	mustTaskJSON(t, db, "default", &board, "", "board", "--as", "amy")
	if len(board.Tasks["inbox"]) != 1 {
		t.Errorf("an agent's board with view: %+v", board.Tasks)
	}
	// view is not approve
	doc := failedTaskJSON(t, db, "default", 3, "approve", id(ids[0]), "--as", "amy")
	if doc["code"] != "operator_only" {
		t.Errorf("view allowed approve: %v", doc)
	}
}

func TestAgentsApproveSwitchAloneAndAuditTrail(t *testing.T) {
	db := newTaskTestDB(t)
	ids := seedTaskRows(t, db, taskSeed{title: "captured"})
	agentsSet(t, db, "default", "allow", "--approve")
	if a := agentsShow(t, db, "default"); a.View || !a.Approve {
		t.Fatalf("after allow --approve: %+v", a)
	}
	// approve does not open the board or the default list.
	if doc := failedTaskJSON(t, db, "default", 3, "board", "--as", "amy"); doc["code"] != "operator_only" {
		t.Errorf("approve opened the board: %v", doc)
	}
	// An agent that has no name (a marker, no --as) is refused and told to name itself.
	t.Setenv("CLAUDECODE", "1")
	doc := failedTaskJSON(t, db, "default", 3, "approve", id(ids[0]))
	if msg, _ := doc["error"].(string); !strings.Contains(msg, "--as") {
		t.Errorf("a nameless agent's approval: %v", doc)
	}
	// With the marker set and a name: approved, as that agent.
	mustTaskJSON(t, db, "default", &struct{}{}, "", "approve", id(ids[0]), "--as", "amy")
	t.Setenv("CLAUDECODE", "")
	shown := opsShow(t, db, ids[0])
	last := shown.Events[len(shown.Events)-1]
	if shown.Task.Status != "ready" || last.Actor != "amy" || !strings.Contains(last.Note, "delegated") {
		t.Fatalf("after the agent's approval: %s, last event %+v", shown.Task.Status, last)
	}
	text, _, err := runTask(t, db, "default", false, "", "show", id(ids[0]))
	if err != nil || !strings.Contains(text, "amy") || !strings.Contains(text, "approved (delegated") {
		t.Errorf("task show does not display the delegated approval: %v\n%s", err, text)
	}
}

func TestAgentsDenyTakesEffectImmediately(t *testing.T) {
	db := newTaskTestDB(t)
	ids := seedTaskRows(t, db, taskSeed{title: "a"}, taskSeed{title: "b"})
	agentsSet(t, db, "default", "allow", "--view", "--approve")
	mustTaskJSON(t, db, "default", &struct{}{}, "", "approve", id(ids[0]), "--as", "amy")
	agentsSet(t, db, "default", "deny", "--approve")
	if a := agentsShow(t, db, "default"); !a.View || a.Approve {
		t.Fatalf("after deny --approve: %+v", a)
	}
	if doc := failedTaskJSON(t, db, "default", 3, "approve", id(ids[1]), "--as", "amy"); doc["code"] != "operator_only" {
		t.Errorf("approve after deny: %v", doc)
	}
	agentsSet(t, db, "default", "deny") // no flag: everything
	if a := agentsShow(t, db, "default"); a.View || a.Approve {
		t.Fatalf("after deny: %+v", a)
	}
	if doc := failedTaskJSON(t, db, "default", 3, "board", "--as", "amy"); doc["code"] != "operator_only" {
		t.Errorf("board after deny: %v", doc)
	}
}

func TestAgentsAllowNeedsASwitch(t *testing.T) {
	db := newTaskTestDB(t)
	doc := failedTaskJSON(t, db, "default", 3, "agents", "allow")
	if doc["code"] != "invalid_input" {
		t.Errorf("allow with no switch: %v", doc)
	}
	if a := agentsShow(t, db, "default"); a.View || a.Approve {
		t.Errorf("allow with no switch changed the access: %+v", a)
	}
}

// An agent never changes the setting: every form of an agent-driven caller is refused before the
// database is opened, and the setting stays as it was.
func TestAgentsControlIsRefusedToEveryAgent(t *testing.T) {
	db := newTaskTestDB(t)
	broken := opsBrokenDB(t)
	for name, setup := range opsAgentContexts {
		t.Run(name, func(t *testing.T) {
			extra := setup(t)
			for _, args := range [][]string{{"agents", "allow", "--view"}, {"agents", "allow", "--approve"}, {"agents", "allow", "--view", "--approve"}, {"agents", "deny"}, {"agents", "deny", "--view"}} {
				full := append(append([]string{}, args...), extra...)
				doc := failedTaskJSON(t, broken, "default", 3, full...)
				if msg, _ := doc["error"].(string); doc["code"] != "operator_only" || !strings.Contains(msg, "your own terminal") {
					t.Errorf("task %s: %v", strings.Join(full, " "), doc)
				}
				doc = failedTaskJSON(t, db, "default", 3, full...)
				if doc["code"] != "operator_only" {
					t.Errorf("task %s: %v", strings.Join(full, " "), doc)
				}
			}
		})
	}
	if a := agentsShow(t, db, "default"); a.View || a.Approve {
		t.Fatalf("an agent changed the access: %+v", a)
	}
	// ...also when the operator had allowed things: an agent cannot deny or widen either.
	agentsSet(t, db, "default", "allow", "--view")
	if doc := failedTaskJSON(t, db, "default", 3, "agents", "allow", "--approve", "--as", "amy"); doc["code"] != "operator_only" {
		t.Errorf("agent allow: %v", doc)
	}
	if doc := failedTaskJSON(t, db, "default", 3, "agents", "deny", "--as", "amy"); doc["code"] != "operator_only" {
		t.Errorf("agent deny: %v", doc)
	}
	if a := agentsShow(t, db, "default"); !a.View || a.Approve {
		t.Fatalf("an agent changed the access: %+v", a)
	}
}

func TestAgentsAccessIsPerProfile(t *testing.T) {
	db := newTaskTestDB(t)
	raw, err := storage.NewDatabase(db)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := raw.DB.Exec(`INSERT INTO profiles (id, name) VALUES ('p-b', 'Other')`); err != nil {
		t.Fatal(err)
	}
	raw.Close()
	a := seedTaskRows(t, db, taskSeed{title: "in default"})
	b := seedTaskRows(t, db, taskSeed{profile: "p-b", title: "in other"})
	agentsSet(t, db, "default", "allow", "--view", "--approve")
	if got := agentsShow(t, db, "p-b"); got.View || got.Approve {
		t.Fatalf("profile B inherited %+v", got)
	}
	if doc := failedTaskJSON(t, db, "p-b", 3, "approve", id(b[0]), "--as", "amy"); doc["code"] != "operator_only" {
		t.Errorf("approve on profile B: %v", doc)
	}
	if doc := failedTaskJSON(t, db, "p-b", 3, "board", "--as", "amy"); doc["code"] != "operator_only" {
		t.Errorf("board of profile B: %v", doc)
	}
	if got := inboxIDs(t, db, "p-b", "--as", "amy"); len(got) != 0 {
		t.Errorf("profile B's Inbox is listed: %v", got)
	}
	mustTaskJSON(t, db, "default", &struct{}{}, "", "approve", id(a[0]), "--as", "amy")
}
