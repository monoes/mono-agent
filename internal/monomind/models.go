package monomind

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// RuntimeModel is one selectable model for an agent runtime — an id to pass
// as --model plus a human-readable label for the picker.
type RuntimeModel struct {
	ID    string `json:"id"`
	Label string `json:"label"`
}

// claudeModels is curated by hand: unlike antigravity and codex (see
// listAntigravityModels/listCodexModels), Claude Code has no CLI-level
// model-listing command — verified directly: `claude models` isn't a
// recognized subcommand, and since `claude [prompt]` treats any unrecognized
// first argument as a prompt to run, it silently launches a real chat turn
// asking the model to describe itself instead of failing, which is exactly
// the surprising/costly behavior this static list avoids triggering. Update
// this when new models ship.
var claudeModels = []RuntimeModel{
	{ID: "claude-opus-5", Label: "Opus 5"},
	{ID: "claude-sonnet-5", Label: "Sonnet 5"},
	{ID: "claude-haiku-4-5-20251001", Label: "Haiku 4.5"},
	{ID: "claude-fable-5-1", Label: "Fable 5.1"},
}

// ListModels returns the models selectable for runtimeID's --model flag.
// antigravity and codex both have a real discovery command (see
// listAntigravityModels/listCodexModels); claude falls back to the curated
// list above. binary is the runtime's own resolved path (from a ScanEntry)
// — required for antigravity/codex, ignored otherwise. An unknown runtimeID
// returns a nil, nil slice so callers can fall back to a plain free-text
// model field.
func ListModels(ctx context.Context, runtimeID, binary string) ([]RuntimeModel, error) {
	switch runtimeID {
	case "antigravity":
		if binary == "" {
			return nil, fmt.Errorf("listing antigravity models: no resolved binary path (is it installed?)")
		}
		return listAntigravityModels(ctx, binary)
	case "codex":
		if binary == "" {
			return nil, fmt.Errorf("listing codex models: no resolved binary path (is it installed?)")
		}
		return listCodexModels(ctx, binary)
	case "claude":
		return claudeModels, nil
	default:
		return nil, nil
	}
}

// listAntigravityModels runs `agy models`, which prints one
// "<id>\t<label>" pair per line (verified directly against a real
// installation — no --json/--output-format flag exists for this
// subcommand, so plain-text parsing is the only option).
func listAntigravityModels(ctx context.Context, binary string) ([]RuntimeModel, error) {
	cctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	out, err := exec.CommandContext(cctx, binary, "models").Output()
	if err != nil {
		return nil, fmt.Errorf("agy models: %w", err)
	}

	var models []RuntimeModel
	sc := bufio.NewScanner(strings.NewReader(string(out)))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		id, label, ok := strings.Cut(line, "\t")
		if !ok {
			// Not a "<id>\t<label>" data row (e.g. the leading "Fetching
			// available models..." status line) — skip rather than error,
			// so a harmless banner line doesn't sink the whole result.
			continue
		}
		id, label = strings.TrimSpace(id), strings.TrimSpace(label)
		if id == "" {
			continue
		}
		if label == "" {
			label = id
		}
		models = append(models, RuntimeModel{ID: id, Label: label})
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("agy models: reading output: %w", err)
	}
	return models, nil
}

// codexModelCatalog mirrors the fields of `codex debug models`' JSON output
// that matter here — that command dumps far more per-model metadata (full
// system-prompt text, reasoning-effort tiers, etc.) which is irrelevant to
// a model picker and deliberately left unparsed.
type codexModelCatalog struct {
	Models []struct {
		Slug        string `json:"slug"`
		DisplayName string `json:"display_name"`
		Visibility  string `json:"visibility"`
	} `json:"models"`
}

// listCodexModels runs `codex debug models` ("Render the raw model catalog
// as JSON" — verified directly against a real installation) and keeps only
// entries marked visibility:"list", the same filter Codex's own UI uses to
// hide internal/reserved slugs (e.g. "gpt-reserve", "codex-auto-review")
// from a picker.
func listCodexModels(ctx context.Context, binary string) ([]RuntimeModel, error) {
	cctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	out, err := exec.CommandContext(cctx, binary, "debug", "models").Output()
	if err != nil {
		return nil, fmt.Errorf("codex debug models: %w", err)
	}

	var catalog codexModelCatalog
	if err := json.Unmarshal(out, &catalog); err != nil {
		return nil, fmt.Errorf("codex debug models: unparseable output: %w", err)
	}

	models := make([]RuntimeModel, 0, len(catalog.Models))
	for _, m := range catalog.Models {
		if m.Visibility != "list" || m.Slug == "" {
			continue
		}
		label := m.DisplayName
		if label == "" {
			label = m.Slug
		}
		models = append(models, RuntimeModel{ID: m.Slug, Label: label})
	}
	return models, nil
}
