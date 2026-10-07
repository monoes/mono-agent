package main

// The task tools work the board the task commands show (spec 8: a pipe test per tool against its
// command). A host's session over a pipe, with a --tasks-only server, works a board; the commands
// read it; and the two agree: a tool's document is the command's --json with the text fields
// named _untrusted, the source flattened, and the note added.

import (
	"encoding/json"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"
)

// newTaskMCP is a database the task commands use and a host's session with a --tasks-only server
// over it (mutations allowed), initialized by a client called claude-code.
func newTaskMCP(t *testing.T) (string, *mcpSession) {
	t.Helper()
	db := newTaskTestDB(t)
	for _, v := range []string{"MONOAGENT_MCP_TASKS_ONLY", "MONOAGENT_MCP_API_ONLY", "MONOAGENT_PROFILE"} {
		t.Setenv(v, "")
	}
	o := mcpOptions(t, db, "default", false)
	o.TasksOnly = true
	m := newMCPSession(t, o)
	mcpInitialize(t, m, "claude-code")
	return db, m
}

// mcpInitialize sends initialize as a host does, naming the client, and waits for the answer.
func mcpInitialize(t *testing.T, m *mcpSession, client string) {
	t.Helper()
	reqID := m.next
	m.next++
	req, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": reqID, "method": "initialize",
		"params": map[string]any{"protocolVersion": "2024-11-05", "capabilities": map[string]any{}, "clientInfo": map[string]any{"name": client, "version": "1"}}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.in.Write(append(req, '\n')); err != nil {
		t.Fatal(err)
	}
	select {
	case line, ok := <-m.lines:
		if !ok || !strings.Contains(string(line), "task_claim with next=true") {
			t.Fatalf("initialize: %s", line)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("the MCP server did not answer initialize within 30 s")
	}
}

// plainTask is a tool's task as the commands print it.
func plainTask(tk map[string]any) map[string]any {
	tk["title"], tk["notes"] = tk["title_untrusted"], tk["notes_untrusted"]
	tk["source"] = map[string]any{"kind": tk["source_kind"], "url": tk["source_url_untrusted"], "title": tk["source_title_untrusted"], "app": tk["source_app_untrusted"]}
	for _, k := range []string{"title_untrusted", "notes_untrusted", "source_kind", "source_url_untrusted", "source_title_untrusted", "source_app_untrusted"} {
		delete(tk, k)
	}
	return tk
}

// plainTaskDoc is a task tool's document as the matching command prints it: without the note,
// and with the names the tools end in _untrusted given back their plain form.
func plainTaskDoc(t *testing.T, text string) map[string]any {
	t.Helper()
	var d map[string]any
	if err := json.Unmarshal([]byte(text), &d); err != nil {
		t.Fatalf("not a JSON document: %v\n%s", err, text)
	}
	if d["note"] == nil {
		t.Errorf("the document has no note: %s", text)
	}
	delete(d, "note")
	if tk, ok := d["task"].(map[string]any); ok {
		d["task"] = plainTask(tk)
	}
	if ts, ok := d["tasks"].([]any); ok {
		for i, tk := range ts {
			ts[i] = plainTask(tk.(map[string]any))
		}
	}
	if es, ok := d["events"].([]any); ok {
		for _, e := range es {
			ev := e.(map[string]any)
			ev["note"] = ev["note_untrusted"]
			delete(ev, "note_untrusted")
		}
	}
	return d
}

// taskCLIDoc is a task command's --json document, as the operator runs it.
func taskCLIDoc(t *testing.T, db string, args ...string) map[string]any {
	t.Helper()
	var d map[string]any
	mustTaskJSON(t, db, "default", &d, "", args...)
	return d
}

func TestTaskToolsReadWhatTheTaskCommandsRead(t *testing.T) {
	db, m := newTaskMCP(t)
	var added addedJSON
	mustTaskJSON(t, db, "default", &added, "", "add", "Read the invoice", "--notes", "From Sam", "--url", "https://example.com/inv",
		"--source-title", "Inbox", "--app", "Mail", "--ready")
	mustTaskJSON(t, db, "default", &addedJSON{}, "", "add", "Later")
	idText := strconv.FormatInt(added.Task.ID, 10)
	for _, c := range []struct {
		tool string
		args map[string]any
		cli  []string
	}{
		{"task_list", map[string]any{}, []string{"list", "--as", "reader"}},
		{"task_list", map[string]any{"status": "inbox,ready"}, []string{"list", "--status", "inbox,ready"}},
		{"task_get", map[string]any{"id": added.Task.ID}, []string{"show", idText}},
		{"task_next", map[string]any{}, []string{"next"}},
	} {
		got, want := plainTaskDoc(t, m.mustCall(c.tool, c.args)), taskCLIDoc(t, db, c.cli...)
		if !reflect.DeepEqual(got, want) {
			t.Errorf("%s %v and `task %s` disagree\n tool %v\n cli  %v", c.tool, c.args, strings.Join(c.cli, " "), got, want)
		}
	}
}

func TestTaskToolsWriteWhatTheTaskCommandsShow(t *testing.T) {
	db, m := newTaskMCP(t)
	var first, second addedJSON
	mustTaskJSON(t, db, "default", &first, "", "add", "First", "--ready")
	mustTaskJSON(t, db, "default", &second, "", "add", "Second", "--ready")
	sameTask := func(tool, text string, id int64) map[string]any {
		t.Helper()
		got := plainTaskDoc(t, text)["task"]
		want := taskCLIDoc(t, db, "show", strconv.FormatInt(id, 10))
		if !reflect.DeepEqual(got, want["task"]) {
			t.Errorf("%s left a task the command shows otherwise\n tool %v\n cli  %v", tool, got, want["task"])
		}
		return want
	}

	claimed := sameTask("task_claim", m.mustCall("task_claim", map[string]any{"next": true}), first.Task.ID)
	by, _ := claimed["task"].(map[string]any)["claim"].(map[string]any)["by"].(string)
	if !regexp.MustCompile(`^agent:claude-code#[0-9a-f]{4}$`).MatchString(by) {
		t.Errorf("the claim is held by %q, want agent:claude-code#<4 hex>", by)
	}
	sameTask("task_comment", m.mustCall("task_comment", map[string]any{"id": first.Task.ID, "text": "halfway"}), first.Task.ID)
	done := sameTask("task_finish", m.mustCall("task_finish", map[string]any{"id": first.Task.ID, "result": "sent it"}), first.Task.ID)
	var kinds []string
	for _, e := range done["events"].([]any) {
		ev := e.(map[string]any)
		kinds = append(kinds, ev["kind"].(string))
		if ev["kind"] != "created" && ev["actor"] != by {
			t.Errorf("the command shows %s by %v, want %s", ev["kind"], ev["actor"], by)
		}
	}
	if got := strings.Join(kinds, ","); got != "created,claimed,comment,result" {
		t.Errorf("history %s", got)
	}

	m.mustCall("task_claim", map[string]any{"id": second.Task.ID})
	sameTask("task_release", m.mustCall("task_release", map[string]any{"id": second.Task.ID, "note": "no access"}), second.Task.ID)

	addedDoc := plainTaskDoc(t, m.mustCall("task_add", map[string]any{"title": "Found a bug", "notes": "in the parser"}))
	tk := addedDoc["task"].(map[string]any)
	want := taskCLIDoc(t, db, "show", strconv.FormatInt(int64(tk["id"].(float64)), 10))
	if !reflect.DeepEqual(tk, want["task"]) || addedDoc["created"] != true || tk["status"] != "inbox" || tk["source"].(map[string]any)["kind"] != "agent" {
		t.Errorf("task_add: %v, the command shows %v", addedDoc, want["task"])
	}
}

// A refusal carries the same code on both sides.
func TestTaskToolsAndCommandsRefuseWithTheSameCode(t *testing.T) {
	db, m := newTaskMCP(t)
	var held addedJSON
	mustTaskJSON(t, db, "default", &held, "", "add", "Held", "--ready")
	idText := strconv.FormatInt(held.Task.ID, 10)
	mustTaskJSON(t, db, "default", &map[string]any{}, "", "claim", idText, "--as", "someone-else")
	for _, c := range []struct {
		tool     string
		args     map[string]any
		cli      []string
		cliExit  int
		wantCode string
	}{
		{"task_claim", map[string]any{"id": held.Task.ID}, []string{"claim", idText, "--as", "a-third"}, 3, "claimed"},
		{"task_get", map[string]any{"id": 999999}, []string{"show", "999999"}, 2, "not_found"},
	} {
		text, isErr := m.call(c.tool, c.args)
		doc := failedTaskJSON(t, db, "default", c.cliExit, c.cli...)
		if !isErr || doc["code"] != c.wantCode || !strings.HasPrefix(text, c.wantCode+": ") {
			t.Errorf("%s: the tool says %q, the command %v; want both %s", c.tool, text, doc, c.wantCode)
		}
	}
}
