package main

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/monoes/mono-agent/internal/workflow"
)

// Import statuses (`workflow import --json` "status").
const (
	importCreated   = "created"
	importUpdated   = "updated"
	importUnchanged = "unchanged"
)

// importIndexEntry remembers one import so a repeated import of the same
// file updates that workflow instead of creating a duplicate.
type importIndexEntry struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Source string `json:"source"` // absolute --file path, or "stdin"
	Hash   string `json:"hash"`   // workflowContentHash at import time
}

func importIndexPath() string { return expandPath("~/.monoagent/workflow-imports.json") }

func loadImportIndex() []importIndexEntry {
	var idx []importIndexEntry
	if b, err := os.ReadFile(importIndexPath()); err == nil {
		_ = json.Unmarshal(b, &idx)
	}
	return idx
}

// recordImport upserts the entry for id (one entry per workflow).
func recordImport(e importIndexEntry) {
	idx := loadImportIndex()
	out := idx[:0]
	for _, old := range idx {
		if old.ID != e.ID {
			out = append(out, old)
		}
	}
	out = append(out, e)
	b, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return
	}
	path := importIndexPath()
	if os.MkdirAll(filepath.Dir(path), 0o700) != nil {
		return
	}
	tmp := path + ".tmp"
	if os.WriteFile(tmp, b, 0o600) == nil {
		_ = os.Rename(tmp, path)
	}
}

func workflowImportSource(inputFile string) string {
	if inputFile == "" {
		return "stdin"
	}
	if abs, err := filepath.Abs(inputFile); err == nil {
		return abs
	}
	return inputFile
}

// workflowContentHash hashes what a workflow does, independent of ids and
// timestamps (ids may have been remapped on a previous import): name,
// description, nodes (type, name, config, position, disabled) and the
// connections between them.
func workflowContentHash(wf *workflow.Workflow) string {
	type cnode struct {
		Type     string         `json:"t"`
		Name     string         `json:"n"`
		Config   map[string]any `json:"c,omitempty"`
		X        float64        `json:"x"`
		Y        float64        `json:"y"`
		Disabled bool           `json:"d,omitempty"`
	}
	keys := map[string]string{} // node id → canonical node JSON
	nodes := make([]string, 0, len(wf.Nodes))
	for _, n := range wf.Nodes {
		cfg := n.Config
		if cfg == nil && n.ConfigRaw != "" {
			_ = json.Unmarshal([]byte(n.ConfigRaw), &cfg)
		}
		b, _ := json.Marshal(cnode{n.Type, n.Name, cfg, n.PositionX, n.PositionY, n.Disabled})
		keys[n.ID] = string(b)
		nodes = append(nodes, string(b))
	}
	sort.Strings(nodes)
	conns := make([]string, 0, len(wf.Connections))
	for _, c := range wf.Connections {
		conns = append(conns, strings.Join([]string{keys[c.SourceNodeID], c.SourceHandle, keys[c.TargetNodeID], c.TargetHandle}, "\x00"))
	}
	sort.Strings(conns)
	h := sha256.New()
	_ = json.NewEncoder(h).Encode([]any{wf.Name, wf.Description, nodes, conns})
	return hex.EncodeToString(h.Sum(nil))
}

// ownedWorkflow returns the local workflow id if it exists and belongs to
// profileID (a workflow with no SQLite row, or no profile, counts as ours).
func ownedWorkflow(ctx context.Context, store *workflow.HybridWorkflowStore, db *sql.DB, profileID, id string) *workflow.Workflow {
	if id == "" {
		return nil
	}
	wf, err := store.GetWorkflow(ctx, id)
	if err != nil || wf == nil {
		return nil
	}
	var owner string
	_ = db.QueryRowContext(ctx, `SELECT profile_id FROM workflows WHERE id = ?`, id).Scan(&owner)
	if owner != "" && profileID != "" && owner != profileID {
		return nil
	}
	return wf
}

