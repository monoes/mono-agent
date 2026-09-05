package main

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/monoes/mono-agent/internal/documents"

	"github.com/spf13/cobra"
)

// newDocumentsCmd returns the `documents` command group: renders
// structured CV/cover-letter/tender-proposal data into HTML+PDF and saves
// both into the vault. See
// docs/mastermind/specs/2026-09-05-document-generation-design.md.
func newDocumentsCmd(cfg *globalConfig) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "documents",
		Short: "Generate CV, cover letter, and tender proposal documents",
	}
	cmd.AddCommand(newDocumentsRenderCmd(cfg))
	return cmd
}

func newDocumentsRenderCmd(cfg *globalConfig) *cobra.Command {
	var docType, dataFile, applicationID string
	cmd := &cobra.Command{
		Use:   "render",
		Short: "Render a document from structured JSON data",
		Example: `  monoagentcli documents render --type cv --data-file cv.json
  monoagentcli documents render --type cover_letter --data-file letter.json --application-id abc123`,
		RunE: func(cmd *cobra.Command, args []string) error {
			raw, err := os.ReadFile(dataFile)
			if err != nil {
				return fmt.Errorf("reading data file: %w", err)
			}

			var htmlDocID, pdfDocID string
			db, err := initDB(cfg)
			if err != nil {
				return fmt.Errorf("initializing database: %w", err)
			}
			defer db.DB.Close()

			switch documents.DocType(docType) {
			case documents.DocTypeCV:
				var data documents.CVData
				if err := json.Unmarshal(raw, &data); err != nil {
					return errInvalidInput("parsing --data-file: %v", err)
				}
				htmlDocID, pdfDocID, err = documents.GenerateDocument(cmd.Context(), db.DB, cfg.ProfileID, applicationID, documents.DocTypeCV, data)
			case documents.DocTypeCoverLetter:
				var data documents.CoverLetterData
				if err := json.Unmarshal(raw, &data); err != nil {
					return errInvalidInput("parsing --data-file: %v", err)
				}
				htmlDocID, pdfDocID, err = documents.GenerateDocument(cmd.Context(), db.DB, cfg.ProfileID, applicationID, documents.DocTypeCoverLetter, data)
			case documents.DocTypeTenderProposal:
				var data documents.TenderProposalData
				if err := json.Unmarshal(raw, &data); err != nil {
					return errInvalidInput("parsing --data-file: %v", err)
				}
				htmlDocID, pdfDocID, err = documents.GenerateDocument(cmd.Context(), db.DB, cfg.ProfileID, applicationID, documents.DocTypeTenderProposal, data)
			default:
				return errInvalidInput("--type must be one of cv, cover_letter, tender_proposal, got %q", docType)
			}
			if err != nil {
				return fmt.Errorf("generating document: %w", err)
			}

			if cfg.JSONOutput {
				enc := json.NewEncoder(cmd.OutOrStdout())
				enc.SetIndent("", "  ")
				return enc.Encode(map[string]string{"html_document_id": htmlDocID, "pdf_document_id": pdfDocID})
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Generated %s: html=%s pdf=%s\n", docType, htmlDocID, pdfDocID)
			return nil
		},
	}
	cmd.Flags().StringVar(&docType, "type", "", "Document type: cv, cover_letter, or tender_proposal (required)")
	cmd.Flags().StringVar(&dataFile, "data-file", "", "Path to a JSON file matching the chosen type's fields (required)")
	cmd.Flags().StringVar(&applicationID, "application-id", "", "Optional job/tender application to link this document to")
	cmd.MarkFlagRequired("type")
	cmd.MarkFlagRequired("data-file")
	return cmd
}
