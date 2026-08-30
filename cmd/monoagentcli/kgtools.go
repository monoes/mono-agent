package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/monoes/mono-agent/internal/monomind"
)

// monographSearchToolSpec exposes the profile's code/doc knowledge graph
// (monograph.db) to chat as a semantic search tool, on top of the
// structured SQL access MonoagentTools already provides.
func monographSearchToolSpec() monomind.ToolSpec {
	return monomind.ToolSpec{
		Name:        "monograph_search",
		Description: "Semantically search this profile's code/document knowledge graph (monograph) for relevant files, symbols, or notes.",
		Schema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"query": map[string]interface{}{"type": "string", "description": "Search query"},
				"limit": map[string]interface{}{"type": "number", "description": "Max results to return (default 15)"},
			},
			"required": []string{"query"},
		},
	}
}

// memoryKGSearchToolSpec exposes the structured entity/relation knowledge
// graph (memory_kg_search) to chat.
func memoryKGSearchToolSpec() monomind.ToolSpec {
	return monomind.ToolSpec{
		Name:        "memory_kg_search",
		Description: "Search this profile's structured entity/relation knowledge graph for facts and context relevant to a query.",
		Schema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"query": map[string]interface{}{"type": "string", "description": "Search query"},
			},
			"required": []string{"query"},
		},
	}
}

// runKGTool invokes the monomind CLI for the two knowledge-graph tools
// above, scoped to profileMonomindDir (a profile's .monomind directory).
func runKGTool(ctx context.Context, bin, name string, args json.RawMessage, profileMonomindDir string) (string, error) {
	var in struct {
		Query string `json:"query"`
		Limit int    `json:"limit"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return "", fmt.Errorf("invalid arguments: %w", err)
	}
	if strings.TrimSpace(in.Query) == "" {
		return "", fmt.Errorf("query is required")
	}

	// Must exist on disk before invoking monomind: it resolves MONOMIND_CWD's
	// project root by walking UP from it looking for an existing marker — a
	// not-yet-created profile dir gets skipped past entirely, landing on some
	// unrelated shared ancestor and silently merging distinct profiles'
	// knowledge graphs together (confirmed directly). See kgsync.go for the
	// same fix on the ingest side.
	if err := os.MkdirAll(profileMonomindDir, 0700); err != nil {
		return "", fmt.Errorf("creating profile monomind dir: %w", err)
	}

	cctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()

	switch name {
	case "monograph_search":
		limit := in.Limit
		if limit <= 0 {
			limit = 15
		}
		out, err := exec.CommandContext(cctx, bin, "monograph", "search",
			"-q", in.Query, "-p", profileMonomindDir, "-l", strconv.Itoa(limit)).Output()
		if err != nil {
			return "", fmt.Errorf("monograph search: %w", err)
		}
		return string(out), nil

	case "memory_kg_search":
		payload, err := json.Marshal(map[string]string{"query": in.Query, "dbPath": profileMonomindDir})
		if err != nil {
			return "", err
		}
		cmd := exec.CommandContext(cctx, bin, "mcp", "exec", "-t", "memory_kg_search", "-p", string(payload))
		// dbPath alone isn't honored by monomind's getDbPath() — it only
		// accepts a custom dbPath inside the resolved project root
		// (MONOMIND_CWD, or cwd if unset), so this env var is required for
		// the search to actually be scoped to this profile instead of
		// silently falling back to a shared default. See kgsync.go for the
		// same fix on the ingest side, where this was directly confirmed:
		// without it, two different profiles' searches returned identical
		// (wrong) results. FilteredEnviron() strips any ambient MONOMIND_*
		// overrides first so the explicit value below is the only one the
		// child sees (a duplicate inherited entry could otherwise shadow
		// the per-profile scoping).
		cmd.Env = append(monomind.FilteredEnviron(), "MONOMIND_CWD="+profileMonomindDir)
		out, err := cmd.Output()
		if err != nil {
			return "", fmt.Errorf("memory_kg_search: %w", err)
		}
		return extractResultJSON(string(out)), nil

	default:
		return "", fmt.Errorf("unknown tool: %s", name)
	}
}

// extractResultJSON pulls the JSON payload out of `monomind mcp exec`
// output, which prints [INFO]/[OK] lines, a bare "Result:" line, then the
// pretty-printed JSON result to EOF. Falls back to the first line starting
// with "{" if the exact "Result:" marker isn't found, and to the raw output
// if neither is present.
func extractResultJSON(out string) string {
	lines := strings.Split(out, "\n")
	for i, line := range lines {
		if strings.TrimSpace(line) == "Result:" {
			return strings.TrimSpace(strings.Join(lines[i+1:], "\n"))
		}
	}
	for i, line := range lines {
		if strings.HasPrefix(strings.TrimSpace(line), "{") {
			return strings.TrimSpace(strings.Join(lines[i:], "\n"))
		}
	}
	return strings.TrimSpace(out)
}
