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

// ingestPayload is the inner JSON, itself encoded as a string inside
// cliEnvelope.Result.Content[0].Text -- monomind's knowledge_ingest tool
// can report failure this way (e.g. its own path-traversal guard rejecting
// a path outside the resolved project root) while the subprocess itself
// still exits 0, so Content[0].Text must be decoded and checked, not just
// the process exit code.
type ingestPayload struct {
	Success bool   `json:"success"`
	Error   string `json:"error"`
}

// IngestDocument best-effort indexes the file at path into profileID's
// Second Brain via `monomind mcp exec -t knowledge_ingest`. Mirrors
// SyncToKnowledgeGraph's subprocess/env-var pattern for MONOMIND_CWD and
// creating the profile dir first — see that function's comment in
// kgsync.go.
//
// cmd.Dir is set to the profile's root (the parent of both vault/ and
// .monomind/), not just MONOMIND_CWD's value — verified directly against a
// real monomind install: knowledge_ingest's own path-traversal guard
// rejects any path outside the subprocess's actual OS working directory
// ("Absolute path must not escape the current working directory"), and
// vault documents live in a sibling directory of .monomind/, not a
// descendant of it. Without this, every ingest attempt fails inside the
// tool while the subprocess still exits 0 -- confirmed directly: omitting
// cmd.Dir here silently indexes nothing, ever, no matter how many
// documents are uploaded.
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

	cmd := exec.CommandContext(cctx, bin, "mcp", "exec", "-t", "knowledge_ingest", "-p", string(params), "--format", "json")
	cmd.Dir = profiledir.Root(db, profileID)
	cmd.Env = append(FilteredEnviron(), "MONOMIND_CWD="+profileDir)
	out, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("monomind.IngestDocument: knowledge_ingest: %w", err)
	}

	jsonStr, err := extractJSONObject(string(out))
	if err != nil {
		return fmt.Errorf("monomind.IngestDocument: %w", err)
	}
	var envelope cliEnvelope
	if err := json.Unmarshal([]byte(jsonStr), &envelope); err != nil {
		return fmt.Errorf("monomind.IngestDocument: decoding CLI envelope: %w", err)
	}
	if len(envelope.Result.Content) == 0 {
		return fmt.Errorf("monomind.IngestDocument: empty response content")
	}

	var payload ingestPayload
	if err := json.Unmarshal([]byte(envelope.Result.Content[0].Text), &payload); err != nil {
		return fmt.Errorf("monomind.IngestDocument: decoding tool payload: %w", err)
	}
	// Exit code 0 alone does not mean the tool succeeded -- check both the
	// envelope-level flag and the inner payload's own success field.
	if envelope.Result.IsError || !payload.Success {
		if payload.Error != "" {
			return fmt.Errorf("monomind.IngestDocument: knowledge_ingest: %s", payload.Error)
		}
		return fmt.Errorf("monomind.IngestDocument: knowledge_ingest reported failure with no error message")
	}
	return nil
}

// extractJSONObject returns the LAST balanced top-level {...} object found
// in s, correctly skipping over braces inside quoted string values. It
// must be the last, not the first: real monomind's own "Parameters: ..."
// pollution line (see below) echoes the tool's own -p argument, which is
// itself a JSON object -- taking the first balanced object would return
// that request echo instead of the actual response envelope that follows
// it. Real monomind prints these human-readable "Parameters: ..." /
// "[OK] Tool executed in Nms" lines to stdout ahead of the JSON envelope
// even with --format json (verified directly against monomind 2.10.10) --
// this makes both IngestDocument and SearchKnowledge robust to that
// instead of assuming stdout is pure JSON. Duplicates internal/matching's
// own extractJSON rather than importing it: internal/matching imports this
// package (for its agent.ask pattern), so the reverse import would cycle.
func extractJSONObject(s string) (string, error) {
	var lastStart, lastEnd int
	found := false
	inString := false
	escaped := false
	depth := 0
	start := -1
	for i := 0; i < len(s); i++ {
		c := s[i]
		if inString {
			switch {
			case escaped:
				escaped = false
			case c == '\\':
				escaped = true
			case c == '"':
				inString = false
			}
			continue
		}
		switch c {
		case '"':
			inString = true
		case '{':
			if depth == 0 {
				start = i
			}
			depth++
		case '}':
			if depth > 0 {
				depth--
				if depth == 0 && start != -1 {
					lastStart, lastEnd = start, i+1
					found = true
					start = -1
				}
			}
		}
	}
	if !found {
		return "", fmt.Errorf("no balanced JSON object found in monomind output")
	}
	return s[lastStart:lastEnd], nil
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
		IsError bool `json:"isError"`
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

	jsonStr, err := extractJSONObject(string(out))
	if err != nil {
		return nil, fmt.Errorf("monomind.SearchKnowledge: %w", err)
	}
	var envelope cliEnvelope
	if err := json.Unmarshal([]byte(jsonStr), &envelope); err != nil {
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
