package chat

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/monoes/mono-agent/internal/orgsign"
	"github.com/monoes/mono-agent/internal/publication"
	"github.com/monoes/mono-agent/internal/workflow"
)

func TestPublicationDeleteRedactOperatorOnly(t *testing.T) {
	for _, m := range orgsign.AgentContextMarkers() {
		t.Setenv(m, "")
	}
	db := newMonoagentTestDB(t)
	mt := NewMonoagentTools(db.DB, "")
	other := NewMonoagentTools(db.DB, "")
	other.SetProfileID("other")
	ctx := context.Background()
	reg := func(tools *MonoagentTools, title string) publication.Entry {
		raw, err := tools.ExecuteContext(ctx, "register_publication", `{"platform":"blog","kind":"article","title":"`+title+`","body":"text"}`)
		if err != nil {
			t.Fatal(err)
		}
		var e publication.Entry
		_ = json.Unmarshal([]byte(raw), &e)
		return e
	}
	a, b := reg(mt, "mine"), reg(other, "theirs")
	for _, tool := range []string{"redact_publication", "delete_publication"} {
		if _, err := mt.ExecuteContext(ctx, tool, `{"id":"`+b.ID+`"}`); err == nil {
			t.Fatalf("%s touched another profile", tool)
		}
	}
	orgCtx := workflow.WithTrigger(ctx, workflow.TriggerTypeOrgTool, map[string]interface{}{"org": map[string]interface{}{"name": "acme", "role": "writer", "agent_id": "a1"}})
	for _, tool := range []string{"redact_publication", "delete_publication"} {
		if _, err := mt.ExecuteContext(orgCtx, tool, `{"id":"`+a.ID+`"}`); err == nil || !strings.Contains(err.Error(), "operator") {
			t.Fatalf("%s in org run: %v", tool, err)
		}
	}
	marker := orgsign.AgentContextMarkers()[0]
	t.Setenv(marker, "1")
	if _, err := mt.ExecuteContext(ctx, "delete_publication", `{"id":"`+a.ID+`"}`); err == nil {
		t.Fatal("delete allowed with agent marker")
	}
	t.Setenv(marker, "")
	mt.markSyncedCommsSeen()
	if _, err := mt.ExecuteContext(ctx, "redact_publication", `{"id":"`+a.ID+`"}`); err == nil {
		t.Fatal("redact allowed after untrusted communications")
	}
	fresh := NewMonoagentTools(db.DB, "")
	raw, err := fresh.ExecuteContext(ctx, "redact_publication", `{"id":"`+a.ID+`","title":true}`)
	if err != nil || !strings.Contains(raw, "[redacted]") || strings.Contains(raw, "mine") {
		t.Fatalf("redact: %s %v", raw, err)
	}
	if _, err = fresh.ExecuteContext(ctx, "delete_publication", `{"id":"`+a.ID+`"}`); err != nil {
		t.Fatal(err)
	}
	if _, err = fresh.ExecuteContext(ctx, "get_publication", `{"id":"`+a.ID+`"}`); err == nil {
		t.Fatal("deleted entry still readable")
	}
}
