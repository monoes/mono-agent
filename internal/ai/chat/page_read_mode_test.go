package chat

import (
	"context"
	"sort"
	"strings"
	"testing"
)

// The extension's chat carries text from a web page, which any site can fill
// with instructions. Its turns therefore get a tool set that cannot write,
// cannot reach personal data and cannot reach credentials: whatever a page
// talks the model into, there is nothing dangerous to call.

func toolNames(mt *MonoagentTools) []string {
	var names []string
	for _, d := range mt.ToolDefs() {
		names = append(names, d.Function.Name)
	}
	sort.Strings(names)
	return names
}

func TestPageReadModeRegistersOnlyTheReadTools(t *testing.T) {
	db := newMonoagentTestDB(t)
	full := NewMonoagentTools(db.DB, "/bin/monoagentcli")
	fullNames := toolNames(full)

	mt := NewMonoagentTools(db.DB, "/bin/monoagentcli")
	mt.SetPageReadOnly()
	got := toolNames(mt)

	want := append([]string(nil), PageReadToolNames...)
	sort.Strings(want)
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("page-read tools = %v, want %v", got, want)
	}
	if len(got) == 0 || len(got) >= len(fullNames) {
		t.Fatalf("page-read mode registers %d of %d tools", len(got), len(fullNames))
	}

	allowed := map[string]bool{}
	for _, n := range want {
		allowed[n] = true
	}
	for _, n := range fullNames {
		if allowed[n] {
			continue
		}
		// Not registered: the model is never offered it ...
		for _, g := range got {
			if g == n {
				t.Errorf("%s is registered in page-read mode", n)
			}
		}
		// ... and even a call made up out of thin air does nothing.
		if _, err := mt.Execute(n, `{"confirm":true}`); err == nil || !strings.Contains(err.Error(), "not available") {
			t.Errorf("%s: Execute err = %v, want a 'not available' refusal", n, err)
		}
	}
}

func TestPageReadModeNamesTheSensitiveToolsAsExcluded(t *testing.T) {
	db := newMonoagentTestDB(t)
	mt := NewMonoagentTools(db.DB, "")
	mt.SetPageReadOnly()
	have := map[string]bool{}
	for _, n := range toolNames(mt) {
		have[n] = true
	}
	for _, n := range []string{
		"list_secrets", "add_secret", "update_secret", "delete_secret",
		"list_vault_items", "get_vault_item_path", "search_profile_documents", "save_document", "save_image",
		"list_messages", "get_message", "list_people", "get_person", "upsert_person", "delete_person",
		"create_workflow", "add_workflow_node", "set_workflow_active", "delete_workflow", "run_workflow",
		"create_org", "add_org_role", "update_org_role", "remove_org_role", "add_org_automation",
		"set_org_grant", "set_org_autonomy", "reload_org", "register_publication", "list_publications",
		"list_social_lists", "list_templates", "list_orgs", "get_org",
	} {
		if have[n] {
			t.Errorf("%s must not exist in page-read mode", n)
		}
	}
}

func TestPageReadModeLeavesTheReadToolsWorking(t *testing.T) {
	db := newMonoagentTestDB(t)
	mt := NewMonoagentTools(db.DB, "")
	mt.SetPageReadOnly()
	if err := mt.checkInjectionGate("save_document"); err == nil {
		t.Fatal("page-read mode must count the page as untrusted content")
	}
	if _, err := mt.Execute("list_workflows", `{}`); err != nil {
		t.Fatalf("list_workflows: %v", err)
	}
	if _, err := mt.Execute("list_node_types", `{}`); err != nil {
		t.Fatalf("list_node_types: %v", err)
	}
}

// Everything outside the page-read mode stays exactly as it was: the same
// definitions, the same dispatch.
func TestDefaultModeStillOffersEveryTool(t *testing.T) {
	db := newMonoagentTestDB(t)
	mt := NewMonoagentTools(db.DB, "")
	have := map[string]bool{}
	for _, n := range toolNames(mt) {
		have[n] = true
	}
	for _, n := range []string{"list_secrets", "create_workflow", "add_workflow_node", "set_workflow_active", "list_messages", "get_person", "set_org_grant"} {
		if !have[n] {
			t.Errorf("%s missing from the default tool set", n)
		}
	}
}

// Page text counts as untrusted content: a gated tool refuses once it has
// been seen, whatever confirm says.
func TestPageContextTripsTheInjectionGate(t *testing.T) {
	db := newMonoagentTestDB(t)
	mt := NewMonoagentTools(db.DB, "/bin/monoagentcli")
	mt.MarkPageContextSeen()
	stubRunSelfExec(t, func(ctx context.Context, bin string, args ...string) ([]byte, error) {
		t.Fatal("exec must not run")
		return nil, nil
	})
	for _, tool := range []string{"set_org_grant", "set_org_autonomy"} {
		_, err := mt.Execute(tool, `{"org_name":"growth","role_id":"lead","automation":"a","level":"full","confirm":true}`)
		if err == nil || !strings.Contains(err.Error(), "web page") {
			t.Fatalf("%s: err = %v, want a web page refusal", tool, err)
		}
	}
	for _, tool := range []string{"save_document", "save_image", "register_publication"} {
		if err := mt.checkInjectionGate(tool); err == nil {
			t.Errorf("%s not gated after page context", tool)
		}
	}
}
