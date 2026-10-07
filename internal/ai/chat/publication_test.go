package chat

import (
	"context"
	"encoding/json"
	"github.com/monoes/mono-agent/internal/publication"
	"github.com/monoes/mono-agent/internal/workflow"
	"strings"
	"testing"
)

func TestPublicationAssistantToolsAndInjectionGate(t *testing.T) {
	db := newMonoagentTestDB(t)
	mt := NewMonoagentTools(db.DB, "")
	ctx := context.Background()
	raw, err := mt.ExecuteContext(ctx, "register_publication", `{"platform":"blog","kind":"article","title":"Our article"}`)
	if err != nil {
		t.Fatal(err)
	}
	var entry publication.Entry
	if err = json.Unmarshal([]byte(raw), &entry); err != nil {
		t.Fatal(err)
	}
	if _, err = mt.ExecuteContext(ctx, "get_publication", `{"id":"`+entry.ID+`"}`); err != nil {
		t.Fatal(err)
	}
	mt.markSyncedCommsSeen()
	if _, err = mt.ExecuteContext(ctx, "register_publication", `{"platform":"blog","kind":"article","title":"injected"}`); err == nil {
		t.Fatal("registration allowed after untrusted communications")
	}
	if _, err = mt.ExecuteContext(ctx, "list_publications", `{}`); err != nil {
		t.Fatal(err)
	}
}

func TestRegisterPublicationCapAndContextProvenance(t *testing.T) {
	db := newMonoagentTestDB(t)
	mt := NewMonoagentTools(db.DB, "")
	big := `{"platform":"blog","kind":"article","body":"` + strings.Repeat("x", maxPublicationInput) + `"}`
	if _, err := mt.ExecuteContext(context.Background(), "register_publication", big); err == nil {
		t.Fatal("oversized body accepted")
	}
	ctx := workflow.WithTrigger(context.Background(), workflow.TriggerTypeOrgTool, map[string]interface{}{"org": map[string]interface{}{"name": "acme", "role": "writer", "agent_id": "a1"}})
	raw, err := mt.ExecuteContext(ctx, "register_publication", `{"platform":"blog","kind":"article","title":"t","org_id":"evil","agent_id":"evil","role_id":"evil"}`)
	if err != nil {
		t.Fatal(err)
	}
	var e publication.Entry
	_ = json.Unmarshal([]byte(raw), &e)
	if e.OrgID != "acme" || e.AgentID != "a1" || e.RoleID != "writer" {
		t.Fatalf("provenance not forced: %+v", e)
	}
}
