package ai

import (
	"database/sql"
	"fmt"
	"strings"
	"time"
)

// ChatMessage represents a single message in an AI chat conversation.
type ChatMessage struct {
	ID         string `json:"id"`
	WorkflowID string `json:"workflow_id"`
	// ProfileID scopes the message to a profile. This matters most for the
	// "general"/"draft" placeholder workflow IDs (see CanvasTools.checkWorkflowOwnership)
	// which are literal, non-unique strings shared by every profile — without
	// this column, one profile's global assistant chat would read, and
	// ClearChatHistory would delete, another profile's messages under the
	// same workflow_id. Empty normalizes to "default".
	ProfileID  string `json:"profile_id,omitempty"`
	Role       string `json:"role"` // "user" | "assistant" | "tool"
	Content    string `json:"content"`
	ToolCalls  string `json:"tool_calls,omitempty"`   // JSON array
	ToolCallID string `json:"tool_call_id,omitempty"` // For tool result messages
	ProviderID string `json:"provider_id,omitempty"`
	Model      string `json:"model,omitempty"`
	TokenCount int    `json:"token_count,omitempty"`
	// SessionID is the underlying agent runtime's resumable session id
	// (monomind Agent Exec Protocol's `session_id`, passed back via
	// `--resume`) — empty for messages saved before this field existed, or
	// for turns where the runtime never returned one. Messages sharing a
	// SessionID within one WorkflowID form one continuable conversation.
	SessionID string `json:"session_id,omitempty"`
	CreatedAt string `json:"created_at"`
}

// ChatSession summarizes one resumable conversation — all ChatMessage rows
// sharing a (WorkflowID, SessionID) pair — for a past-sessions list.
type ChatSession struct {
	SessionID    string `json:"session_id"`
	WorkflowID   string `json:"workflow_id"`
	Runtime      string `json:"runtime,omitempty"`
	Model        string `json:"model,omitempty"`
	Preview      string `json:"preview,omitempty"` // first user message, truncated
	MessageCount int    `json:"message_count"`
	StartedAt    string `json:"started_at"`
	UpdatedAt    string `json:"updated_at"`
}

// AIStore provides persistence for chat messages and the conversation/turn/event
// history (chat_events.go).
type AIStore struct {
	db *sql.DB
}

// NewAIStore creates a new AIStore and ensures the required tables exist.
func NewAIStore(db *sql.DB) (*AIStore, error) {
	s := &AIStore{db: db}
	if err := s.initTables(); err != nil {
		return nil, fmt.Errorf("ai store: init tables: %w", err)
	}
	return s, nil
}

// OpenAIStore returns an AIStore over db without creating or altering any
// table, for read-only callers such as `monoagentcli doctor` (whose checks
// must not change the database). The tables must already exist.
func OpenAIStore(db *sql.DB) *AIStore { return &AIStore{db: db} }

func (s *AIStore) initTables() error {
	const messagesSQL = `CREATE TABLE IF NOT EXISTS ai_chat_messages (
		id TEXT PRIMARY KEY,
		workflow_id TEXT NOT NULL,
		role TEXT NOT NULL,
		content TEXT NOT NULL,
		tool_calls TEXT NOT NULL DEFAULT '',
		tool_call_id TEXT NOT NULL DEFAULT '',
		provider_id TEXT NOT NULL DEFAULT '',
		model TEXT NOT NULL DEFAULT '',
		token_count INTEGER NOT NULL DEFAULT 0,
		session_id TEXT NOT NULL DEFAULT '',
		-- This defaults to '' (not 'default') — a sentinel that never matches any
		-- real profile's exact-match lookups. SaveChatMessage always writes a
		-- real profile ID, so '' only ever occurs on rows that predate this
		-- column; those must stay excluded from every profile's history
		-- rather than being silently attributed to whichever profile is
		-- named "default" (see the legacy-ambiguous-exclusion requirement).
		profile_id TEXT NOT NULL DEFAULT '',
		created_at TEXT NOT NULL
	)`

	if _, err := s.db.Exec(messagesSQL); err != nil {
		return fmt.Errorf("create ai_chat_messages: %w", err)
	}
	// Migrate: add columns that may be missing on existing DBs. SQLite errors
	// if the column already exists ("duplicate column name: ..."); that one
	// error is expected and ignored, but any other failure (disk full,
	// locked DB, corrupted schema) must propagate instead of being silently
	// swallowed alongside it.
	if err := addColumnIfMissing(s.db, `ALTER TABLE ai_chat_messages ADD COLUMN tool_call_id TEXT NOT NULL DEFAULT ''`); err != nil {
		return err
	}
	if err := addColumnIfMissing(s.db, `ALTER TABLE ai_chat_messages ADD COLUMN session_id TEXT NOT NULL DEFAULT ''`); err != nil {
		return err
	}
	// Backfill to an empty-string sentinel (ambiguous), not "default" — see
	// messagesSQL's comment.
	if err := addColumnIfMissing(s.db, `ALTER TABLE ai_chat_messages ADD COLUMN profile_id TEXT NOT NULL DEFAULT ''`); err != nil {
		return err
	}
	if err := s.initChatEventTables(); err != nil {
		return err
	}
	return nil
}

// addColumnIfMissing runs an ALTER TABLE ... ADD COLUMN statement, treating
// SQLite's "duplicate column name" error (the column already exists, from a
// previous run of this same migration) as success, and propagating any
// other error instead of discarding it.
func addColumnIfMissing(db *sql.DB, alterSQL string) error {
	_, err := db.Exec(alterSQL)
	if err == nil || strings.Contains(err.Error(), "duplicate column name") {
		return nil
	}
	return fmt.Errorf("migrate: %s: %w", alterSQL, err)
}

