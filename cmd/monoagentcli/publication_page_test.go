package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/monoes/mono-agent/internal/orgsign"
	"github.com/monoes/mono-agent/internal/publication"
)

func TestPublicationCLIDeleteAndCursor(t *testing.T) {
	dbPath := newApplicationCLITestDB(t)
	t.Setenv("MONOAGENT_ACTOR", "")
	for _, m := range orgsign.AgentContextMarkers() {
		t.Setenv(m, "")
	}
	run := func(input string, args ...string) (string, error) {
		cmd := newPublicationCmd(&globalConfig{DBPath: dbPath, JSONOutput: true})
		var out bytes.Buffer
		cmd.SetOut(&out)
		cmd.SetIn(strings.NewReader(input))
		cmd.SetArgs(args)
		err := cmd.Execute()
		return out.String(), err
	}
	var ids []string
	for _, key := range []string{"a", "b", "c"} {
		out, err := run(`{"platform":"x","kind":"post","body":"text `+key+`","idempotency_key":"`+key+`"}`, "register", "--stdin-json")
		var e publication.Entry
		if err != nil || json.Unmarshal([]byte(out), &e) != nil {
			t.Fatalf("register: %s %v", out, err)
		}
		ids = append(ids, e.ID)
	}
	out, err := run("", "list", "--limit", "2", "--cursor", "")
	var page struct {
		Publications []publication.Entry `json:"publications"`
		NextCursor   string              `json:"next_cursor"`
	}
	if err != nil || json.Unmarshal([]byte(out), &page) != nil || len(page.Publications) != 2 || page.NextCursor == "" {
		t.Fatalf("first page: %s %v", out, err)
	}
	out, err = run("", "list", "--limit", "2", "--cursor", page.NextCursor)
	if err != nil || json.Unmarshal([]byte(out), &page) != nil || len(page.Publications) != 1 || page.NextCursor != "" {
		t.Fatalf("last page: %s %v", out, err)
	}
	t.Setenv("MONOAGENT_ACTOR", "bot")
	if _, err := run("", "delete", ids[0]); err == nil || !strings.Contains(err.Error(), "only the operator") {
		t.Fatalf("agent delete should be refused: %v", err)
	}
	t.Setenv("MONOAGENT_ACTOR", "")
	if out, err = run("", "delete", ids[0]); err != nil || !strings.Contains(out, ids[0]) {
		t.Fatalf("delete: %s %v", out, err)
	}
	if _, err = run("", "get", ids[0]); err == nil {
		t.Fatal("deleted publication still readable")
	}
}
