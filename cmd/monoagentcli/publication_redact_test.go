package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/monoes/mono-agent/internal/orgsign"
)

func TestPublicationRedactCLIOperatorOnly(t *testing.T) {
	for _, m := range orgsign.AgentContextMarkers() {
		t.Setenv(m, "")
	}
	t.Setenv("MONOAGENT_ACTOR", "")
	dbPath := newApplicationCLITestDB(t)
	run := func(input string, args ...string) (string, error) {
		cmd := newPublicationCmd(&globalConfig{DBPath: dbPath, JSONOutput: true})
		var out bytes.Buffer
		cmd.SetOut(&out)
		cmd.SetIn(strings.NewReader(input))
		cmd.SetArgs(args)
		err := cmd.Execute()
		return out.String(), err
	}
	out, err := run(`{"platform":"x","kind":"post","title":"T","body":"private text","url":"https://example.com/1"}`, "register", "--stdin-json")
	if err != nil {
		t.Fatal(err)
	}
	id := out[strings.Index(out, `"id": "`)+7:]
	id = id[:strings.Index(id, `"`)]
	t.Setenv("MONOAGENT_ACTOR", "writer-bot")
	if _, err = run("", "redact", id); err == nil {
		t.Fatal("agent redacted history")
	}
	t.Setenv("MONOAGENT_ACTOR", "")
	out, err = run("", "redact", id, "--title")
	if err != nil || strings.Contains(out, "private text") || strings.Contains(out, `"T"`) || !strings.Contains(out, "[redacted]") || !strings.Contains(out, "https://example.com/1") {
		t.Fatalf("redact: %s %v", out, err)
	}
	if _, err = run("", "redact", "missing"); err == nil {
		t.Fatal("missing id accepted")
	}
}
