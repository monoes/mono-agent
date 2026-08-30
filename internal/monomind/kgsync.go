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

// KGNode is one entity to ingest into a profile's knowledge graph.
type KGNode struct {
	Name        string
	Type        string
	Description string
}

// KGEdge is one relation between two KGNode.Name values to ingest into a
// profile's knowledge graph.
type KGEdge struct {
	Source      string
	Target      string
	Relation    string
	Description string
}

// kgIngestParams mirrors the `memory_kg_ingest` MCP tool's `-p` JSON shape.
type kgIngestParams struct {
	Nodes []kgIngestNode `json:"nodes"`
	Edges []kgIngestEdge `json:"edges"`

	OriginRef string `json:"originRef"`
	DBPath    string `json:"dbPath"`
}

type kgIngestNode struct {
	Name        string `json:"name"`
	Type        string `json:"type,omitempty"`
	Description string `json:"description,omitempty"`
}

type kgIngestEdge struct {
	Source      string `json:"source"`
	Target      string `json:"target"`
	Relation    string `json:"relation"`
	Description string `json:"description,omitempty"`
}

// SyncToKnowledgeGraph best-effort ingests nodes/edges into profileID's
// knowledge-graph database via `monomind mcp exec -t memory_kg_ingest`. It is
// safe to call from a fire-and-forget goroutine after a real DB write
// succeeds: it never panics on nil/empty input (a no-op when there is
// nothing to sync), and returns an error only on genuine exec failure (binary
// missing, non-zero exit, timeout) — never on KG-side outcomes, since ingest
// is idempotent.
func SyncToKnowledgeGraph(ctx context.Context, db *sql.DB, profileID string, nodes []KGNode, edges []KGEdge, originRef string) error {
	if len(nodes) == 0 && len(edges) == 0 {
		return nil
	}

	bin, err := Find()
	if err != nil {
		return err
	}

	profileDir := profiledir.MonomindDir(db, profileID)
	// Must exist on disk BEFORE the subprocess runs: monomind resolves
	// MONOMIND_CWD's project root by walking UP from it looking for an
	// existing marker — if profileDir doesn't exist yet, that walk skips
	// past it entirely and lands on some unrelated shared ancestor,
	// silently merging every not-yet-created profile's knowledge graph
	// into the same one (confirmed directly: two distinct, never-created
	// profile dirs returned each other's entries; creating the dirs first
	// fixed it).
	if err := os.MkdirAll(profileDir, 0700); err != nil {
		return fmt.Errorf("kgsync: creating profile monomind dir: %w", err)
	}
	params := kgIngestParams{
		OriginRef: originRef,
		DBPath:    profileDir,
	}
	for _, n := range nodes {
		params.Nodes = append(params.Nodes, kgIngestNode{Name: n.Name, Type: n.Type, Description: n.Description})
	}
	for _, e := range edges {
		params.Edges = append(params.Edges, kgIngestEdge{Source: e.Source, Target: e.Target, Relation: e.Relation, Description: e.Description})
	}

	paramsJSON, err := json.Marshal(params)
	if err != nil {
		return fmt.Errorf("kgsync: marshal params: %w", err)
	}

	cctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()

	cmd := exec.CommandContext(cctx, bin, "mcp", "exec", "-t", "memory_kg_ingest", "-p", string(paramsJSON))
	// The `dbPath` JSON param alone is not enough: monomind's getDbPath()
	// runs a path-traversal guard that only honors a custom dbPath when it
	// falls inside the resolved project root (MONOMIND_CWD, or process.cwd()
	// if unset), the project's own home-data dir, or the global brain —
	// otherwise it silently ignores dbPath and falls back to whatever
	// project this subprocess's cwd resolves to. Without this env var, every
	// profile's writes were silently landing in the SAME shared default
	// location instead of being isolated per profile (confirmed directly:
	// two distinct dbPaths returned identical search results until this env
	// var was added). Setting MONOMIND_CWD to the same directory satisfies
	// that guard so dbPath is actually honored.
	cmd.Env = append(os.Environ(), "MONOMIND_CWD="+profileDir)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("kgsync: memory_kg_ingest: %w", err)
	}
	return nil
}
