// cmd/monoagentcli/profile_documents.go
package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"

	"github.com/monoes/mono-agent/internal/monomind"
	"github.com/monoes/mono-agent/internal/vault"

	"github.com/spf13/cobra"
)

// indexDocument runs knowledge_ingest for path and records the outcome via
// SetDocumentIndexed, stamping a real (mtime, size) staleness baseline on
// success. Shared by upload-document (index immediately after copying in)
// and the standalone `documents index` command (on-demand re-index).
// os.Stat failures are non-fatal here (best-effort baseline) since a
// missing baseline just means Stale can never trigger for this success --
// strictly safer than failing the whole ingest over a stat error. Returns
// setErr separately (rather than swallowing it) so each caller can decide
// how to surface a record-keeping failure distinctly from an indexing
// failure.
func indexDocument(ctx context.Context, db *sql.DB, profileID, id, path string) (indexed bool, indexErrMsg string, setErr error) {
	indexed = true
	if ingestErr := monomind.IngestDocument(ctx, db, profileID, path); ingestErr != nil {
		indexed = false
		indexErrMsg = ingestErr.Error()
	}
	var mtime, size int64
	if indexed {
		if fi, statErr := os.Stat(path); statErr == nil {
			mtime = fi.ModTime().UnixNano()
			size = fi.Size()
		}
	}
	setErr = vault.SetDocumentIndexed(ctx, db, profileID, id, indexed, indexErrMsg, mtime, size)
	return indexed, indexErrMsg, setErr
}

func newProfileUploadDocumentCmd(cfg *globalConfig) *cobra.Command {
	var source string
	cmd := &cobra.Command{
		Use:     "upload-document <path>",
		Short:   "Upload a profile document and index it for chat search",
		Args:    cobra.ExactArgs(1),
		Example: `  monoagentcli profile upload-document ~/resume.pdf`,
		RunE: func(cmd *cobra.Command, args []string) error {
			db, err := initDB(cfg)
			if err != nil {
				return fmt.Errorf("initializing database: %w", err)
			}
			defer db.DB.Close()

			ctx := vault.ContextWithProfileID(cmd.Context(), cfg.ProfileID)
			id, err := vault.RegisterDocument(ctx, db.DB, args[0], source)
			if err != nil {
				return fmt.Errorf("uploading document: %w", err)
			}

			docs, err := vault.ListDocuments(ctx, db.DB, cfg.ProfileID)
			if err != nil {
				return fmt.Errorf("looking up uploaded document: %w", err)
			}
			var storedPath string
			for _, d := range docs {
				if d.ID == id {
					storedPath = d.Path
				}
			}

			indexed, indexErrMsg, setErr := indexDocument(cmd.Context(), db.DB, cfg.ProfileID, id, storedPath)
			if !indexed {
				fmt.Fprintf(cmd.ErrOrStderr(), "warning: document uploaded but indexing failed: %s\n", indexErrMsg)
			}
			if setErr != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "warning: could not record indexing status: %v\n", setErr)
			}

			if cfg.JSONOutput {
				enc := json.NewEncoder(cmd.OutOrStdout())
				enc.SetIndent("", "  ")
				out := map[string]interface{}{"id": id, "indexed": indexed}
				if indexErrMsg != "" {
					out["index_error"] = indexErrMsg
				}
				return enc.Encode(out)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Uploaded %q as %s.\n", args[0], id)
			if indexed {
				fmt.Fprintln(cmd.OutOrStdout(), "Indexed for knowledge search.")
			} else {
				fmt.Fprintf(cmd.OutOrStdout(), "Not indexed — %s\n", indexErrMsg)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&source, "source", "upload", "Where this document came from")
	return cmd
}

func newProfileDocumentsCmd(cfg *globalConfig) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "documents",
		Short: "Manage uploaded profile documents",
	}
	cmd.AddCommand(newProfileDocumentsListCmd(cfg), newProfileDocumentsRmCmd(cfg), newProfileDocumentsIndexCmd(cfg))
	return cmd
}

func newProfileDocumentsListCmd(cfg *globalConfig) *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List uploaded profile documents",
		RunE: func(cmd *cobra.Command, args []string) error {
			db, err := initDB(cfg)
			if err != nil {
				return fmt.Errorf("initializing database: %w", err)
			}
			defer db.DB.Close()

			docs, err := vault.ListDocuments(cmd.Context(), db.DB, cfg.ProfileID)
			if err != nil {
				return fmt.Errorf("listing documents: %w", err)
			}
			if cfg.JSONOutput {
				enc := json.NewEncoder(cmd.OutOrStdout())
				enc.SetIndent("", "  ")
				return enc.Encode(docs)
			}
			if len(docs) == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), "No documents uploaded.")
				return nil
			}
			table := newPlainTable(cmd.OutOrStdout(), []string{"ID", "Filename", "Source", "Uploaded"}, nil)
			for _, d := range docs {
				table.Append([]string{d.ID, truncateStr(d.Filename, 50), d.Source, d.CreatedAt})
			}
			table.Render()
			return nil
		},
	}
}

