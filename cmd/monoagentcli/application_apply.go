// cmd/monoagentcli/application_apply.go
package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/monoes/mono-agent/internal/apply"
	"github.com/monoes/mono-agent/internal/applications"
	"github.com/monoes/mono-agent/internal/documents"

	"github.com/spf13/cobra"
)

// confirmPromptFunc asks the user a y/N question on stdin/stdout, real by
// default; swappable for tests. Returns true only on an explicit "y"/"yes"
// (case-insensitive) — any other input, including a read error or EOF,
// is treated as "no" (the safe default for an action that opens a real
// browser and starts an application flow).
var confirmPromptFunc = func(prompt string) bool {
	fmt.Fprintf(os.Stderr, "%s [y/N]: ", prompt)
	reader := bufio.NewReader(os.Stdin)
	line, err := reader.ReadString('\n')
	if err != nil {
		return false
	}
	answer := strings.ToLower(strings.TrimSpace(line))
	return answer == "y" || answer == "yes"
}

func newApplicationApplyCmd(cfg *globalConfig) *cobra.Command {
	var mode, cvDataFile, letterDataFile string
	cmd := &cobra.Command{
		Use:   "apply <id>",
		Short: "Prepare an application's documents and open the posting in a browser",
		Long: "Generates (or reuses) the CV and cover letter for this application, then opens the job\n" +
			"posting in a real browser window for you to complete and submit yourself — this command\n" +
			"never fills in or submits the form on your behalf. Use `application send` afterward to\n" +
			"record that you sent it.",
		Args:    cobra.ExactArgs(1),
		Example: `  monoagentcli application apply 1c2e... --cv-data-file cv.json --cover-letter-data-file letter.json`,
		RunE: func(cmd *cobra.Command, args []string) error {
			db, err := initDB(cfg)
			if err != nil {
				return fmt.Errorf("initializing database: %w", err)
			}
			defer db.DB.Close()

			store := applications.NewStore(db.DB)
			app, err := store.Get(cmd.Context(), cfg.ProfileID, args[0])
			if err != nil {
				return fmt.Errorf("getting application: %w", err)
			}
			if app.Kind != applications.KindJob {
				return errInvalidInput("application apply currently supports job-kind applications only, got %q", app.Kind)
			}

			effectiveMode := mode
			if effectiveMode == "" {
				if cfg.JSONOutput {
					effectiveMode = "auto"
				} else {
					effectiveMode = "confirm"
				}
			}
			if effectiveMode != "auto" && effectiveMode != "confirm" {
				return errInvalidInput("--mode must be \"auto\" or \"confirm\", got %q", effectiveMode)
			}

			if effectiveMode == "confirm" {
				prompt := fmt.Sprintf("About to prepare and open %s — %s (%s). Continue?", app.Job.Company, app.Job.Title, app.Job.URL)
				if !confirmPromptFunc(prompt) {
					fmt.Fprintln(cmd.OutOrStdout(), "Cancelled.")
					return nil
				}
			}

			var cvData documents.CVData
			if cvDataFile != "" {
				raw, err := os.ReadFile(cvDataFile)
				if err != nil {
					return fmt.Errorf("reading --cv-data-file: %w", err)
				}
				if err := json.Unmarshal(raw, &cvData); err != nil {
					return errInvalidInput("parsing --cv-data-file: %v", err)
				}
			}
			var letterData documents.CoverLetterData
			if letterDataFile != "" {
				raw, err := os.ReadFile(letterDataFile)
				if err != nil {
					return fmt.Errorf("reading --cover-letter-data-file: %w", err)
				}
				if err := json.Unmarshal(raw, &letterData); err != nil {
					return errInvalidInput("parsing --cover-letter-data-file: %v", err)
				}
			}

			cvHTML, cvPDF, letterHTML, letterPDF, err := apply.Prepare(cmd.Context(), db.DB, cfg.ProfileID, app.ID, cvData, letterData)
			if err != nil {
				return fmt.Errorf("preparing documents: %w", err)
			}

			if err := apply.OpenForApplication(cmd.Context(), app.Job.URL); err != nil {
				return fmt.Errorf("opening browser: %w", err)
			}

			if cfg.JSONOutput {
				enc := json.NewEncoder(cmd.OutOrStdout())
				enc.SetIndent("", "  ")
				return enc.Encode(map[string]string{
					"cv_html_document_id": cvHTML, "cv_pdf_document_id": cvPDF,
					"cover_letter_html_document_id": letterHTML, "cover_letter_pdf_document_id": letterPDF,
				})
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Opened %s. Documents ready — cv: %s / %s, cover letter: %s / %s\nRun `monoagentcli application send %s` once you've submitted it.\n",
				app.Job.URL, cvHTML, cvPDF, letterHTML, letterPDF, app.ID)
			return nil
		},
	}
	cmd.Flags().StringVar(&mode, "mode", "", "confirm (default in text mode) or auto (default in --json mode)")
	cmd.Flags().StringVar(&cvDataFile, "cv-data-file", "", "Path to a JSON file matching documents.CVData's fields")
	cmd.Flags().StringVar(&letterDataFile, "cover-letter-data-file", "", "Path to a JSON file matching documents.CoverLetterData's fields")
	return cmd
}

func newApplicationSendCmd(cfg *globalConfig) *cobra.Command {
	var note string
	cmd := &cobra.Command{
		Use:   "send <id>",
		Short: "Record that you submitted this application yourself",
		Long:  "This is the only way an application's status becomes \"applied\" — mono-agent never submits a form on your behalf; this command is how you tell it you did.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			db, err := initDB(cfg)
			if err != nil {
				return fmt.Errorf("initializing database: %w", err)
			}
			defer db.DB.Close()

			store := applications.NewStore(db.DB)
			if err := store.SetStatus(cmd.Context(), cfg.ProfileID, args[0], applications.StatusApplied, applications.ActorUser, note); err != nil {
				return fmt.Errorf("recording send: %w", err)
			}
			if cfg.JSONOutput {
				enc := json.NewEncoder(cmd.OutOrStdout())
				enc.SetIndent("", "  ")
				return enc.Encode(map[string]string{"id": args[0], "status": "applied"})
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Recorded %s as applied.\n", args[0])
			return nil
		},
	}
	cmd.Flags().StringVar(&note, "note", "", "Optional note recorded in the transition ledger")
	return cmd
}
