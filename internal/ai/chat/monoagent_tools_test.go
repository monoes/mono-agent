package chat

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"monoagent/internal/storage"

	"github.com/zalando/go-keyring"
)

func newMonoagentTestDB(t *testing.T) *storage.Database {
	t.Helper()
	keyring.MockInit()
	dbPath := filepath.Join(t.TempDir(), "monoagent-tools-test.db")
	db, err := storage.NewDatabase(dbPath)
	if err != nil {
		t.Fatalf("NewDatabase failed: %v", err)
	}
	if err := db.ApplyMigrations(); err != nil {
		t.Fatalf("ApplyMigrations failed: %v", err)
	}
	t.Cleanup(func() { db.DB.Close() })
	return db
}

func mustJSON(t *testing.T, s string, v interface{}) {
	t.Helper()
	if err := json.Unmarshal([]byte(s), v); err != nil {
		t.Fatalf("unmarshal %q failed: %v", s, err)
	}
}

func TestMonoagentTools_ProfileIsolation(t *testing.T) {
	db := newMonoagentTestDB(t)

	if _, err := db.DB.Exec("INSERT INTO workflows (id, name, profile_id) VALUES ('wf-a', 'A workflow', 'profile-a')"); err != nil {
		t.Fatalf("seed workflow failed: %v", err)
	}
	if _, err := db.DB.Exec(
		"INSERT INTO people (id, platform_username, platform, profile_id) VALUES ('person-a', 'alice', 'instagram', 'profile-a')",
	); err != nil {
		t.Fatalf("seed person failed: %v", err)
	}
	if _, err := db.DB.Exec(
		"INSERT INTO actions (id, created_at, title, type, state, target_platform, profile_id) VALUES ('action-a', 0, 'A action', 'dm', 'PENDING', 'instagram', 'profile-a')",
	); err != nil {
		t.Fatalf("seed action failed: %v", err)
	}

	mtB := NewMonoagentTools(db.DB, "")
	mtB.SetProfileID("profile-b")

	out, err := mtB.Execute("list_workflows", "{}")
	if err != nil {
		t.Fatalf("list_workflows failed: %v", err)
	}
	var wfRes struct {
		Workflows []workflowSummary `json:"workflows"`
	}
	mustJSON(t, out, &wfRes)
	if len(wfRes.Workflows) != 0 {
		t.Errorf("list_workflows leaked across profiles: got %d, want 0", len(wfRes.Workflows))
	}

	out, err = mtB.Execute("list_people", "{}")
	if err != nil {
		t.Fatalf("list_people failed: %v", err)
	}
	var peopleRes struct {
		People []personSummary `json:"people"`
	}
	mustJSON(t, out, &peopleRes)
	if len(peopleRes.People) != 0 {
		t.Errorf("list_people leaked across profiles: got %d, want 0", len(peopleRes.People))
	}

	out, err = mtB.Execute("list_actions", "{}")
	if err != nil {
		t.Fatalf("list_actions failed: %v", err)
	}
	var actionsRes struct {
		Actions []actionSummary `json:"actions"`
	}
	mustJSON(t, out, &actionsRes)
	if len(actionsRes.Actions) != 0 {
		t.Errorf("list_actions leaked across profiles: got %d, want 0", len(actionsRes.Actions))
	}

	if _, err := mtB.Execute("get_workflow", `{"workflow_id":"wf-a"}`); err == nil {
		t.Error("get_workflow crossed profile boundary: expected an error, got none")
	}
	if _, err := mtB.Execute("get_person", `{"person_id":"person-a"}`); err == nil {
		t.Error("get_person crossed profile boundary: expected an error, got none")
	}
	if _, err := mtB.Execute("get_action", `{"action_id":"action-a"}`); err == nil {
		t.Error("get_action crossed profile boundary: expected an error, got none")
	}

	mtA := NewMonoagentTools(db.DB, "")
	mtA.SetProfileID("profile-a")
	out, err = mtA.Execute("list_workflows", "{}")
	if err != nil {
		t.Fatalf("list_workflows (owner) failed: %v", err)
	}
	mustJSON(t, out, &wfRes)
	if len(wfRes.Workflows) != 1 {
		t.Fatalf("list_workflows (owner): got %d, want 1", len(wfRes.Workflows))
	}
	if _, err := mtA.Execute("get_workflow", `{"workflow_id":"wf-a"}`); err != nil {
		t.Errorf("get_workflow (owner) unexpected error: %v", err)
	}
}

func TestMonoagentTools_SecretsNeverExposeValues(t *testing.T) {
	db := newMonoagentTestDB(t)
	mt := NewMonoagentTools(db.DB, "")
	mt.SetProfileID("default")

	fakeCredentialValue := "not-a-real-credential-value-98765"
	fakeNote := "do not leak this note either"
	addArgs, _ := json.Marshal(map[string]interface{}{
		"kind":   "secret",
		"name":   "my-api-key",
		"fields": map[string]string{"value": fakeCredentialValue},
		"notes":  fakeNote,
	})
	out, err := mt.Execute("add_secret", string(addArgs))
	if err != nil {
		t.Fatalf("add_secret failed: %v", err)
	}
	if strings.Contains(out, fakeCredentialValue) {
		t.Fatalf("add_secret response leaked the credential value: %s", out)
	}
	var addRes struct {
		ID        string `json:"id"`
		Reference string `json:"reference"`
	}
	mustJSON(t, out, &addRes)
	if addRes.Reference == "" || !strings.HasPrefix(addRes.Reference, "@") {
		t.Errorf("add_secret returned reference %q without the expected at-sign prefix", addRes.Reference)
	}

	listOut, err := mt.Execute("list_secrets", "")
	if err != nil {
		t.Fatalf("list_secrets failed: %v", err)
	}
	if strings.Contains(listOut, fakeCredentialValue) || strings.Contains(listOut, fakeNote) {
		t.Fatalf("list_secrets leaked a value or note: %s", listOut)
	}

	newValue := "a-different-still-fake-credential-value"
	updateArgs, _ := json.Marshal(map[string]interface{}{
		"id":     addRes.ID,
		"fields": map[string]string{"value": newValue},
	})
	updOut, err := mt.Execute("update_secret", string(updateArgs))
	if err != nil {
		t.Fatalf("update_secret failed: %v", err)
	}
	if strings.Contains(updOut, newValue) {
		t.Fatalf("update_secret response leaked the new value: %s", updOut)
	}
}
