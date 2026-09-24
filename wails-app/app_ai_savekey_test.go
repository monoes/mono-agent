package main

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/zalando/go-keyring"

	"github.com/monoes/mono-agent/internal/ai"
	"github.com/monoes/mono-agent/internal/storage"
)

// The AI connection edit form never gets the stored key (#146 item 10), so
// a save with the key left blank must keep it, and a typed key replaces it.
func TestSaveAIProviderKeepsKeyWhenLeftBlank(t *testing.T) {
	keyring.MockInit()
	db, err := storage.NewDatabase(t.TempDir() + "/ai.db")
	if err != nil {
		t.Fatal(err)
	}
	if err := db.ApplyMigrations(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.DB.Close() })
	store, err := ai.NewAIStore(db.DB)
	if err != nil {
		t.Fatal(err)
	}
	a := &App{aiStore: store}

	save := func(body map[string]any) map[string]any {
		t.Helper()
		b, _ := json.Marshal(body)
		out := a.SaveAIProvider(string(b))
		if strings.Contains(out, `"error"`) {
			t.Fatalf("SaveAIProvider: %s", out)
		}
		var m map[string]any
		_ = json.Unmarshal([]byte(out), &m)
		return m
	}
	created := save(map[string]any{"name": "OpenAI", "provider_id": "openai", "api_key": "sk-original"})
	id := created["id"].(string)
	if created["api_key"] != "" {
		t.Fatalf("the key must not come back to the GUI: %v", created["api_key"])
	}
	key := func() string {
		p, err := store.GetProvider(id, "default")
		if err != nil {
			t.Fatal(err)
		}
		return p.APIKey
	}

	// What the edit form sends: the listed provider (no key, and here not
	// even its vault_ref) with a blank key and a new name.
	save(map[string]any{"id": id, "name": "Renamed", "provider_id": "openai", "api_key": ""})
	if got := key(); got != "sk-original" {
		t.Fatalf("key after a blank save = %q, want the stored one", got)
	}
	save(map[string]any{"id": id, "name": "Renamed", "provider_id": "openai", "api_key": "sk-new"})
	if got := key(); got != "sk-new" {
		t.Fatalf("key after typing a new one = %q", got)
	}
}
