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
	ID           string   `json:"id"`
	Label        string   `json:"label"`
	Description  string   `json:"description,omitempty"`
	EffortLevels []string `json:"effort_levels,omitempty"`
}

// claudeModels is curated by hand: unlike antigravity and codex (see
// listAntigravityModels/listCodexModels), Claude Code has no CLI-level
// model-listing command — verified directly: `claude models` isn't a
// recognized subcommand, and since `claude [prompt]` treats any unrecognized
// first argument as a prompt to run, it silently launches a real chat turn
// asking the model to describe itself instead of failing, which is exactly
// the surprising/costly behavior this static list avoids triggering. The
// Agent SDK's supportedModels() is the real list (what Claude Code's /model
// picker shows); monomind exposes it as `agent models` (capability
// agent-models, see listAgentModels), and this copy of it as of 2026-09-28
// is only the fallback for a monomind without that capability.
var claudeModels = []RuntimeModel{
	{ID: "claude-opus-5-5", Label: "Opus 5.5", EffortLevels: []string{"low", "medium", "high", "xhigh", "max"}},
	{ID: "claude-fable-5-1", Label: "Fable 5.1", EffortLevels: []string{"low", "medium", "high", "xhigh", "max"}},
	{ID: "claude-sonnet-5", Label: "Sonnet 5", EffortLevels: []string{"low", "medium", "high", "xhigh", "max"}},
	{ID: "claude-haiku-4-5-20251001", Label: "Haiku 4.5"},
	{ID: "claude-opus-5", Label: "Opus 5", EffortLevels: []string{"low", "medium", "high", "xhigh", "max"}},
	{ID: "claude-fable-5", Label: "Fable 5", EffortLevels: []string{"low", "medium", "high", "xhigh", "max"}},
	{ID: "claude-opus-4-8", Label: "Opus 4.8", EffortLevels: []string{"low", "medium", "high", "xhigh", "max"}},
	{ID: "claude-opus-4-7", Label: "Opus 4.7", EffortLevels: []string{"low", "medium", "high", "xhigh", "max"}},
	{ID: "claude-opus-4-6", Label: "Opus 4.6", EffortLevels: []string{"low", "medium", "high", "max"}},
	{ID: "claude-sonnet-4-6", Label: "Sonnet 4.6", EffortLevels: []string{"low", "medium", "high", "max"}},
}

// ListModels returns the models selectable for runtimeID's --model flag.
// antigravity and codex both have a real discovery command (see
// listAntigravityModels/listCodexModels); claude falls back to the curated
// list above. binary is the runtime's own resolved path (from a ScanEntry)
// — required for antigravity/codex, ignored otherwise. An unknown runtimeID
// returns a nil, nil slice so callers can fall back to a plain free-text
// model field.
func ListModels(ctx context.Context, runtimeID, binary string) ([]RuntimeModel, error) {
	if set, err := Capabilities(ctx); err == nil && set.Has(CapAgentModels) {
		models, supported, err := listAgentModels(ctx, runtimeID)
		switch {
		case err == nil && !supported:
			return nil, nil
		case err == nil:
			return models, nil
		}
		// A failed listing (runtime not logged in, timed out) falls back to
		// the built-in sources rather than leaving the picker empty.
	}
	return builtinModels(ctx, runtimeID, binary)
}

// builtinModels is ListModels without monomind's agent models: the
// runtimes' own listing commands, and the curated claude list.
func builtinModels(ctx context.Context, runtimeID, binary string) ([]RuntimeModel, error) {
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
// that matter here: slug, display name, visibility, and supported reasoning levels.
type codexModelCatalog struct {
	Models []struct {
		Slug                     string `json:"slug"`
		DisplayName              string `json:"display_name"`
		Visibility               string `json:"visibility"`
		SupportedReasoningLevels []struct {
			Effort string `json:"effort"`
		} `json:"supported_reasoning_levels"`
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
		var efforts []string
		for _, l := range m.SupportedReasoningLevels {
			if l.Effort != "" {
				efforts = append(efforts, l.Effort)
			}
		}
		models = append(models, RuntimeModel{ID: m.Slug, Label: label, EffortLevels: efforts})
	}
	return models, nil
}

// CapAgentModels is `monomind agent models` (monomind#369).
const CapAgentModels = "agent-models"

// agentModelsResult is `monomind agent models --json` (protocol §12).
type agentModelsResult struct {
	Supported bool `json:"supported"`
	Models    []struct {
		ID           string   `json:"id"`
		ResolvedID   string   `json:"resolved_id"`
		Label        string   `json:"label"`
		Description  string   `json:"description"`
		Default      bool     `json:"default"`
		EffortLevels []string `json:"effort_levels"`
	} `json:"models"`
	Error *struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

// agentModelsTimeout bounds `agent models`; monomind's own limit is 30s.
var agentModelsTimeout = 45 * time.Second

// listAgentModels runs `monomind agent models --runtime <id> --json`, the
// runtime's own model list (for claude, what Claude Code's /model picker
// shows for this account). supported is false for a runtime with no
// listing command.
func listAgentModels(ctx context.Context, runtimeID string) (models []RuntimeModel, supported bool, err error) {
	bin, err := Find()
	if err != nil {
		return nil, false, err
	}
	cctx, cancel := context.WithTimeout(ctx, agentModelsTimeout)
	defer cancel()
	cmd := exec.CommandContext(cctx, bin, "agent", "models", "--runtime", runtimeID, "--json")
	cmd.Env = FilteredEnviron()
	out, runErr := cmd.Output()
	var res agentModelsResult
	if err := json.Unmarshal(lastJSONDocument(out), &res); err != nil {
		if runErr != nil {
			return nil, false, fmt.Errorf("monomind agent models: %w", runErr)
		}
		return nil, false, fmt.Errorf("monomind agent models: unparseable output: %w", err)
	}
	if res.Error != nil {
		return nil, false, fmt.Errorf("monomind agent models: %s: %s", res.Error.Code, res.Error.Message)
	}
	if runErr != nil {
		return nil, false, fmt.Errorf("monomind agent models: %w", runErr)
	}
	for _, m := range res.Models {
		if m.ID == "" {
			continue
		}
		models = append(models, RuntimeModel{
			ID:           m.ID,
			Label:        modelLabel(m.Label, m.Description, m.ID, m.Default),
			Description:  m.Description,
			EffortLevels: m.EffortLevels,
		})
	}
	return models, res.Supported, nil
}

// modelLabel names a listed model for a picker. Claude's aliases carry a
// generic label ("Opus", "Default (recommended)") and put the concrete
// model first in the description ("Opus 5.5 · Best for …"), so that name
// is used: "Opus 5.5", "Default (Opus 5.5)".
func modelLabel(label, description, id string, isDefault bool) string {
	concrete, _, found := strings.Cut(description, " · ")
	concrete = strings.TrimSpace(concrete)
	switch {
	case label == "":
		label = id
	case !found || concrete == "":
	case isDefault:
		label = "Default (" + concrete + ")"
	case strings.HasPrefix(concrete, label):
		label = concrete
	}
	return label
}

// lastJSONDocument returns out from its first '{' on, skipping any banner
// text a runtime printed before the JSON.
func lastJSONDocument(out []byte) []byte {
	if i := strings.IndexByte(string(out), '{'); i > 0 {
		return out[i:]
	}
	return out
}
