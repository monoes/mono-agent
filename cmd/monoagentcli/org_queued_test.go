package main

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func runOrgQueued(t *testing.T, root string, args ...string) (map[string]interface{}, error) {
	t.Helper()
	cmd := newOrgCmd(&globalConfig{})
	cmd.SetArgs(append([]string{"--project", root, "queued"}, args...))
	var runErr error
	out := captureStdout(t, func() { runErr = cmd.Execute() })
	if runErr != nil {
		return nil, runErr
	}
	var m map[string]interface{}
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &m); err != nil {
		t.Fatalf("org queued printed non-JSON %q: %v", out, err)
	}
	return m, nil
}

func writeOrgFile(t *testing.T, root, rel, content string) {
	t.Helper()
	p := filepath.Join(root, ".monomind", "orgs", rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// C-35: `org queued` lists a stopped org's offline queue as JSON, read-only.
func TestOrgQueuedListsInbox(t *testing.T) {
	root := t.TempDir()
	writeOrgFile(t, root, "growth.json", `{"name":"growth"}`)
	inbox := `{"fromQualified":"hq:ceo","toRole":"lead","subject":"Q3","body":"go","ts":1789725600000,"messageId":"msg-1"}` + "\n"
	writeOrgFile(t, root, "growth/inbox.jsonl", inbox)

	m, err := runOrgQueued(t, root, "growth")
	if err != nil {
		t.Fatal(err)
	}
	if m["org"] != "growth" || m["count"] != float64(1) || m["skipped"] != float64(0) {
		t.Fatalf("unexpected payload: %v", m)
	}
	msgs, _ := m["messages"].([]interface{})
	if len(msgs) != 1 {
		t.Fatalf("messages = %v", m["messages"])
	}
	first := msgs[0].(map[string]interface{})
	if first["from"] != "hq:ceo" || first["to"] != "lead" || first["messageId"] != "msg-1" {
		t.Fatalf("message = %v", first)
	}
	if note, _ := m["delivery"].(string); note != "at next start" {
		t.Fatalf("delivery = %v", m["delivery"])
	}
	if b, _ := os.ReadFile(filepath.Join(root, ".monomind", "orgs", "growth", "inbox.jsonl")); string(b) != inbox {
		t.Fatal("org queued modified inbox.jsonl")
	}
}

func TestOrgQueuedEmptyQueueIsAnEmptyList(t *testing.T) {
	root := t.TempDir()
	writeOrgFile(t, root, "growth.json", `{"name":"growth"}`)
	m, err := runOrgQueued(t, root, "growth")
	if err != nil {
		t.Fatal(err)
	}
	if msgs, ok := m["messages"].([]interface{}); !ok || len(msgs) != 0 || m["count"] != float64(0) {
		t.Fatalf("want an empty list, got %v", m)
	}
}

func TestOrgQueuedUnknownOrgIsNotFound(t *testing.T) {
	_, err := runOrgQueued(t, t.TempDir(), "ghost")
	var ce *cliError
	if !errors.As(err, &ce) || ce.code != 2 {
		t.Fatalf("want a not-found error, got %v", err)
	}
}

func TestOrgQueuedRejectsInvalidName(t *testing.T) {
	_, err := runOrgQueued(t, t.TempDir(), "../x")
	var ce *cliError
	if !errors.As(err, &ce) || ce.code != 3 {
		t.Fatalf("want an invalid-input error, got %v", err)
	}
}
