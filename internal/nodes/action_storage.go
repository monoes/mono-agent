package nodes

import (
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/monoes/mono-agent/internal/personitem"
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
// upserting a people row for the person the item is about (a profile, or
// the author of a post or comment — see personitem.Resolve; extracted items
// reaching here have already been through NormalizeBrowserItem). Items that
// name no person (a post without an author, a company page) still get a
// workflow_node_targets row (person_id left null) so the interaction is
// still visible, just not linked to a person.
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

	// `node run` executes a node standalone under a synthetic execution ID
	// ("cli") that has no workflow_executions row; the FK would reject every
	// insert and fail the whole action. Keep the targets, unattached.
	executionID := sql.NullString{String: s.executionID, Valid: true}
	var one int
	if err := tx.QueryRow(`SELECT 1 FROM workflow_executions WHERE id = ?`, s.executionID).Scan(&one); err != nil {
		if err != sql.ErrNoRows {
			return fmt.Errorf("nodes: looking up execution %s: %w", s.executionID, err)
		}
		executionID = sql.NullString{}
	}

	upsertPerson, err := tx.Prepare(`
		INSERT INTO people (id, platform_username, platform, full_name, image_url,
		        website, introduction, is_verified, job_title, headline, location, about,
		        experience, education, profile_url, profile_id, created_at, updated_at)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(platform_username, platform, profile_id)
		DO UPDATE SET
		  full_name    = COALESCE(excluded.full_name, people.full_name),
		  image_url    = COALESCE(excluded.image_url, people.image_url),
		  website      = COALESCE(excluded.website, people.website),
		  introduction = COALESCE(excluded.introduction, people.introduction),
		  is_verified  = COALESCE(excluded.is_verified, people.is_verified),
		  job_title    = COALESCE(excluded.job_title, people.job_title),
		  headline     = COALESCE(excluded.headline, people.headline),
		  location     = COALESCE(excluded.location, people.location),
		  about        = COALESCE(excluded.about, people.about),
		  experience   = COALESCE(excluded.experience, people.experience),
		  education    = COALESCE(excluded.education, people.education),
		  profile_url  = COALESCE(people.profile_url, excluded.profile_url),
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
		// The target's link is what the node acted on (a profile, a post).
		link, _ := item["url"].(string)
		if link == "" {
			link, _ = item["profile_url"].(string)
		}

		var personID sql.NullString
		if ref, ok := personitem.Resolve(platform, item); ok {
			// An item about a post or comment names its author: only the
			// author's name describes the person, the rest is the post's.
			var prof personitem.Profile
			var introduction string
			var isVerified interface{}
			if ref.Author {
				prof.FullName = ref.FullName
			} else {
				prof = personitem.ProfileOf(item)
				introduction, _ = item["introduction"].(string)
				if v, ok := item["is_verified"].(bool); ok && v {
					isVerified = 1
				}
			}
			platformUpper := strings.ToUpper(platform)
			if _, err := upsertPerson.Exec(
				uuid.New().String(), ref.Username, platformUpper,
				nullIfEmpty(prof.FullName), nullIfEmpty(prof.ImageURL), nullIfEmpty(prof.Website),
				nullIfEmpty(introduction), isVerified, nullIfEmpty(prof.JobTitle),
				nullIfEmpty(prof.Headline), nullIfEmpty(prof.Location), nullIfEmpty(prof.About),
				nullIfEmpty(prof.Experience), nullIfEmpty(prof.Education), nullIfEmpty(ref.ProfileURL),
				profileID, now, now,
			); err != nil {
				return fmt.Errorf("nodes: upserting person %s: %w", ref.Username, err)
			}
			var pid string
			if err := lookupPerson.QueryRow(ref.Username, platformUpper, profileID).Scan(&pid); err == nil {
				personID = sql.NullString{String: pid, Valid: true}
			}
		}

		commentText, _ := item["comment_text"].(string)
		if commentText == "" {
			commentText, _ = item["response_text"].(string)
		}

		if _, err := insertTarget.Exec(
			uuid.New().String(), executionID, s.nodeID, personID, strings.ToUpper(platform),
			nullIfEmpty(link), "COMPLETED", now.Format(time.RFC3339), nullIfEmpty(commentText), nil, now,
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