func newProfileDocumentsRmCmd(cfg *globalConfig) *cobra.Command {
	return &cobra.Command{
		Use:   "rm <id>",
		Short: "Delete an uploaded profile document",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			db, err := initDB(cfg)
			if err != nil {
				return fmt.Errorf("initializing database: %w", err)
			}
			defer db.DB.Close()

			if err := vault.DeleteDocument(cmd.Context(), db.DB, cfg.ProfileID, args[0]); err != nil {
				return errNotFound("%v", err)
			}
			if cfg.JSONOutput {
				enc := json.NewEncoder(cmd.OutOrStdout())
				enc.SetIndent("", "  ")
				return enc.Encode(map[string]string{"id": args[0]})
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Deleted %q.\n", args[0])
			return nil
		},
	}
}

// newProfileDocumentsIndexCmd is the on-demand indexing action for a
// document the GUI/CLI already knows about (whether uploaded or
// discovered by a filesystem scan) -- the "Index" button for a Not
// indexed/Stale row. Output shape mirrors upload-document's (a lowercase
// JSON map, not documents-list's PascalCase struct encoding): behaviorally
// this is upload-document's sibling (both attempt one ingest and report
// pass/fail), not list's.
func newProfileDocumentsIndexCmd(cfg *globalConfig) *cobra.Command {
	return &cobra.Command{
		Use:     "index <id>",
		Short:   "Index (or re-index) a profile document for knowledge search",
		Args:    cobra.ExactArgs(1),
		Example: `  monoagentcli profile documents index doc-003`,
		RunE: func(cmd *cobra.Command, args []string) error {
			db, err := initDB(cfg)
			if err != nil {
				return fmt.Errorf("initializing database: %w", err)
			}
			defer db.DB.Close()

			docs, err := vault.ListDocuments(cmd.Context(), db.DB, cfg.ProfileID)
			if err != nil {
				return fmt.Errorf("looking up document: %w", err)
			}
			var path string
			found := false
			for _, d := range docs {
				if d.ID == args[0] {
					path = d.Path
					found = true
				}
			}
			if !found {
				return errNotFound("document %q not found", args[0])
			}

			indexed, indexErrMsg, setErr := indexDocument(cmd.Context(), db.DB, cfg.ProfileID, args[0], path)
			if setErr != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "warning: could not record indexing status: %v\n", setErr)
			}

			if cfg.JSONOutput {
				enc := json.NewEncoder(cmd.OutOrStdout())
				enc.SetIndent("", "  ")
				out := map[string]interface{}{"id": args[0], "indexed": indexed}
				if indexErrMsg != "" {
					out["index_error"] = indexErrMsg
				}
				return enc.Encode(out)
			}
			if indexed {
				fmt.Fprintln(cmd.OutOrStdout(), "Indexed for knowledge search.")
			} else {
				fmt.Fprintf(cmd.OutOrStdout(), "Not indexed — %s\n", indexErrMsg)
			}
			return nil
		},
	}
}

func newProfileSearchKnowledgeCmd(cfg *globalConfig) *cobra.Command {
	return &cobra.Command{
		Use:     "search-knowledge <query>",
		Short:   "Search uploaded profile documents (for testing — chat uses this automatically)",
		Args:    cobra.ExactArgs(1),
		Example: `  monoagentcli profile search-knowledge "backend frameworks"`,
		RunE: func(cmd *cobra.Command, args []string) error {
			db, err := initDB(cfg)
			if err != nil {
				return fmt.Errorf("initializing database: %w", err)
			}
			defer db.DB.Close()

			results, err := monomind.SearchKnowledge(cmd.Context(), db.DB, cfg.ProfileID, args[0])
			if err != nil {
				return fmt.Errorf("searching profile documents: %w", err)
			}
			if cfg.JSONOutput {
				enc := json.NewEncoder(cmd.OutOrStdout())
				enc.SetIndent("", "  ")
				return enc.Encode(results)
			}
			if len(results) == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), "No matching content found.")
				return nil
			}
			for _, r := range results {
				fmt.Fprintf(cmd.OutOrStdout(), "[%.2f] %s: %s\n", r.Score, r.Path, r.Excerpt)
			}
			return nil
		},
	}
}
