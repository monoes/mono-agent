package ai

import (
	"database/sql"
	"encoding/json"
	"strings"
	"testing"

	"github.com/zalando/go-keyring"

	"monoagent/internal/storage"
)

// TestProviderJSONOmitsAPIKey is a regression test: serializing an AIProvider
// (as ListAIProviders/SaveAIProvider do for the GUI) must never leak the
// plaintext API key.
func TestProviderJSONOmitsAPIKey(t *testing.T) {
	p := AIProvider{
		ID:         "p1",
		Name:       "My OpenAI",
		ProviderID: "openai",
		Tier:       "known",
		APIKey:     "sk-supersecret",
	}
	b, err := json.Marshal(p)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(b), "sk-supersecret") {
		t.Errorf("serialized provider leaked API key: %s", b)
	}
	// A slice (as returned by ListProviders) must also be redacted.
	b2, _ := json.Marshal([]AIProvider{p})
	if strings.Contains(string(b2), "sk-supersecret") {
		t.Errorf("serialized provider slice leaked API key: %s", b2)
	}
}

func openTestDB(t *testing.T) *sql.DB {
	t.Helper()
	keyring.MockInit()
	db, err := storage.NewDatabase(t.TempDir() + "/ai-test.db")
	if err != nil {
		t.Fatalf("NewDatabase: %v", err)
	}
	if err := db.ApplyMigrations(); err != nil {
		t.Fatalf("ApplyMigrations: %v", err)
	}
	t.Cleanup(func() { db.DB.Close() })
	return db.DB
}

func TestStoreInitTables(t *testing.T) {
	db := openTestDB(t)
	store, err := NewAIStore(db)
	if err != nil {
		t.Fatalf("NewAIStore: %v", err)
	}
	_ = store

	// Verify ai_providers table exists by inserting a raw row.
	_, err = db.Exec(`INSERT INTO ai_providers (id, name, provider_id, tier, api_key, created_at)
		VALUES ('test', 'Test', 'openai', 'known', 'sk-xxx', '2025-01-01T00:00:00Z')`)
	if err != nil {
		t.Fatalf("insert into ai_providers: %v", err)
	}

	// Verify ai_chat_messages table exists by inserting a raw row.
	_, err = db.Exec(`INSERT INTO ai_chat_messages (id, workflow_id, role, content, created_at)
		VALUES ('msg1', 'wf1', 'user', 'hello', '2025-01-01T00:00:00Z')`)
	if err != nil {
		t.Fatalf("insert into ai_chat_messages: %v", err)
	}
}

func TestProviderCRUD(t *testing.T) {
	db := openTestDB(t)
	store, err := NewAIStore(db)
	if err != nil {
		t.Fatalf("NewAIStore: %v", err)
	}

	p := AIProvider{
		ID:           "p1",
		Name:         "My OpenAI",
		ProviderID:   "openai",
		Tier:         "known",
		APIKey:       "sk-abc",
		BaseURL:      "https://api.openai.com/v1",
		DefaultModel: "gpt-4o",
		Status:       "untested",
	}

	// Save
	if err := store.SaveProvider(p); err != nil {
		t.Fatalf("SaveProvider: %v", err)
	}

	// Get
	got, err := store.GetProvider("p1", "default")
	if err != nil {
		t.Fatalf("GetProvider: %v", err)
	}
	if got.Name != "My OpenAI" {
		t.Errorf("Name = %q, want %q", got.Name, "My OpenAI")
	}
	if got.APIKey != "sk-abc" {
		t.Errorf("APIKey = %q, want %q", got.APIKey, "sk-abc")
	}
	if got.CreatedAt == "" {
		t.Error("CreatedAt should be auto-set when empty")
	}

	// List
	providers, err := store.ListProviders("default")
	if err != nil {
		t.Fatalf("ListProviders: %v", err)
	}
	if len(providers) != 1 {
		t.Fatalf("ListProviders len = %d, want 1", len(providers))
	}

	// Update (re-save with new name)
	p.Name = "My OpenAI Updated"
	p.CreatedAt = got.CreatedAt // preserve original timestamp
	if err := store.SaveProvider(p); err != nil {
		t.Fatalf("SaveProvider (update): %v", err)
	}
	got2, err := store.GetProvider("p1", "default")
	if err != nil {
		t.Fatalf("GetProvider after update: %v", err)
	}
	if got2.Name != "My OpenAI Updated" {
		t.Errorf("Name after update = %q, want %q", got2.Name, "My OpenAI Updated")
	}

	// Delete
	if err := store.DeleteProvider("p1", "default"); err != nil {
		t.Fatalf("DeleteProvider: %v", err)
	}
	_, err = store.GetProvider("p1", "default")
	if err != sql.ErrNoRows {
		t.Errorf("GetProvider after delete: err = %v, want sql.ErrNoRows", err)
	}
}

