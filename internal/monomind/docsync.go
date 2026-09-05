package monomind

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"time"

	"github.com/monoes/mono-agent/internal/profiledir"
)

// IngestDocument best-effort indexes the file at path into profileID's
// Second Brain via `monomind mcp exec -t knowledge_ingest`. Mirrors
// SyncToKnowledgeGraph's subprocess/env-var pattern exactly — see that
// function's comment in kgsync.go for why MONOMIND_CWD and creating the
// profile dir first are both required, not optional. Does not pass
// --format json: only the exit code matters here, the inner payload is
// unused (matching SyncToKnowledgeGraph's own fire-and-forget style).
func IngestDocument(ctx context.Context, db *sql.DB, profileID, path string) error {
	bin, err := Find()
	if err != nil {
		return err
	}

	profileDir := profiledir.MonomindDir(db, profileID)
	if err := os.MkdirAll(profileDir, 0700); err != nil {
		return fmt.Errorf("monomind.IngestDocument: creating profile monomind dir: %w", err)
	}

	params, err := json.Marshal(map[string]string{"path": path})
	if err != nil {
		return fmt.Errorf("monomind.IngestDocument: marshal params: %w", err)
	}

	cctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()

	cmd := exec.CommandContext(cctx, bin, "mcp", "exec", "-t", "knowledge_ingest", "-p", string(params))
	cmd.Env = append(FilteredEnviron(), "MONOMIND_CWD="+profileDir)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("monomind.IngestDocument: knowledge_ingest: %w", err)
	}
	return nil
}

// KnowledgeResult is one matching excerpt from SearchKnowledge.
type KnowledgeResult struct {
	Path    string
	Excerpt string
	Score   float64
}

// cliEnvelope is the outer JSON `monomind mcp exec ... --format json` prints.
type cliEnvelope struct {
	Result struct {
		Content []struct {
			Text string `json:"text"`
		} `json:"content"`
	} `json:"result"`
}

// knowledgeSearchPayload is the inner JSON, itself encoded as a string
// inside cliEnvelope.Result.Content[0].Text — see docs/mastermind/specs/
// 2026-09-05-profile-documents-design.md's "Protocol note" section for why
// this is decoded twice, not once.
type knowledgeSearchPayload struct {
	Success bool `json:"success"`
	Results []struct {
		Kind       string  `json:"kind"`
		FilePath   string  `json:"filePath"`
		Text       string  `json:"text"`
		Similarity float64 `json:"similarity"`
	} `json:"results"`
}

// SearchKnowledge queries profileID's Second Brain via
// `monomind mcp exec -t knowledge_search`, scoped to store="project" (this
// profile's own ingested documents — never "global"/"all", which would
// pull in the user's personal cross-project brain). Only "excerpt"-kind
// results are returned; knowledge-graph/rule/memory result kinds are
// filtered out (out of scope — see the design spec).
func SearchKnowledge(ctx context.Context, db *sql.DB, profileID, query string) ([]KnowledgeResult, error) {
	bin, err := Find()
	if err != nil {
		return nil, err
	}

	profileDir := profiledir.MonomindDir(db, profileID)
	if err := os.MkdirAll(profileDir, 0700); err != nil {
		return nil, fmt.Errorf("monomind.SearchKnowledge: creating profile monomind dir: %w", err)
	}

	params, err := json.Marshal(map[string]string{"query": query, "store": "project"})
	if err != nil {
		return nil, fmt.Errorf("monomind.SearchKnowledge: marshal params: %w", err)
	}

	cctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	cmd := exec.CommandContext(cctx, bin, "mcp", "exec", "-t", "knowledge_search", "-p", string(params), "--format", "json")
	cmd.Env = append(FilteredEnviron(), "MONOMIND_CWD="+profileDir)
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("monomind.SearchKnowledge: knowledge_search: %w", err)
	}

	var envelope cliEnvelope
	if err := json.Unmarshal(out, &envelope); err != nil {
		return nil, fmt.Errorf("monomind.SearchKnowledge: decoding CLI envelope: %w", err)
	}
	if len(envelope.Result.Content) == 0 {
		return nil, fmt.Errorf("monomind.SearchKnowledge: empty response content")
	}

	var payload knowledgeSearchPayload
	if err := json.Unmarshal([]byte(envelope.Result.Content[0].Text), &payload); err != nil {
		return nil, fmt.Errorf("monomind.SearchKnowledge: decoding tool payload: %w", err)
	}

	results := make([]KnowledgeResult, 0, len(payload.Results))
	for _, r := range payload.Results {
		if r.Kind != "excerpt" {
			continue
		}
		results = append(results, KnowledgeResult{Path: r.FilePath, Excerpt: r.Text, Score: r.Similarity})
	}
	return results, nil
}
