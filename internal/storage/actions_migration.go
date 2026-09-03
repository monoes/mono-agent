package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/google/uuid"
)

// MigrateActionsToWorkflows converts every row in the legacy standalone
// actions/action_targets tables into an equivalent single-node workflow +
// workflow_execution (+ workflow_node_targets for its targets), then drops
// actions/action_targets/action_daily_counters.
//
// This can't be a plain .sql migration (see 030_workflow_action_bridge.sql)
// because building each node's config JSON requires merging several source
// columns per row, which needs real branching, not just INSERT...SELECT.
//
// Idempotent by construction: it starts by checking whether the `actions`
// table still exists. Once the migration has run once (and dropped it),
// every later call is a no-op — there is no separate "already migrated"
// flag to keep in sync.
func MigrateActionsToWorkflows(ctx context.Context, db *sql.DB) error {
	var actionsTableExists int
	if err := db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='actions'`,
	).Scan(&actionsTableExists); err != nil {
		return fmt.Errorf("checking for legacy actions table: %w", err)
	}
	if actionsTableExists == 0 {
		return nil
	}

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("beginning actions-to-workflows migration transaction: %w", err)
	}
	defer tx.Rollback()

	rows, err := tx.QueryContext(ctx, `
		SELECT id, title, type, state, target_platform, content_subject,
		       content_message, content_blob_urls, keywords, COALESCE(params, '{}'), profile_id,
		       created_at_ts, updated_at_ts
		FROM actions
	`)
	if err != nil {
		return fmt.Errorf("reading legacy actions: %w", err)
	}

	type legacyAction struct {
		id, title, actionType, state, platform                              string
		contentSubject, contentMessage, contentBlobURLs, keywords, paramsJS string
		profileID, createdAt, updatedAt                                     string
	}
	var actionsToMigrate []legacyAction
	for rows.Next() {
		var a legacyAction
		var contentSubject, contentMessage, contentBlobURLs, keywords, createdAt, updatedAt sql.NullString
		if err := rows.Scan(&a.id, &a.title, &a.actionType, &a.state, &a.platform,
			&contentSubject, &contentMessage, &contentBlobURLs, &keywords, &a.paramsJS, &a.profileID,
			&createdAt, &updatedAt); err != nil {
			rows.Close()
			return fmt.Errorf("scanning legacy action: %w", err)
		}
		a.contentSubject, a.contentMessage, a.keywords = contentSubject.String, contentMessage.String, keywords.String
		a.contentBlobURLs = contentBlobURLs.String
		a.createdAt, a.updatedAt = createdAt.String, updatedAt.String
		actionsToMigrate = append(actionsToMigrate, a)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return fmt.Errorf("iterating legacy actions: %w", err)
	}
	rows.Close()

	migratedActions, migratedTargets := 0, 0
	for _, a := range actionsToMigrate {
		config := map[string]interface{}{}
		if a.paramsJS != "" {
			_ = json.Unmarshal([]byte(a.paramsJS), &config)
		}
		if a.contentMessage != "" {
			config["message"] = a.contentMessage
		}
		if a.contentSubject != "" {
			config["subject"] = a.contentSubject
		}
		if a.keywords != "" {
			config["keywords"] = a.keywords
		}
		if a.contentBlobURLs != "" {
			// "blob_urls" matches the key name attached-media action JSON
			// definitions already expect (see storage.Action's
			// content_blob_urls handling) — preserved so migrated
			// image/attachment-bearing actions don't silently lose them.
			config["blob_urls"] = a.contentBlobURLs
		}
		configJSON, err := json.Marshal(config)
		if err != nil {
			return fmt.Errorf("encoding config for migrated action %s: %w", a.id, err)
		}

		// created_at/updated_at are passed explicitly rather than relying on
		// the columns' DB-level defaults — verified live that those defaults
		// are broken on existing installs (ReconcileSchema's table-rebuild
		// history leaves workflows.created_at/updated_at and
		// workflow_nodes.created_at/updated_at as `DEFAULT ''`, not
		// CURRENT_TIMESTAMP, even though 006_workflow_system.sql declares
		// them correctly) — every other INSERT into these tables in the
		// codebase (e.g. internal/ai/chat/tools.go's CanvasTools) already
		// works around this the same way.
		now := time.Now().UTC().Format(time.RFC3339)

		workflowID := uuid.New().String()
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO workflows (id, name, description, is_active, version, profile_id, created_at, updated_at)
			 VALUES (?, ?, ?, 0, 1, ?, ?, ?)`,
			workflowID, a.title, "Migrated from the standalone Actions page", a.profileID, now, now,
		); err != nil {
			return fmt.Errorf("inserting migrated workflow for action %s: %w", a.id, err)
		}

		nodeID := uuid.New().String()
		nodeType := strings.ToLower(a.platform) + "." + strings.ToLower(a.actionType)
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO workflow_nodes (id, workflow_id, node_type, name, config, created_at, updated_at)
			 VALUES (?, ?, ?, ?, ?, ?, ?)`,
			nodeID, workflowID, nodeType, a.title, string(configJSON), now, now,
		); err != nil {
			return fmt.Errorf("inserting migrated node for action %s: %w", a.id, err)
		}

		status := "CANCELLED"
		switch a.state {
		case "COMPLETED":
			status = "SUCCESS"
		case "FAILED":
			status = "FAILED"
		}
		executionID := uuid.New().String()
		startedAt, finishedAt := nullIfEmpty(a.createdAt), nullIfEmpty(a.updatedAt)
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO workflow_executions (id, workflow_id, status, trigger_type, started_at, finished_at, profile_id)
			 VALUES (?, ?, ?, 'manual', ?, ?, ?)`,
			executionID, workflowID, status, startedAt, finishedAt, a.profileID,
		); err != nil {
			return fmt.Errorf("inserting migrated execution for action %s: %w", a.id, err)
		}
		migratedActions++

		targetRows, err := tx.QueryContext(ctx, `
			SELECT id, person_id, platform, link, source_type, status, last_interacted_at, comment_text, metadata, created_at
			FROM action_targets WHERE action_id = ?
		`, a.id)
		if err != nil {
			return fmt.Errorf("reading targets for action %s: %w", a.id, err)
		}
		for targetRows.Next() {
			var origID string
			var personID, platform, link, sourceType, tStatus, lastInteracted, commentText, metadata, createdAt sql.NullString
			if err := targetRows.Scan(&origID, &personID, &platform, &link, &sourceType, &tStatus, &lastInteracted, &commentText, &metadata, &createdAt); err != nil {
				targetRows.Close()
				return fmt.Errorf("scanning target for action %s: %w", a.id, err)
			}
			targetPlatform := a.platform
			if platform.Valid && platform.String != "" {
				targetPlatform = platform.String
			}
			if _, err := tx.ExecContext(ctx,
				`INSERT INTO workflow_node_targets
				   (id, execution_id, node_id, person_id, platform, link, status, last_interacted_at, comment_text, metadata, created_at)
				 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
				uuid.New().String(), executionID, nodeID, nullIfEmptyNS(personID), targetPlatform,
				nullIfEmptyNS(link), valueOr(tStatus, "PENDING"), nullIfEmptyNS(lastInteracted),
				nullIfEmptyNS(commentText), mergeSourceTypeIntoMetadata(metadata, sourceType), nullIfEmptyNS(createdAt),
			); err != nil {
				targetRows.Close()
				return fmt.Errorf("inserting migrated target for action %s: %w", a.id, err)
			}
			migratedTargets++
		}
		if err := targetRows.Err(); err != nil {
			targetRows.Close()
			return fmt.Errorf("iterating targets for action %s: %w", a.id, err)
		}
		targetRows.Close()
	}

	for _, stmt := range []string{
		`DROP TABLE IF EXISTS action_targets`,
		`DROP TABLE IF EXISTS actions`,
		`DROP TABLE IF EXISTS action_daily_counters`,
	} {
		if _, err := tx.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("dropping legacy table (%s): %w", stmt, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("committing actions-to-workflows migration: %w", err)
	}

	log.Printf("migrated %d legacy action(s) and %d target(s) into workflows; dropped actions/action_targets/action_daily_counters", migratedActions, migratedTargets)
	return nil
}

func nullIfEmpty(s string) interface{} {
	if s == "" {
		return nil
	}
	return s
}

func nullIfEmptyNS(s sql.NullString) interface{} {
	if !s.Valid || s.String == "" {
		return nil
	}
	return s.String
}

func valueOr(s sql.NullString, fallback string) string {
	if !s.Valid || s.String == "" {
		return fallback
	}
	return s.String
}

// mergeSourceTypeIntoMetadata folds action_targets.source_type (target
// provenance — e.g. "from-list" vs "from-search"; dropped by the new schema,
// which has no dedicated column for it) into workflow_node_targets.metadata
// rather than discarding it. If metadata already holds a JSON object, adds
// a source_type key; if it's empty, becomes {"source_type": "..."}; if it's
// non-JSON legacy text, wraps it so neither value is lost. Returns nil
// (SQL NULL) if there's nothing to preserve.
func mergeSourceTypeIntoMetadata(metadata, sourceType sql.NullString) interface{} {
	st := ""
	if sourceType.Valid {
		st = sourceType.String
	}
	md := ""
	if metadata.Valid {
		md = metadata.String
	}
	if st == "" {
		return nullIfEmpty(md)
	}
	if md == "" {
		out, _ := json.Marshal(map[string]interface{}{"source_type": st})
		return string(out)
	}
	var parsed map[string]interface{}
	if json.Unmarshal([]byte(md), &parsed) == nil {
		parsed["source_type"] = st
		out, _ := json.Marshal(parsed)
		return string(out)
	}
	out, _ := json.Marshal(map[string]interface{}{"source_type": st, "legacy_metadata": md})
	return string(out)
}
