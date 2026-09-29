package monomind

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// writeFakeScript writes an executable shell script standing in for a real
// runtime binary — same pattern TestScanFailsFastOnIncompatibleMonomind
// uses (see exec_test.go) — and returns its path.
func writeFakeScript(t *testing.T, script string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "fake-bin")
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestListModelsUnknownRuntimeReturnsNilNil(t *testing.T) {
	models, err := builtinModels(context.Background(), "some-future-runtime", "")
	if err != nil {
		t.Fatalf("ListModels(unknown runtime) error = %v, want nil (caller falls back to free text)", err)
	}
	if models != nil {
		t.Fatalf("ListModels(unknown runtime) = %v, want nil", models)
	}
}

func TestListModelsClaudeIsStatic(t *testing.T) {
	models, err := builtinModels(context.Background(), "claude", "")
	if err != nil {
		t.Fatalf("ListModels(claude) unexpected error: %v", err)
	}
	if len(models) == 0 {
		t.Fatal("ListModels(claude) returned no models")
	}
	for _, m := range models {
		if m.ID == "" || m.Label == "" {
			t.Errorf("claude model with empty ID/Label: %+v", m)
		}
	}
}

func TestListModelsAntigravityParsesTabSeparatedOutput(t *testing.T) {
	script := "#!/bin/sh\n" +
		"echo 'Fetching available models...'\n" +
		"echo 'gemini-3.8-flash-high\tGemini 3.8 Flash (High)'\n" +
		"echo ''\n" + // blank line — must be skipped
		"echo 'claude-sonnet-4-6\tClaude Sonnet 4.6 (Thinking)'\n"
	bin := writeFakeScript(t, script)

	models, err := builtinModels(context.Background(), "antigravity", bin)
	if err != nil {
		t.Fatalf("ListModels(antigravity) unexpected error: %v", err)
	}
	want := []RuntimeModel{
		{ID: "gemini-3.8-flash-high", Label: "Gemini 3.8 Flash (High)"},
		{ID: "claude-sonnet-4-6", Label: "Claude Sonnet 4.6 (Thinking)"},
	}
	if len(models) != len(want) {
		t.Fatalf("got %d models, want %d: %+v", len(models), len(want), models)
	}
	for i, m := range models {
		if !reflect.DeepEqual(m, want[i]) {
			t.Errorf("model[%d] = %+v, want %+v", i, m, want[i])
		}
	}
}

func TestListModelsAntigravityRequiresBinary(t *testing.T) {
	if _, err := builtinModels(context.Background(), "antigravity", ""); err == nil {
		t.Fatal("expected an error when antigravity's binary path is empty, got nil")
	}
}

func TestListModelsCodexFiltersToListVisibilityOnly(t *testing.T) {
	script := "#!/bin/sh\ncat <<'EOF'\n" +
		`{"models":[` +
		`{"slug":"gpt-6-astra","display_name":"GPT-6-Astra","visibility":"list","supported_reasoning_levels":[{"effort":"low"},{"effort":"medium"},{"effort":"high"},{"effort":"ultra"}]},` +
		`{"slug":"gpt-reserve","display_name":"GPT-Reserve","visibility":"hide"},` +
		`{"slug":"codex-auto-review","display_name":"Codex Auto Review","visibility":"hide"},` +
		`{"slug":"gpt-5.5","display_name":"GPT-5.5","visibility":"list"}` +
		`]}` +
		"\nEOF\n"
	bin := writeFakeScript(t, script)

	models, err := builtinModels(context.Background(), "codex", bin)
	if err != nil {
		t.Fatalf("ListModels(codex) unexpected error: %v", err)
	}
	want := []RuntimeModel{
		{ID: "gpt-6-astra", Label: "GPT-6-Astra", EffortLevels: []string{"low", "medium", "high"}},
		{ID: "gpt-5.5", Label: "GPT-5.5"},
	}
	if len(models) != len(want) {
		t.Fatalf("got %d models, want %d (hidden entries should be filtered out): %+v", len(models), len(want), models)
	}
	for i, m := range models {
		if !reflect.DeepEqual(m, want[i]) {
			t.Errorf("model[%d] = %+v, want %+v", i, m, want[i])
		}
	}
}

func TestListModelsCodexRequiresBinary(t *testing.T) {
	if _, err := builtinModels(context.Background(), "codex", ""); err == nil {
		t.Fatal("expected an error when codex's binary path is empty, got nil")
	}
}

func TestCuratedModelsForUnlistedRuntimes(t *testing.T) {
	effort := map[string]bool{"off": true, "low": true, "medium": true, "high": true, "xhigh": true, "max": true}
	prefix := map[string]string{"cline": "", "aider": "openrouter/", "pi": "openrouter/"}
	for _, rt := range []string{"cline", "aider", "pi", "dsh"} {
		models, err := builtinModels(context.Background(), rt, "")
		if err != nil || len(models) == 0 {
			t.Fatalf("%s: %v, %v; want a curated list", rt, models, err)
		}
		free := 0
		seen := map[string]bool{}
		for _, m := range models {
			if m.ID == "" || m.Label == "" || seen[m.ID] {
				t.Errorf("%s: bad or duplicate entry %+v", rt, m)
			}
			seen[m.ID] = true
			for _, e := range m.EffortLevels {
				if !effort[e] {
					t.Errorf("%s %s: effort %q is not a monomind --effort level", rt, m.ID, e)
				}
			}
			if strings.HasSuffix(m.ID, ":free") {
				free++
			}
		}
		if free < len(freeOpenRouterModels) {
			t.Errorf("%s: %d free OpenRouter models, want all %d", rt, free, len(freeOpenRouterModels))
		}
		if p, ok := prefix[rt]; ok && models[0].ID != p+freeOpenRouterModels[0].id {
			t.Errorf("%s: first id %q, want %q", rt, models[0].ID, p+freeOpenRouterModels[0].id)
		}
	}
}
