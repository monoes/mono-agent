package nodes

import (
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/monoes/mono-agent/internal/bot"
)

// workflowActionStorage implements action.StorageInterface backed by the
// workflow_node_targets / workflow_daily_counters tables (see
// data/migrations/030_workflow_action_bridge.sql), instead of the legacy
// actions/action_targets/action_daily_counters tables the standalone Actions
// page used. It is what BrowserNode.Execute passes to action.NewActionExecutor
// so that running a browser-automation node inside a workflow persists the
// same interaction history and honors the same daily rate caps the removed
// Actions page used to.
//
// UpdateActionState/UpdateActionReachedIndex are no-ops here: a workflow
// node's run state is already tracked by the workflow engine itself
// (workflow_executions/workflow_execution_nodes) — there is no separate
// per-action state machine to keep in sync.
type workflowActionStorage struct {
	db          *sql.DB
	profileID   string
	executionID string
	nodeID      string
	platform    string
}

func (s *workflowActionStorage) UpdateActionState(id, state string) error { return nil }

func (s *workflowActionStorage) UpdateActionReachedIndex(id string, index int) error { return nil }

// SaveExtractedData writes one workflow_node_targets row per extracted item,
// upserting a people row for any item whose target can be resolved to a
// username (mirroring the person-upsert the legacy `monoagentcli run` path
// did, minus its richer count-parsing — extracted items reaching here have
// already been through NormalizeBrowserItem). Items that can't be resolved
// to a username still get a workflow_node_targets row (person_id left null)
// so the interaction is still visible, just not linked to a person.
func (s *workflowActionStorage) SaveExtractedData(actionID string, items []map[string]interface{}) error {
	if s.db == nil || len(items) == 0 {
		return nil
	}
	if s.executionID == "" || s.nodeID == "" {
		// No workflow context to attach targets to (e.g. Execute called
		// outside a real workflow run) — nothing sensible to persist.
		return nil
	}

	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("nodes: beginning SaveExtractedData transaction: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck

	profileID := s.profileID
	if profileID == "" {
		profileID = "default"
	}
	now := time.Now().UTC()

	upsertPerson, err := tx.Prepare(`
		INSERT INTO people (id, platform_username, platform, full_name, image_url,
		        website, introduction, is_verified, profile_id, created_at, updated_at)
		VALUES (?,?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(platform_username, platform, profile_id)
		DO UPDATE SET
		  full_name    = COALESCE(excluded.full_name, people.full_name),
		  image_url    = COALESCE(excluded.image_url, people.image_url),
		  website      = COALESCE(excluded.website, people.website),
		  introduction = COALESCE(excluded.introduction, people.introduction),
		  is_verified  = COALESCE(excluded.is_verified, people.is_verified),
		  updated_at   = excluded.updated_at`)
	if err != nil {
		return fmt.Errorf("nodes: preparing person upsert: %w", err)
	}
	defer upsertPerson.Close()

	lookupPerson, err := tx.Prepare(`SELECT id FROM people WHERE platform_username = ? AND platform = ? AND profile_id = ?`)
	if err != nil {
		return fmt.Errorf("nodes: preparing person lookup: %w", err)
	}
	defer lookupPerson.Close()

	insertTarget, err := tx.Prepare(`
		INSERT INTO workflow_node_targets
		  (id, execution_id, node_id, person_id, platform, link, status, last_interacted_at, comment_text, metadata, created_at)
		VALUES (?,?,?,?,?,?,?,?,?,?,?)`)
	if err != nil {
		return fmt.Errorf("nodes: preparing target insert: %w", err)
	}
	defer insertTarget.Close()

	for _, item := range items {
		platform, _ := item["platform"].(string)
		if platform == "" {
			platform = s.platform
		}
		profileURL, _ := item["url"].(string)
		if profileURL == "" {
			profileURL, _ = item["profile_url"].(string)
		}

		username := extractUsernameFromURL(platform, profileURL)

		var personID sql.NullString
		if username != "" {
			fullName, _ := item["full_name"].(string)
			imageURL, _ := item["image_url"].(string)
			website, _ := item["website"].(string)
			introduction, _ := item["introduction"].(string)
			var isVerified int
			if v, ok := item["is_verified"].(bool); ok && v {
				isVerified = 1
			}

			if _, err := upsertPerson.Exec(
				uuid.New().String(), username, strings.ToUpper(platform),
				nullIfEmpty(fullName), nullIfEmpty(imageURL), nullIfEmpty(website),
				nullIfEmpty(introduction), isVerified, profileID, now, now,
			); err != nil {
				return fmt.Errorf("nodes: upserting person %s: %w", username, err)
			}
			var pid string
			if err := lookupPerson.QueryRow(username, strings.ToUpper(platform), profileID).Scan(&pid); err == nil {
				personID = sql.NullString{String: pid, Valid: true}
			}
		}

		commentText, _ := item["comment_text"].(string)
		if commentText == "" {
			commentText, _ = item["response_text"].(string)
		}

		if _, err := insertTarget.Exec(
			uuid.New().String(), s.executionID, s.nodeID, personID, strings.ToUpper(platform),
			nullIfEmpty(profileURL), "COMPLETED", now.Format(time.RFC3339), nullIfEmpty(commentText), nil, now,
		); err != nil {
			return fmt.Errorf("nodes: inserting workflow_node_target: %w", err)
		}
	}

	return tx.Commit()
}

// GetDailyActionCount and IncrementDailyActionCount key the daily-cap
// counter on "<platform>.<actionType>" (the same shape as the node's
// registered node_type, e.g. "instagram.follow_users") rather than the bare
// actionType action.ActionExecutor passes in — the legacy actions table
// scoped caps per-platform implicitly (each action row had its own
// target_platform); this makes that scoping explicit in the key itself.
func (s *workflowActionStorage) GetDailyActionCount(actionType string) (int, error) {
	if s.db == nil {
		return 0, nil
	}
	var count int
	err := s.db.QueryRow(
		`SELECT count FROM workflow_daily_counters WHERE profile_key = ? AND node_type = ? AND day = ?`,
		s.dailyCounterKey(), s.nodeTypeKey(actionType), today(),
	).Scan(&count)
	if err == sql.ErrNoRows {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("nodes: reading daily action count: %w", err)
	}
	return count, nil
}

func (s *workflowActionStorage) IncrementDailyActionCount(actionType string) (int, error) {
	if s.db == nil {
		return 0, nil
	}
	if _, err := s.db.Exec(`
		INSERT INTO workflow_daily_counters (profile_key, node_type, day, count, updated_at)
		VALUES (?, ?, ?, 1, CURRENT_TIMESTAMP)
		ON CONFLICT(profile_key, node_type, day) DO UPDATE SET
		  count = count + 1, updated_at = CURRENT_TIMESTAMP`,
		s.dailyCounterKey(), s.nodeTypeKey(actionType), today(),
	); err != nil {
		return 0, fmt.Errorf("nodes: incrementing daily action count: %w", err)
	}
	return s.GetDailyActionCount(actionType)
}

func (s *workflowActionStorage) dailyCounterKey() string {
	if s.profileID == "" {
		return "default"
	}
	return s.profileID
}

func (s *workflowActionStorage) nodeTypeKey(actionType string) string {
	return strings.ToLower(s.platform) + "." + strings.ToLower(actionType)
}

func today() string {
	return time.Now().UTC().Format("2006-01-02")
}

func nullIfEmpty(s string) interface{} {
	if s == "" {
		return nil
	}
	return s
}

// extractUsernameFromURL mirrors the username-extraction fallback chain the
// legacy `monoagentcli run` execution path used: try the platform's bot
// adapter first (ExtractUsername understands each platform's URL shape),
// then fall back to the URL's last path segment.
func extractUsernameFromURL(platform, profileURL string) string {
	platformUpper := strings.ToUpper(platform)
	if factory, ok := bot.PlatformRegistry[platformUpper]; ok {
		if u := factory().ExtractUsername(profileURL); u != "" {
			return u
		}
	}
	if profileURL == "" {
		return ""
	}
	parts := strings.Split(strings.Trim(profileURL, "/"), "/")
	if len(parts) == 0 {
		return ""
	}
	return strings.TrimPrefix(parts[len(parts)-1], "@")
}
