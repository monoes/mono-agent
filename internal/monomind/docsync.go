package monomind

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
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
	profileDir := profiledir.MonomindDir(db, profileID)
	if err := os.MkdirAll(profileDir, 0700); err != nil {
		return fmt.Errorf("monomind.IngestDocument: creating profile monomind dir: %w", err)
	}
	if err := runIngest(ctx, profiledir.Root(db, profileID), profileDir, map[string]string{"path": path}); err != nil {
		return fmt.Errorf("monomind.IngestDocument: %w", err)
	}
	return nil
}

// CaptureScope is the monomind knowledge scope holding profileID's browser
// captures: `profile:<id>`, a store of its own under monomind's global
// brain (packages/@monomind/cli/src/knowledge/profile-store.ts). It is ""
// for an id profiledir would not accept, so a hostile id never reaches an
// argv or a store path.
func CaptureScope(profileID string) string {
	id := strings.TrimSpace(profileID)
	if !profiledir.ValidProfileID(id) {
		return ""
	}
	return "profile:" + id
}

// IngestCapture indexes one browser capture (the envelope's primary
// artifact, normally readable.md) into profileID's capture store —
// CaptureScope, named explicitly rather than left to monomind's
// meta.json routing, so a capture that predates `meta.profile` lands in
// the same store as one that carries it.
//
// The working directory is the profile's DEFAULT root, not
// profiledir.Root: capture.ProfileInbox never follows a moved root_dir,
// and knowledge_ingest refuses a path outside its working directory. A
// profile whose root was moved would otherwise refuse every capture.
func IngestCapture(ctx context.Context, profileID, path string) error {
	scope := CaptureScope(profileID)
	if scope == "" {
		return fmt.Errorf("monomind.IngestCapture: unusable profile id %q", profileID)
	}
	monomindDir := profiledir.MonomindDir(nil, profileID)
	if err := os.MkdirAll(monomindDir, 0700); err != nil {
		return fmt.Errorf("monomind.IngestCapture: creating profile monomind dir: %w", err)
	}
	if err := runIngest(ctx, filepath.Dir(monomindDir), monomindDir, map[string]string{"path": path, "scope": scope}); err != nil {
		// The most common cause is a monomind that predates capture
		// indexing; say so on the row rather than leave "all chunk stores
		// failed" to be decoded.
		if set, capErr := Capabilities(ctx); capErr == nil && set != nil && !CaptureIndexingSupported(set.Version, set.Has(CapKnowledgeProfileCaptures)) {
			return fmt.Errorf("monomind.IngestCapture: %w (monomind %s cannot index every saved page; update to %s or newer: npm install -g @monoes/monomindcli@latest)",
				err, set.Version, CaptureCompanionsVersion)
		}
		return fmt.Errorf("monomind.IngestCapture: %w", err)
	}
	return nil
}

