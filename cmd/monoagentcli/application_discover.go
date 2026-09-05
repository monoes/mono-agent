// cmd/monoagentcli/application_discover.go
package main

import (
	"encoding/json"
	"fmt"

	"github.com/monoes/mono-agent/internal/applications"
	"github.com/monoes/mono-agent/internal/discovery"
	"github.com/monoes/mono-agent/internal/discoveryregistry"

	"github.com/spf13/cobra"
)

// newApplicationDiscoverCmd returns the `application discover` command:
// searches one discovery.Source and imports non-duplicate results as new
// pending job applications.
func newApplicationDiscoverCmd(cfg *globalConfig) *cobra.Command {
	var keywords, location, source string
	var limit int
	cmd := &cobra.Command{
		Use:   "discover",
		Short: "Search a job board and import new postings as pending applications",
		Example: `  monoagentcli application discover --keywords "backend engineer" --location Berlin
  monoagentcli application discover --keywords "platform engineer" --limit 50`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if keywords == "" {
				return errInvalidInput("--keywords is required")
			}
			src, ok := discoveryregistry.Get(source)
			if !ok {
				return errInvalidInput("unknown --source %q (available: %v)", source, discoveryregistry.Names())
			}

			db, err := initDB(cfg)
			if err != nil {
				return fmt.Errorf("initializing database: %w", err)
			}
			defer db.DB.Close()
			store := applications.NewStore(db.DB)

			created, skipped, failed, searchErr := discovery.Search(cmd.Context(), src, store, cfg.ProfileID, discovery.SearchQuery{
				Keywords: keywords, Location: location, Limit: limit,
			})
			if searchErr != nil && len(created) == 0 {
				return fmt.Errorf("discovering jobs: %w", searchErr)
			}

			if cfg.JSONOutput {
				type createdApp struct {
					ID      string `json:"id"`
					Title   string `json:"title"`
					Company string `json:"company"`
					URL     string `json:"url"`
				}
				out := make([]createdApp, 0, len(created))
				for _, app := range created {
					out = append(out, createdApp{ID: app.ID, Title: app.Job.Title, Company: app.Job.Company, URL: app.Job.URL})
				}
				enc := json.NewEncoder(cmd.OutOrStdout())
				enc.SetIndent("", "  ")
				return enc.Encode(map[string]interface{}{
					"imported": len(created), "skipped": skipped, "failed": failed, "applications": out,
				})
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Imported %d new job(s), skipped %d duplicate(s).\n", len(created), skipped)
			if failed > 0 {
				fmt.Fprintf(cmd.OutOrStdout(), "%d result(s) failed to save.\n", failed)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&keywords, "keywords", "", "Job search keywords (required)")
	cmd.Flags().StringVar(&location, "location", "", "Job location filter")
	cmd.Flags().StringVar(&source, "source", "linkedin", "Job board to search")
	cmd.Flags().IntVar(&limit, "limit", 25, "Maximum results to import (capped at 100)")
	return cmd
}