// TestDeleteProvider_RemovesLinkedVaultEntry is a regression test: deleting a
// provider must also remove its linked vault_secrets row, mirroring
// connections.Store.Delete — otherwise the vault is left with an orphaned
// entry for a provider that no longer exists.
func TestDeleteProvider_RemovesLinkedVaultEntry(t *testing.T) {
	db := openTestDB(t)
	store, err := NewAIStore(db)
	if err != nil {
		t.Fatalf("NewAIStore: %v", err)
	}

	p := AIProvider{ID: "p1", Name: "My OpenAI", ProviderID: "openai", Tier: "known", APIKey: "sk-abc"}
	if err := store.SaveProvider(p); err != nil {
		t.Fatalf("SaveProvider: %v", err)
	}

	got, err := store.GetProvider("p1", "default")
	if err != nil {
		t.Fatalf("GetProvider: %v", err)
	}
	if got.VaultRef == "" {
		t.Fatal("expected a vault_ref to be set after SaveProvider")
	}

	if err := store.DeleteProvider("p1", "default"); err != nil {
		t.Fatalf("DeleteProvider: %v", err)
	}

	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM vault_secrets WHERE id = ?`, got.VaultRef).Scan(&count); err != nil {
		t.Fatalf("querying vault_secrets: %v", err)
	}
	if count != 0 {
		t.Fatalf("expected linked vault entry %s to be removed after DeleteProvider, got count=%d", got.VaultRef, count)
	}
}

// TestProviderProfileIsolation is a regression test: a provider saved under one
// profile must not be visible, testable, or deletable from a different profile.
func TestProviderProfileIsolation(t *testing.T) {
	db := openTestDB(t)
	store, err := NewAIStore(db)
	if err != nil {
		t.Fatalf("NewAIStore: %v", err)
	}

	p := AIProvider{ID: "p-a", Name: "Profile A Key", ProviderID: "openai", Tier: "known", APIKey: "sk-a", ProfileID: "profile-a"}
	if err := store.SaveProvider(p); err != nil {
		t.Fatalf("SaveProvider: %v", err)
	}

	if _, err := store.GetProvider("p-a", "profile-b"); err == nil {
		t.Error("GetProvider: expected error reading another profile's provider, got nil")
	}
	list, err := store.ListProviders("profile-b")
	if err != nil {
		t.Fatalf("ListProviders: %v", err)
	}
	if len(list) != 0 {
		t.Errorf("ListProviders(profile-b) = %d providers, want 0", len(list))
	}
	if err := store.DeleteProvider("p-a", "profile-b"); err != nil {
		t.Fatalf("DeleteProvider from wrong profile should not error (no rows affected): %v", err)
	}
	if _, err := store.GetProvider("p-a", "profile-a"); err != nil {
		t.Errorf("provider should still exist after cross-profile delete attempt: %v", err)
	}
}

func TestChatMessageCRUD(t *testing.T) {
	db := openTestDB(t)
	store, err := NewAIStore(db)
	if err != nil {
		t.Fatalf("NewAIStore: %v", err)
	}

	wfID := "workflow-1"

	// Save messages
	m1 := ChatMessage{
		ID:         "m1",
		WorkflowID: wfID,
		Role:       "user",
		Content:    "Hello",
		CreatedAt:  "2025-01-01T00:00:01Z",
	}
	m2 := ChatMessage{
		ID:         "m2",
		WorkflowID: wfID,
		Role:       "assistant",
		Content:    "Hi there!",
		ProviderID: "p1",
		Model:      "gpt-4o",
		TokenCount: 42,
		CreatedAt:  "2025-01-01T00:00:02Z",
	}

	if err := store.SaveChatMessage(m1); err != nil {
		t.Fatalf("SaveChatMessage m1: %v", err)
	}
	if err := store.SaveChatMessage(m2); err != nil {
		t.Fatalf("SaveChatMessage m2: %v", err)
	}

	// Get history
	history, err := store.GetChatHistory(wfID)
	if err != nil {
		t.Fatalf("GetChatHistory: %v", err)
	}
	if len(history) != 2 {
		t.Fatalf("GetChatHistory len = %d, want 2", len(history))
	}
	if history[0].ID != "m1" || history[1].ID != "m2" {
		t.Errorf("history order: got [%s, %s], want [m1, m2]", history[0].ID, history[1].ID)
	}
	if history[1].TokenCount != 42 {
		t.Errorf("TokenCount = %d, want 42", history[1].TokenCount)
	}

	// Clear history
	if err := store.ClearChatHistory(wfID); err != nil {
		t.Fatalf("ClearChatHistory: %v", err)
	}
	history, err = store.GetChatHistory(wfID)
	if err != nil {
		t.Fatalf("GetChatHistory after clear: %v", err)
	}
	if len(history) != 0 {
		t.Errorf("GetChatHistory after clear: len = %d, want 0", len(history))
	}
}

func TestSaveAndGetProvider_APIKeyGoesThroughVault(t *testing.T) {
	db := openTestDB(t)
	store, err := NewAIStore(db)
	if err != nil {
		t.Fatalf("NewAIStore: %v", err)
	}

	cred := "PLACEHOLDER-one"
	p := AIProvider{ID: "p1", Name: "My OpenAI"}
	p.ProviderID = "openai"
	p.Tier = "known"
	p.APIKey = cred
	if err := store.SaveProvider(p); err != nil {
		t.Fatalf("SaveProvider: %v", err)
	}

	var rawStoredValue, vaultRef string
	if err := db.QueryRow(`SELECT api_key, vault_ref FROM ai_providers WHERE id = 'p1'`).Scan(&rawStoredValue, &vaultRef); err != nil {
		t.Fatalf("reading raw row: %v", err)
	}
	if rawStoredValue != "" {
		t.Fatalf("expected ai_providers.api_key to be empty after save, got %q", rawStoredValue)
	}
	if vaultRef == "" {
		t.Fatal("expected vault_ref to be populated")
	}

	got, err := store.GetProvider("p1", "default")
	if err != nil {
		t.Fatalf("GetProvider: %v", err)
	}
	if got.APIKey != cred {
		t.Fatalf("expected GetProvider to resolve the real credential, got %q", got.APIKey)
	}

	list, err := store.ListProviders("default")
	if err != nil {
		t.Fatalf("ListProviders: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("expected one provider, got %d", len(list))
	}
	if list[0].APIKey != "" {
		t.Fatal("expected ListProviders to never decrypt the credential, same invariant as vault_secrets.List")
	}
}

func TestSaveProvider_UpdatingReusesSameVaultEntry(t *testing.T) {
	db := openTestDB(t)
	store, err := NewAIStore(db)
	if err != nil {
		t.Fatalf("NewAIStore: %v", err)
	}

	credA := "PLACEHOLDER-one"
	p := AIProvider{ID: "p1", Name: "My OpenAI"}
	p.ProviderID = "openai"
	p.Tier = "known"
	p.APIKey = credA
	if err := store.SaveProvider(p); err != nil {
		t.Fatalf("first SaveProvider: %v", err)
	}
	first, err := store.GetProvider("p1", "default")
	if err != nil {
		t.Fatalf("GetProvider: %v", err)
	}

	credB := "PLACEHOLDER-two"
	p.APIKey = credB
	p.VaultRef = first.VaultRef
	if err := store.SaveProvider(p); err != nil {
		t.Fatalf("second SaveProvider: %v", err)
	}
	second, err := store.GetProvider("p1", "default")
	if err != nil {
		t.Fatalf("GetProvider after update: %v", err)
	}
	if second.VaultRef != first.VaultRef {
		t.Fatalf("expected the same vault entry to be reused, got %q want %q", second.VaultRef, first.VaultRef)
	}
	if second.APIKey != credB {
		t.Fatalf("expected updated credential, got %q", second.APIKey)
	}
}

func TestProviderStatus(t *testing.T) {
	db := openTestDB(t)
	store, err := NewAIStore(db)
	if err != nil {
		t.Fatalf("NewAIStore: %v", err)
	}

	p := AIProvider{
		ID:         "ps1",
		Name:       "Status Test",
		ProviderID: "openai",
		Tier:       "known",
		APIKey:     "sk-test",
		Status:     "untested",
	}
	if err := store.SaveProvider(p); err != nil {
		t.Fatalf("SaveProvider: %v", err)
	}

	// Verify initial status
	got, err := store.GetProvider("ps1", "default")
	if err != nil {
		t.Fatalf("GetProvider: %v", err)
	}
	if got.Status != "untested" {
		t.Errorf("initial Status = %q, want %q", got.Status, "untested")
	}

	// Update status
	testedAt := "2025-06-15T12:00:00Z"
	if err := store.UpdateProviderStatus("ps1", "active", testedAt, "default"); err != nil {
		t.Fatalf("UpdateProviderStatus: %v", err)
	}

	got, err = store.GetProvider("ps1", "default")
	if err != nil {
		t.Fatalf("GetProvider after status update: %v", err)
	}
	if got.Status != "active" {
		t.Errorf("Status = %q, want %q", got.Status, "active")
	}
	if got.LastTested != testedAt {
		t.Errorf("LastTested = %q, want %q", got.LastTested, testedAt)
	}
}

// TestChatSessions covers the resumable-session grouping introduced for
// AIChatPanel's session continuity + past-sessions list: messages sharing a
// SessionID within one WorkflowID must group into one ChatSession, ordered
// most-recently-updated first, and GetSessionMessages must return only that
// session's rows.
func TestChatSessions(t *testing.T) {
	db := openTestDB(t)
	store, err := NewAIStore(db)
	if err != nil {
		t.Fatalf("NewAIStore: %v", err)
	}

	const wf = "general"
	save := func(id, role, content, sessionID, createdAt string) {
		t.Helper()
		if err := store.SaveChatMessage(ChatMessage{
			ID: id, WorkflowID: wf, Role: role, Content: content,
			ProviderID: "codex", Model: "gpt-5", SessionID: sessionID, CreatedAt: createdAt,
		}); err != nil {
			t.Fatalf("SaveChatMessage(%s): %v", id, err)
		}
	}

	// Session "s1": two turns, earlier.
	save("m1", "user", "hi, remember 42", "s1", "2026-01-01T00:00:00Z")
	save("m2", "assistant", "ok, 42 noted", "s1", "2026-01-01T00:00:01Z")
	save("m3", "user", "what number?", "s1", "2026-01-01T00:00:02Z")
	save("m4", "assistant", "42", "s1", "2026-01-01T00:00:03Z")
	// Session "s2": one turn, later — should sort first.
	save("m5", "user", "new topic entirely, this is a much longer message that should get truncated in the preview text shown in the past-sessions dropdown list", "s2", "2026-01-02T00:00:00Z")
	save("m6", "assistant", "sure", "s2", "2026-01-02T00:00:01Z")
	// A message with no session_id (legacy row) must be excluded entirely.
	save("m7", "user", "legacy row", "", "2026-01-03T00:00:00Z")

	sessions, err := store.ListChatSessions(wf)
	if err != nil {
		t.Fatalf("ListChatSessions: %v", err)
	}
	if len(sessions) != 2 {
		t.Fatalf("ListChatSessions returned %d sessions, want 2 (legacy no-session-id row must be excluded)", len(sessions))
	}
	if sessions[0].SessionID != "s2" {
		t.Errorf("sessions[0].SessionID = %q, want %q (most-recently-updated first)", sessions[0].SessionID, "s2")
	}
	if sessions[1].SessionID != "s1" {
		t.Errorf("sessions[1].SessionID = %q, want %q", sessions[1].SessionID, "s1")
	}
	if sessions[1].MessageCount != 4 {
		t.Errorf("sessions[1] (s1) MessageCount = %d, want 4", sessions[1].MessageCount)
	}
	if sessions[1].Runtime != "codex" || sessions[1].Model != "gpt-5" {
		t.Errorf("sessions[1] (s1) Runtime/Model = %q/%q, want codex/gpt-5", sessions[1].Runtime, sessions[1].Model)
	}
	if got := []rune(sessions[0].Preview); len(got) > 81 { // 80 chars + ellipsis
		t.Errorf("sessions[0].Preview not truncated: %d runes", len(got))
	}

	msgs, err := store.GetSessionMessages(wf, "s1")
	if err != nil {
		t.Fatalf("GetSessionMessages: %v", err)
	}
	if len(msgs) != 4 {
		t.Fatalf("GetSessionMessages(s1) returned %d messages, want 4", len(msgs))
	}
	for _, m := range msgs {
		if m.SessionID != "s1" {
			t.Errorf("GetSessionMessages(s1) returned a message from session %q", m.SessionID)
		}
	}
	if msgs[3].Content != "42" {
		t.Errorf("GetSessionMessages(s1)[3].Content = %q, want %q (ascending order)", msgs[3].Content, "42")
	}
}
