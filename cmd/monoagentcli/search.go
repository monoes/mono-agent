package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/monoes/mono-agent/internal/storage"
	"github.com/spf13/cobra"
)

func newSearchCmd(cfg *globalConfig) *cobra.Command {
	var (
		keyword    string
		maxResults int
		timeout    time.Duration
	)

	cmd := &cobra.Command{
		Use:   "search <platform>",
		Short: "Quick keyword search on a platform",
		Long:  "Creates a KEYWORD_SEARCH action and immediately executes it. Results are saved to the database.",
		Example: `  monoagentcli search instagram --keyword "golang developer" --max 50
  monoagentcli search linkedin --keyword "startup founder" --max 100
  monoagentcli search x --keyword "AI engineer"`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			platform := strings.ToLower(args[0])

			db, err := initDB(cfg)
			if err != nil {
				return fmt.Errorf("initializing database: %w", err)
			}
			defer db.Close()

			runStart := time.Now().UTC()

			// Create a KEYWORD_SEARCH action.
			actionID := storage.NewID()
			act := &storage.Action{
				ID:        actionID,
				CreatedAt: time.Now().Unix(),
				Title:     fmt.Sprintf("Quick search: %s on %s", keyword, platform),
				Type:      "KEYWORD_SEARCH",
				State:     "PENDING",
				// Uppercase to match the GUI platform filter (see action.go).
				TargetPlatform: strings.ToUpper(platform),
				Keywords:       keyword,
			}

			if err := upsertAction(db, act, cfg.ProfileID); err != nil {
				return fmt.Errorf("creating search action: %w", err)
			}

			// Create a single target representing the search.
			targetID := storage.NewID()
			metadata, err := json.Marshal(map[string]interface{}{"keyword": keyword, "max_results": maxResults})
			if err != nil {
				return fmt.Errorf("encoding search metadata: %w", err)
			}
			_, err = db.DB.Exec(
				`INSERT INTO action_targets (id, action_id, platform, status, metadata)
				 VALUES (?, ?, ?, 'PENDING', ?)`,
				targetID, actionID, platform, string(metadata),
			)
			if err != nil {
				return fmt.Errorf("creating search target: %w", err)
			}

			fmt.Fprintf(os.Stderr, "Created search action %s for keyword %q on %s (max: %d)\n",
				actionID, keyword, platform, maxResults)

			// Execute immediately. In JSON mode, suppress executeAction's own
			// human/JSON output (its table goes to stderr when JSONOutput is
			// false) so stdout carries exactly one JSON document: the profiles
			// this search actually persisted.
			ctx := cmd.Context()
			execCfg := cfg
			if cfg.JSONOutput {
				clone := *cfg
				clone.JSONOutput = false
				execCfg = &clone
			}
			if err := executeAction(ctx, db, execCfg, act, timeout); err != nil {
				return err
			}
			if cfg.JSONOutput {
				return printSearchResultsJSON(cmd.Context(), db, cfg, actionID, strings.ToUpper(platform), runStart, maxResults)
			}
			return nil
		},
	}

	cmd.Flags().StringVar(&keyword, "keyword", "", "Search keyword (required)")
	cmd.Flags().IntVar(&maxResults, "max", 50, "Maximum number of results to collect")
	cmd.Flags().DurationVar(&timeout, "timeout", 10*time.Minute, "Maximum execution time")
	_ = cmd.MarkFlagRequired("keyword")

	return cmd
}

// printSearchResultsJSON emits the `search --json` document: the action's
// final state plus every profile row this search actually persisted (created
// or updated since the run started), scoped to the active profile and platform.
func printSearchResultsJSON(ctx context.Context, db *storage.Database, cfg *globalConfig, actionID, platform string, runStart time.Time, limit int) error {
	var state string
	if err := db.DB.QueryRowContext(ctx, `SELECT state FROM actions WHERE id = ?`, actionID).Scan(&state); err != nil {
		return fmt.Errorf("reading action state: %w", err)
	}

	profileID := cfg.ProfileID
	if profileID == "" {
		profileID = "default"
	}
	if limit <= 0 {
		limit = 50
	}
	rows, err := db.DB.QueryContext(ctx, `
		SELECT id, platform_username, platform, COALESCE(full_name,''), COALESCE(image_url,''),
		       COALESCE(website,''), content_count, COALESCE(follower_count,''), following_count,
		       COALESCE(introduction,''), is_verified, COALESCE(category,''), COALESCE(job_title,'')
		FROM people
		WHERE COALESCE(profile_id,'default') = ? AND UPPER(platform) = ? AND updated_at >= ?
		ORDER BY created_at DESC
		LIMIT ?`,
		profileID, platform, runStart, limit,
	)
	if err != nil {
		return fmt.Errorf("querying persisted profiles: %w", err)
	}
	defer rows.Close()

	type profileJSON struct {
		ID             string `json:"id"`
		Username       string `json:"platform_username"`
		Platform       string `json:"platform"`
		FullName       string `json:"full_name,omitempty"`
		ImageURL       string `json:"image_url,omitempty"`
		Website        string `json:"website,omitempty"`
		ContentCount   int    `json:"content_count"`
		FollowerCount  string `json:"follower_count,omitempty"`
		FollowingCount int    `json:"following_count"`
		Introduction   string `json:"introduction,omitempty"`
		IsVerified     bool   `json:"is_verified"`
		Category       string `json:"category,omitempty"`
		JobTitle       string `json:"job_title,omitempty"`
	}
	profiles := []profileJSON{}
	for rows.Next() {
		var p profileJSON
		if err := rows.Scan(
			&p.ID, &p.Username, &p.Platform, &p.FullName, &p.ImageURL,
			&p.Website, &p.ContentCount, &p.FollowerCount, &p.FollowingCount,
			&p.Introduction, &p.IsVerified, &p.Category, &p.JobTitle,
		); err != nil {
			return fmt.Errorf("scanning profile row: %w", err)
		}
		profiles = append(profiles, p)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterating profile rows: %w", err)
	}

	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(map[string]interface{}{
		"action_id":     actionID,
		"state":         state,
		"profiles":      profiles,
		"profile_count": len(profiles),
	})
}