// runIngest runs one knowledge_ingest call in dir with MONOMIND_CWD set to
// monomindDir, and decides success from the tool's own payload.
func runIngest(ctx context.Context, dir, monomindDir string, params map[string]string) error {
	bin, err := findIn(dir)
	if err != nil {
		return err
	}
	raw, err := json.Marshal(params)
	if err != nil {
		return fmt.Errorf("marshal params: %w", err)
	}

	cctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()

	cmd := exec.CommandContext(cctx, bin, "mcp", "exec", "-t", "knowledge_ingest", "-p", string(raw), "--format", "json")
	cmd.Dir = dir
	cmd.Env = PinEnv(append(FilteredEnviron(), "MONOMIND_CWD="+monomindDir), bin)
	out, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("knowledge_ingest: %w", err)
	}

	jsonStr, err := extractJSONObject(string(out))
	if err != nil {
		return err
	}
	var envelope cliEnvelope
	if err := json.Unmarshal([]byte(jsonStr), &envelope); err != nil {
		return fmt.Errorf("decoding CLI envelope: %w", err)
	}
	if len(envelope.Result.Content) == 0 {
		return fmt.Errorf("empty response content")
	}

	var payload ingestPayload
	if err := json.Unmarshal([]byte(envelope.Result.Content[0].Text), &payload); err != nil {
		return fmt.Errorf("decoding tool payload: %w", err)
	}
	// Exit code 0 alone does not mean the tool succeeded -- check both the
	// envelope-level flag and the inner payload's own success field.
	if envelope.Result.IsError || !payload.Success {
		if payload.Error != "" {
			return fmt.Errorf("knowledge_ingest: %s", payload.Error)
		}
		return fmt.Errorf("knowledge_ingest reported failure with no error message")
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
// `monomind mcp exec -t knowledge_search` and returns the best excerpts
// from both of the profile's stores:
//
//   - its documents: store="project" in the profile's own monomind home
//     (uploads and discovered files; never "global"/"all", which would
//     pull in the user's personal cross-project brain);
//   - its browser captures: CaptureScope, the `profile:<id>` store
//     monomind files a capture saved into this profile under. Searched
//     for document excerpts only — the knowledge-graph, rule and memory
//     surfaces belong to whatever project the process runs in, not to the
//     profile.
//
// The two run in parallel and are merged by score. One store failing
// still returns the other's results; the error is returned only when both
// fail. Only "excerpt"-kind results are returned (see the design spec).
func SearchKnowledge(ctx context.Context, db *sql.DB, profileID, query string) ([]KnowledgeResult, error) {
	bin, err := Find()
	if err != nil {
		return nil, err
	}

	profileDir := profiledir.MonomindDir(db, profileID)
	if err := os.MkdirAll(profileDir, 0700); err != nil {
		return nil, fmt.Errorf("monomind.SearchKnowledge: creating profile monomind dir: %w", err)
	}

	type search struct {
		params  map[string]any
		dir     string
		results []KnowledgeResult
		err     error
	}
	searches := []*search{{params: map[string]any{"query": query, "store": "project"}, dir: profileDir}}
	if scope := CaptureScope(profileID); scope != "" {
		searches = append(searches, &search{
			params: map[string]any{"query": query, "store": "project", "scope": scope, "surfaces": []string{"chunks"}},
			dir:    profiledir.MonomindDir(nil, profileID),
		})
	}

	var wg sync.WaitGroup
	for _, s := range searches {
		wg.Add(1)
		go func(s *search) {
			defer wg.Done()
			s.results, s.err = runKnowledgeSearch(ctx, bin, s.dir, s.params)
		}(s)
	}
	wg.Wait()

	results := make([]KnowledgeResult, 0)
	var firstErr error
	failed := 0
	for _, s := range searches {
		if s.err != nil {
			failed++
			if firstErr == nil {
				firstErr = s.err
			}
			continue
		}
		results = append(results, s.results...)
	}
	if failed == len(searches) {
		return nil, fmt.Errorf("monomind.SearchKnowledge: %w", firstErr)
	}
	sort.SliceStable(results, func(i, j int) bool { return results[i].Score > results[j].Score })
	return results, nil
}

// runKnowledgeSearch runs one knowledge_search call with MONOMIND_CWD set
// to monomindDir and returns its excerpt-kind results.
func runKnowledgeSearch(ctx context.Context, bin, monomindDir string, params map[string]any) ([]KnowledgeResult, error) {
	raw, err := json.Marshal(params)
	if err != nil {
		return nil, fmt.Errorf("marshal params: %w", err)
	}

	cctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	cmd := exec.CommandContext(cctx, bin, "mcp", "exec", "-t", "knowledge_search", "-p", string(raw), "--format", "json")
	cmd.Env = PinEnv(append(FilteredEnviron(), "MONOMIND_CWD="+monomindDir), bin)
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("knowledge_search: %w", err)
	}

	jsonStr, err := extractJSONObject(string(out))
	if err != nil {
		return nil, err
	}
	var envelope cliEnvelope
	if err := json.Unmarshal([]byte(jsonStr), &envelope); err != nil {
		return nil, fmt.Errorf("decoding CLI envelope: %w", err)
	}
	if len(envelope.Result.Content) == 0 {
		return nil, fmt.Errorf("empty response content")
	}

	var payload knowledgeSearchPayload
	if err := json.Unmarshal([]byte(envelope.Result.Content[0].Text), &payload); err != nil {
		return nil, fmt.Errorf("decoding tool payload: %w", err)
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

// CaptureCompanionsVersion is the first monomind that ingests a capture's
// companion documents (transcript.md, summary.md) as documents of their
// own. Earlier releases file them under the page's URL as a new version
// of it, superseding readable.md, and refuse to ingest any capture whose
// URL has a query string (every YouTube video): "all chunk stores failed".
const CaptureCompanionsVersion = "2.18.3"

// CapKnowledgeProfileCaptures is advertised by monomind releases after
// 2.18.3 that ingest browser captures fully (see CaptureCompanionsVersion).
// Either it or the version is enough.
const CapKnowledgeProfileCaptures = "knowledge-profile-captures"

// SupportsCaptureCompanions reports whether the installed monomind can take
// a capture's companion documents. False when monomind is missing or its
// version cannot be read.
func SupportsCaptureCompanions(ctx context.Context) bool {
	set, err := Capabilities(ctx)
	if err != nil || set == nil {
		return false
	}
	return CaptureIndexingSupported(set.Version, set.Has(CapKnowledgeProfileCaptures))
}

// CaptureIndexingSupported reports whether a monomind can index every
// browser capture: it advertises CapKnowledgeProfileCaptures, or its
// version is CaptureCompanionsVersion or later (2.18.3 has the fix but not
// the capability).
func CaptureIndexingSupported(version string, hasCapability bool) bool {
	return hasCapability || versionAtLeast(version, CaptureCompanionsVersion)
}
