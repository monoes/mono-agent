package mcp

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/monoes/mono-agent/internal/orgsign"
	"github.com/monoes/mono-agent/internal/publication"
)

func TestPublicationMCPDeleteRedactGatedAndScoped(t *testing.T) {
	for _, allow := range []bool{false, true} {
		names := map[string]bool{}
		for _, def := range toolDefinitions(allow) {
			names[def["name"].(string)] = true
		}
		if names["publication_delete"] != allow || names["publication_redact"] != allow {
			t.Fatalf("delete/redact gate with allow=%v", allow)
		}
	}
	for _, m := range orgsign.AgentContextMarkers() {
		t.Setenv(m, "")
	}
	s := newTestServerAllowMutations(t, true)
	rt, err := s.runtime()
	if err != nil {
		t.Fatal(err)
	}
	defer s.closeRuntime()
	ctx := context.Background()
	other, err := publication.NewStore(rt.db.DB, "other").Register(ctx, publication.Entry{Platform: "custom", Kind: "post", Body: "theirs"})
	if err != nil {
		t.Fatal(err)
	}
	for _, tool := range []string{"publication_redact", "publication_delete"} {
		if _, err = callTool(ctx, s, tool, json.RawMessage(`{"id":"`+other.ID+`"}`)); err == nil {
			t.Fatalf("%s crossed profiles", tool)
		}
	}
	raw, err := callTool(ctx, s, "publication_register", json.RawMessage(`{"platform":"custom","kind":"post","body":"ours"}`))
	if err != nil {
		t.Fatal(err)
	}
	var mine publication.Entry
	if err = json.Unmarshal([]byte(raw), &mine); err != nil {
		t.Fatalf("register output %v: %s", err, raw)
	}
	raw, err = callTool(ctx, s, "publication_redact", json.RawMessage(`{"id":"`+mine.ID+`"}`))
	if err != nil || !strings.Contains(raw, "[redacted]") {
		t.Fatalf("redact: %v %v", raw, err)
	}
	if _, err = callTool(ctx, s, "publication_delete", json.RawMessage(`{"id":"`+mine.ID+`"}`)); err != nil {
		t.Fatal(err)
	}
}