// findImportTarget finds the local workflow an import should update: the
// file's own id, else a workflow imported earlier from the same source
// with the same content or the same name.
func findImportTarget(ctx context.Context, store *workflow.HybridWorkflowStore, db *sql.DB, profileID string,
	wf *workflow.Workflow, source, hash string) *workflow.Workflow {
	if existing := ownedWorkflow(ctx, store, db, profileID, wf.ID); existing != nil {
		return existing
	}
	idx := loadImportIndex()
	for _, match := range []func(importIndexEntry) bool{
		func(e importIndexEntry) bool { return e.Source == source && e.Hash == hash },
		func(e importIndexEntry) bool { return e.Source == source && e.Name == wf.Name },
	} {
		for i := len(idx) - 1; i >= 0; i-- {
			if match(idx[i]) {
				if existing := ownedWorkflow(ctx, store, db, profileID, idx[i].ID); existing != nil {
					return existing
				}
			}
		}
	}
	return nil
}

// missingBundleHint lists bundled packages that were not installed and the
// command that installs them (a re-import with --yes; imports are
// idempotent, so the workflow itself is left unchanged).
func missingBundleHint(items []bundleImportItem, inputFile string) ([]string, string) {
	var missing []string
	for _, it := range items {
		if it.Status == "missing" {
			missing = append(missing, it.ID)
		}
	}
	if len(missing) == 0 {
		return nil, ""
	}
	if inputFile == "" {
		return missing, "monoagentcli workflow import --yes < <workflow file>"
	}
	return missing, fmt.Sprintf("monoagentcli workflow import --file %s --yes", shellQuoteArg(workflowImportSource(inputFile)))
}

func shellQuoteArg(s string) string {
	if s != "" && strings.IndexFunc(s, func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("/._-+:@", r))
	}) < 0 {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// printWorkflowImport handles the bundled automations and prints the
// import result (JSON or human), including the status and, when bundled
// packages are missing, which ones and the exact command installing them.
func printWorkflowImport(cfg *globalConfig, cmd *cobra.Command, wf *workflow.Workflow, status string,
	remapped, remappedConns map[string]string, raw []byte, yes bool, inputFile string) error {
	// Bundled automations (workflow export --bundle-automations):
	// report present/missing ones, install missing with --yes or
	// after a prompt (only when stdin is free, i.e. --file).
	bundled := handleBundledAutomations(raw, bundleImportOptions{
		yes:         yes,
		interactive: !cfg.JSONOutput && inputFile != "" && stdinIsTerminal(),
		in:          cmd.InOrStdin(),
		out:         os.Stderr,
	})
	missing, installCmd := missingBundleHint(bundled, inputFile)

	if cfg.JSONOutput {
		out := map[string]interface{}{"id": wf.ID, "name": wf.Name, "status": status}
		if bundled != nil {
			out["automations"] = bundled
		}
		if len(missing) > 0 {
			out["missingAutomations"] = missing
			out["installCommand"] = installCmd
		}
		if len(remapped) > 0 {
			out["remapped_node_ids"] = remapped
		}
		if len(remappedConns) > 0 {
			out["remapped_connection_ids"] = remappedConns
		}
		return json.NewEncoder(os.Stdout).Encode(out)
	}
	switch status {
	case importUnchanged:
		fmt.Fprintf(os.Stdout, "Workflow %q is already imported and unchanged (id: %s)\n", wf.Name, wf.ID)
	case importUpdated:
		fmt.Fprintf(os.Stdout, "Updated workflow %q in place (id: %s, %d nodes, %d connections)\n",
			wf.Name, wf.ID, len(wf.Nodes), len(wf.Connections))
	default:
		fmt.Fprintf(os.Stdout, "Imported workflow %q as id: %s  (%d nodes, %d connections)\n",
			wf.Name, wf.ID, len(wf.Nodes), len(wf.Connections))
	}
	printRemapped := func(label string, m map[string]string) {
		if len(m) == 0 {
			return
		}
		parts := make([]string, 0, len(m))
		for old, newID := range m {
			parts = append(parts, old+" → "+newID)
		}
		sort.Strings(parts)
		fmt.Fprintf(os.Stdout, "Remapped %s (already used by another workflow): %s\n", label, strings.Join(parts, ", "))
	}
	printRemapped("node ids", remapped)
	printRemapped("connection ids", remappedConns)
	printBundleImport(os.Stdout, bundled)
	if len(missing) > 0 {
		fmt.Fprintf(os.Stdout, "The workflow was imported, but it needs %d automation(s) that are not installed: %s\nInstall them with: %s\n",
			len(missing), strings.Join(missing, ", "), installCmd)
	}
	return nil
}