// SaveChatMessage inserts a chat message. If CreatedAt is empty it is set to
// now. If ProfileID is empty it defaults to "default".
func (s *AIStore) SaveChatMessage(m ChatMessage) error {
	if m.CreatedAt == "" {
		m.CreatedAt = time.Now().UTC().Format(time.RFC3339)
	}
	if m.ProfileID == "" {
		m.ProfileID = "default"
	}
	const q = `INSERT INTO ai_chat_messages (id, workflow_id, role, content, tool_calls, tool_call_id, provider_id, model, token_count, session_id, profile_id, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`
	_, err := s.db.Exec(q,
		m.ID, m.WorkflowID, m.Role, m.Content,
		m.ToolCalls, m.ToolCallID, m.ProviderID, m.Model,
		m.TokenCount, m.SessionID, m.ProfileID, m.CreatedAt,
	)
	return err
}

// GetChatHistory returns all messages for a workflow, scoped to profileID
// (empty normalizes to "default"), ordered by created_at ascending. Exact
// match (not COALESCE) against profile_id: ambiguous legacy rows predating
// this column carry an empty-string sentinel and are never returned, for
// any profileID including "default" — see messagesSQL's comment. rowid is
// the tiebreaker: SQLite rowids are monotonic for inserts, so messages
// saved within the same timestamp (RFC3339 has second granularity) still
// come back in insert order — no seq column needed.
func (s *AIStore) GetChatHistory(workflowID, profileID string) ([]ChatMessage, error) {
	if profileID == "" {
		profileID = "default"
	}
	const q = `SELECT id, workflow_id, role, content, tool_calls, tool_call_id, provider_id, model, token_count, session_id, profile_id, created_at
		FROM ai_chat_messages WHERE workflow_id = ? AND profile_id = ? ORDER BY created_at ASC, rowid ASC`
	rows, err := s.db.Query(q, workflowID, profileID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var messages []ChatMessage
	for rows.Next() {
		var m ChatMessage
		if err := rows.Scan(
			&m.ID, &m.WorkflowID, &m.Role, &m.Content,
			&m.ToolCalls, &m.ToolCallID, &m.ProviderID, &m.Model,
			&m.TokenCount, &m.SessionID, &m.ProfileID, &m.CreatedAt,
		); err != nil {
			return nil, err
		}
		messages = append(messages, m)
	}
	return messages, rows.Err()
}

// GetSessionMessages returns messages for one resumable session within a
// workflow, scoped to profileID (empty normalizes to "default"), ordered by
// created_at ascending.
func (s *AIStore) GetSessionMessages(workflowID, sessionID, profileID string) ([]ChatMessage, error) {
	if profileID == "" {
		profileID = "default"
	}
	const q = `SELECT id, workflow_id, role, content, tool_calls, tool_call_id, provider_id, model, token_count, session_id, profile_id, created_at
		FROM ai_chat_messages WHERE workflow_id = ? AND session_id = ? AND profile_id = ? ORDER BY created_at ASC`
	rows, err := s.db.Query(q, workflowID, sessionID, profileID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var messages []ChatMessage
	for rows.Next() {
		var m ChatMessage
		if err := rows.Scan(
			&m.ID, &m.WorkflowID, &m.Role, &m.Content,
			&m.ToolCalls, &m.ToolCallID, &m.ProviderID, &m.Model,
			&m.TokenCount, &m.SessionID, &m.ProfileID, &m.CreatedAt,
		); err != nil {
			return nil, err
		}
		messages = append(messages, m)
	}
	return messages, rows.Err()
}

// ListChatSessions groups a workflow's messages, scoped to profileID, by
// session_id into a most-recently-updated-first list, for a past-sessions
// browser. Messages with no session_id (saved before this field existed, or
// from a turn whose runtime never returned one) are excluded — they have no
// id to resume by.
func (s *AIStore) ListChatSessions(workflowID, profileID string) ([]ChatSession, error) {
	messages, err := s.GetChatHistory(workflowID, profileID)
	if err != nil {
		return nil, err
	}

	order := make([]string, 0)
	bySession := make(map[string]*ChatSession)
	for _, m := range messages {
		if m.SessionID == "" {
			continue
		}
		cs, ok := bySession[m.SessionID]
		if !ok {
			cs = &ChatSession{SessionID: m.SessionID, WorkflowID: workflowID, StartedAt: m.CreatedAt}
			bySession[m.SessionID] = cs
			order = append(order, m.SessionID)
		}
		if cs.Preview == "" && m.Role == "user" {
			cs.Preview = truncateChatPreview(m.Content)
		}
		cs.UpdatedAt = m.CreatedAt
		cs.MessageCount++
		if m.ProviderID != "" {
			cs.Runtime = m.ProviderID
		}
		if m.Model != "" {
			cs.Model = m.Model
		}
	}

	out := make([]ChatSession, 0, len(order))
	for i := len(order) - 1; i >= 0; i-- {
		out = append(out, *bySession[order[i]])
	}
	return out, nil
}

// truncateChatPreview shortens a message to a session-list preview length,
// cutting on a rune boundary.
func truncateChatPreview(s string) string {
	const maxLen = 80
	r := []rune(s)
	if len(r) <= maxLen {
		return s
	}
	return string(r[:maxLen]) + "…"
}

// ClearChatHistory deletes all messages for a given workflow, scoped to
// profileID (empty normalizes to "default") so clearing one profile's
// "general" chat can never delete another profile's messages under the same
// placeholder workflow_id.
func (s *AIStore) ClearChatHistory(workflowID, profileID string) error {
	if profileID == "" {
		profileID = "default"
	}
	_, err := s.db.Exec(`DELETE FROM ai_chat_messages WHERE workflow_id = ? AND profile_id = ?`, workflowID, profileID)
	return err
}
