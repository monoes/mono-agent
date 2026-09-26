// cmd/monoagentcli/profile_documents_sync.go
package main

import (
	"encoding/json"
	"fmt"

	"github.com/monoes/mono-agent/internal/docscan"
	"github.com/monoes/mono-agent/internal/profiledir"
	"github.com/monoes/mono-agent/internal/vault"

	"github.com/spf13/cobra"
)

// folderSyncResult is the --json shape of `profile documents sync` and
// `image sync`: what one reconcile of the profile folder changed.
type folderSyncResult struct {
	ProfileID string   `json:"profile_id"`
	Root      string   `json:"root"`
	Scanned   int      `json:"scanned"`
	Added     int      `json:"added"`
	Updated   int      `json:"updated"`
	Removed   int      `json:"removed"`
	Changed   bool     `json:"changed"`
	Errors    []string `json:"errors"`
}

func newFolderSyncResult(profileID, root string, scanned int, r vault.ReconcileResult) folderSyncResult {
	out := folderSyncResult{ProfileID: profileID, Root: root, Scanned: scanned,
		Added: r.Added, Updated: r.Updated, Removed: r.Removed, Changed: r.Changed(), Errors: []string{}}
	for _, e := range r.Errs {
		out.Errors = append(out.Errors, e.Error())
	}
	return out
}

// printFolderSync writes res as JSON or one summary line, with per-file
// errors as stderr warnings in text mode. Per-file errors never fail the
// command: the rest of the folder was still reconciled.
func printFolderSync(cmd *cobra.Command, cfg *globalConfig, noun string, res folderSyncResult) error {
	if cfg.JSONOutput {
		enc := json.NewEncoder(cmd.OutOrStdout())
		enc.SetIndent("", "  ")
		return enc.Encode(res)
	}
	for _, e := range res.Errors {
		fmt.Fprintf(cmd.ErrOrStderr(), "warning: %s\n", e)
	}
	fmt.Fprintf(cmd.OutOrStdout(), "Scanned %d %s in %s: %d added, %d updated, %d removed.\n",
		res.Scanned, noun, res.Root, res.Added, res.Updated, res.Removed)
	return nil
}

// newProfileDocumentsSyncCmd reconciles vault_documents with the documents
// on disk in the active profile's folder, once. The desktop app runs it
// when its folder watcher sees a change.
func newProfileDocumentsSyncCmd(cfg *globalConfig) *cobra.Command {
	return &cobra.Command{
		Use:     "sync",
		Short:   "Register, refresh and remove discovered documents to match the profile folder",
		Args:    cobra.NoArgs,
		Example: `  monoagentcli profile documents sync --json`,
		RunE: func(cmd *cobra.Command, args []string) error {
			db, err := initDB(cfg)
			if err != nil {
				return fmt.Errorf("initializing database: %w", err)
			}
			defer db.DB.Close()

			// Root unmodified: see docscan.Scan for why it must match
			// profiledir.Root byte-for-byte.
			root := profiledir.Root(db.DB, cfg.ProfileID)
			if root == "" {
				return errInvalidInput("invalid profile id %q", cfg.ProfileID)
			}
			files, err := docscan.Scan(root)
			if err != nil {
				return fmt.Errorf("scanning %s: %w", root, err)
			}
			found := make([]vault.DiscoveredFile, len(files))
			for i, f := range files {
				found[i] = vault.DiscoveredFile{Path: f.Path, Filename: f.Filename, SizeBytes: f.SizeBytes}
			}
			r := vault.SyncDiscoveredDocuments(cmd.Context(), db.DB, cfg.ProfileID, found)
			return printFolderSync(cmd, cfg, "documents", newFolderSyncResult(cfg.ProfileID, root, len(files), r))
		},
	}
}
