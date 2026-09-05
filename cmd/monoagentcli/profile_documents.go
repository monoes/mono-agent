// cmd/monoagentcli/profile_documents.go
package main

import (
	"encoding/json"
	"fmt"

	"github.com/monoes/mono-agent/internal/monomind"
	"github.com/monoes/mono-agent/internal/vault"

	"github.com/spf13/cobra"
)

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

			indexed := true
			indexErrMsg := ""
			if ingestErr := monomind.IngestDocument(cmd.Context(), db.DB, cfg.ProfileID, storedPath); ingestErr != nil {
				indexed = false
				indexErrMsg = ingestErr.Error()
				fmt.Fprintf(cmd.ErrOrStderr(), "warning: document uploaded but indexing failed: %v\n", ingestErr)
			}
			if setErr := vault.SetDocumentIndexed(ctx, db.DB, cfg.ProfileID, id, indexed, indexErrMsg); setErr != nil {
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
	cmd.AddCommand(newProfileDocumentsListCmd(cfg), newProfileDocumentsRmCmd(cfg))
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
				table.Append([]string{d.ID, d.Filename, d.Source, d.CreatedAt})
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
