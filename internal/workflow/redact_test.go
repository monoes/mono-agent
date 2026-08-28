package workflow

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestRedactItemsMasksSensitiveKeys(t *testing.T) {
	items := []map[string]any{{
		"token":         "sk-super-secret",
		"PASSWORD":      "hunter2",
		"Api_Key":       "key123",
		"client_secret": "cs-1",
		"bearer":        "b",
	}}
	got := RedactItems(items)
	for _, key := range []string{"token", "PASSWORD", "Api_Key", "client_secret", "bearer"} {
		if v, _ := got[0][key].(string); v != "***" {
			t.Errorf("key %q = %v, want ***", key, got[0][key])
		}
	}
}

func TestRedactItemsNestedAndArrays(t *testing.T) {
	items := []map[string]any{{
		"user": map[string]any{
			"name":     "alice",
			"password": "hunter2",
			"contacts": []any{
				map[string]any{"type": "email", "address": "a@b.c"},
				map[string]any{"type": "api", "access_token": "tok-1"},
			},
		},
		"tags": []any{"x", "y"},
	}}
	got := RedactItems(items)[0]
	user := got["user"].(map[string]any)
	if user["name"] != "alice" {
		t.Errorf("name = %v, want alice (non-matching key must pass through)", user["name"])
	}
	if user["password"] != "***" {
		t.Errorf("nested password = %v, want ***", user["password"])
	}
	contacts := user["contacts"].([]any)
	if contacts[0].(map[string]any)["address"] != "a@b.c" {
		t.Errorf("non-matching nested key altered: %v", contacts[0])
	}
	if contacts[1].(map[string]any)["access_token"] != "***" {
		t.Errorf("nested access_token = %v, want ***", contacts[1])
	}
	if got["tags"].([]any)[0] != "x" {
		t.Errorf("scalar array altered: %v", got["tags"])
	}
}

func TestRedactItemsNonMatchingKeysUntouched(t *testing.T) {
	// Values that LOOK like secrets under innocuous keys are NOT masked:
	// the redactor is key-match-only and never scans content.
	items := []map[string]any{{
		"body":          "password=hunter2 token=abc",
		"tokens_count":  3,
		"authorized":    true,
		"secret_note":   "keep",
		"Authorization": "Bearer xyz",
	}}
	got := RedactItems(items)[0]
	if got["body"] != "password=hunter2 token=abc" {
		t.Errorf("body was content-scanned: %v", got["body"])
	}
	if got["tokens_count"] != 3 || got["authorized"] != true || got["secret_note"] != "keep" {
		t.Errorf("near-miss keys were masked: %+v", got)
	}
	if got["Authorization"] != "***" {
		t.Errorf("exact Authorization = %v, want ***", got["Authorization"])
	}
}

func TestRedactItemsDoesNotMutateInput(t *testing.T) {
	items := []map[string]any{{"token": "sk-1", "nested": map[string]any{"api_key": "k"}}}
	_ = RedactItems(items)
	if items[0]["token"] != "sk-1" || items[0]["nested"].(map[string]any)["api_key"] != "k" {
		t.Fatalf("RedactItems mutated its input: %+v", items)
	}
}

func TestRedactItemsNil(t *testing.T) {
	if got := RedactItems(nil); got != nil {
		t.Errorf("RedactItems(nil) = %v, want nil", got)
	}
}

func TestRedactAndTruncateItemsOrderAndShape(t *testing.T) {
	small := Item{JSON: map[string]any{"token": "sk-1", "note": "hi"}}
	big := Item{JSON: map[string]any{"token": "sk-2", "blob": strings.Repeat("x", MaxOutputItemBytes)}}

	got := RedactAndTruncateItems([]Item{small, big})
	if len(got) != 2 {
		t.Fatalf("item count must be preserved, got %d", len(got))
	}
	if got[0].JSON["token"] != "***" || got[0].JSON["note"] != "hi" {
		t.Errorf("small redacted item = %+v", got[0].JSON)
	}
	if truncated, _ := got[1].JSON["truncated"].(bool); !truncated {
		t.Errorf("oversized item must still be truncated after redaction: %+v", got[1].JSON)
	}
	if got[1].JSON["token"] != nil {
		t.Errorf("truncated stub should not carry the original token: %+v", got[1].JSON)
	}
}

func TestTruncateItemsMarshalEquivalence(t *testing.T) {
	small := Item{JSON: map[string]any{"k": "v"}}
	big := Item{JSON: map[string]any{"k": strings.Repeat("x", MaxOutputItemBytes+1)}}
	got := TruncateItems([]Item{small, big})
	b, err := json.Marshal(got[0])
	if err != nil || len(b) > MaxOutputItemBytes {
		t.Errorf("small item must pass through: %v %d", err, len(b))
	}
	if got[1].JSON["truncated"] != true {
		t.Errorf("big item must be replaced by stub: %+v", got[1].JSON)
	}
	if ob, _ := got[1].JSON["original_bytes"].(int); ob <= MaxOutputItemBytes {
		t.Errorf("original_bytes = %v, want > %d", got[1].JSON["original_bytes"], MaxOutputItemBytes)
	}
}
