package ai

import (
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/monoes/mono-agent/internal/ai/chatevents"
)

// ErrTurnActive is returned by DeleteConversation when a turn is still
// active — deletion while a supervisor may be mid-write would race the
// journal and could leave a running process pointed at a vanished
// conversation.
var ErrTurnActive = errors.New("chat: conversation has an active turn")

// ErrConversationNotFound / ErrTurnNotFound distinguish "doesn't exist or
// isn't yours" from a generic sql.ErrNoRows leaking scoping details to
// callers — every lookup here is profile-scoped, so a wrong profileID and a
// truly missing ID look identical on purpose.
var (
	ErrConversationNotFound = errors.New("chat: conversation not found")
	ErrTurnNotFound         = errors.New("chat: turn not found")
)

// Conversation is one server-created app-level chat conversation — see the
// plan's "three distinct identifiers" section. HistoryKey is the opaque
// provider model-context bucket; it is deliberately excluded from JSON so
// it never reaches the frontend or a Wails response.
type Conversation struct {
	ID              string `json:"id"`
	ProfileID       string `json:"profileId"`
	Backend         string `json:"backend"` // "agent" | "provider"
	WorkflowContext string `json:"workflowContext"`
	RuntimeID       string `json:"runtimeId,omitempty"`
	ProviderID      string `json:"providerId,omitempty"`
	Model           string `json:"model,omitempty"`
	SessionID       string `json:"sessionId,omitempty"`
	HistoryKey      string `json:"-"`
	CreatedAt       string `json:"createdAt"`
	UpdatedAt       string `json:"updatedAt"`
}

// Turn is one client-registered turn within a conversation — the mutable
// projection the supervisor updates as a run progresses and finalizes.
type Turn struct {
	ID               string `json:"id"`
	ConversationID   string `json:"conversationId"`
	ProfileID        string `json:"profileId"`
	OwnerInstanceID  string `json:"-"`
	Prompt           string `json:"prompt"`
	Status           string `json:"status"` // active|completed|failed|cancelled|interrupted
	Reason           string `json:"reason,omitempty"`
	LastCommittedSeq int64  `json:"lastCommittedSeq"`
	CreatedAt        string `json:"createdAt"`
	UpdatedAt        string `json:"updatedAt"`
}

const turnStatusActive = "active"

func (s *AIStore) initChatEventTables() error {
	const conversationsSQL = `CREATE TABLE IF NOT EXISTS ai_chat_conversations (
		id TEXT PRIMARY KEY,
		profile_id TEXT NOT NULL,
		backend TEXT NOT NULL,
		workflow_context TEXT NOT NULL DEFAULT '',
		runtime_id TEXT NOT NULL DEFAULT '',
		provider_id TEXT NOT NULL DEFAULT '',
		model TEXT NOT NULL DEFAULT '',
		session_id TEXT NOT NULL DEFAULT '',
		history_key TEXT NOT NULL DEFAULT '',
		created_at TEXT NOT NULL,
		updated_at TEXT NOT NULL
	)`
	const turnsSQL = `CREATE TABLE IF NOT EXISTS ai_chat_turns (
		id TEXT PRIMARY KEY,
		conversation_id TEXT NOT NULL,
		profile_id TEXT NOT NULL,
		owner_instance_id TEXT NOT NULL DEFAULT '',
		prompt TEXT NOT NULL,
		status TEXT NOT NULL DEFAULT 'active',
		reason TEXT NOT NULL DEFAULT '',
		last_committed_seq INTEGER NOT NULL DEFAULT 0,
		created_at TEXT NOT NULL,
		updated_at TEXT NOT NULL
	)`
	// Composite primary key doubles as the unique (profile_id,
	// conversation_id, turn_id, seq) constraint the plan requires, and gives
	// an efficient natural index for "events after seq N" catch-up queries.
	const eventsSQL = `CREATE TABLE IF NOT EXISTS ai_chat_events (
		profile_id TEXT NOT NULL,
		conversation_id TEXT NOT NULL,
		turn_id TEXT NOT NULL,
		seq INTEGER NOT NULL,
		created_at TEXT NOT NULL,
		version INTEGER NOT NULL,
		type TEXT NOT NULL,
		payload TEXT NOT NULL,
		PRIMARY KEY (profile_id, conversation_id, turn_id, seq)
	)`
	if _, err := s.db.Exec(conversationsSQL); err != nil {
		return fmt.Errorf("create ai_chat_conversations: %w", err)
	}
	if _, err := s.db.Exec(turnsSQL); err != nil {
		return fmt.Errorf("create ai_chat_turns: %w", err)
	}
	if _, err := s.db.Exec(eventsSQL); err != nil {
		return fmt.Errorf("create ai_chat_events: %w", err)
	}
	if _, err := s.db.Exec(`CREATE INDEX IF NOT EXISTS idx_ai_chat_conversations_profile ON ai_chat_conversations(profile_id, updated_at DESC)`); err != nil {
		return fmt.Errorf("create idx_ai_chat_conversations_profile: %w", err)
	}
	if _, err := s.db.Exec(`CREATE INDEX IF NOT EXISTS idx_ai_chat_turns_conversation ON ai_chat_turns(conversation_id, profile_id, created_at DESC)`); err != nil {
		return fmt.Errorf("create idx_ai_chat_turns_conversation: %w", err)
	}
	return nil
}

func nowRFC3339() string { return time.Now().UTC().Format(time.RFC3339) }

// CreateConversation creates and returns a new app-level conversation
// scoped to profileID. HistoryKey is generated unconditionally (harmless
// when unused by an agent-backend conversation) so provider conversations
// always have an opaque model-context bucket distinct from workflowContext.
func (s *AIStore) CreateConversation(profileID, backend, workflowContext, runtimeID, providerID, model string) (Conversation, error) {
	if profileID == "" {
		profileID = "default"
	}
	now := nowRFC3339()
	c := Conversation{
		ID:              uuid.NewString(),
		ProfileID:       profileID,
		Backend:         backend,
		WorkflowContext: workflowContext,
		RuntimeID:       runtimeID,
		ProviderID:      providerID,
		Model:           model,
		HistoryKey:      uuid.NewString(),
		CreatedAt:       now,
		UpdatedAt:       now,
	}
	const q = `INSERT INTO ai_chat_conversations
		(id, profile_id, backend, workflow_context, runtime_id, provider_id, model, session_id, history_key, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, '', ?, ?, ?)`
	if _, err := s.db.Exec(q, c.ID, c.ProfileID, c.Backend, c.WorkflowContext, c.RuntimeID, c.ProviderID, c.Model, c.HistoryKey, c.CreatedAt, c.UpdatedAt); err != nil {
		return Conversation{}, fmt.Errorf("create conversation: %w", err)
	}
	return c, nil
}

// GetConversation returns a conversation, scoped to profileID —
// ErrConversationNotFound both when the ID doesn't exist and when it
// belongs to a different profile, so callers cannot distinguish "wrong
// owner" from "doesn't exist".
func (s *AIStore) GetConversation(id, profileID string) (Conversation, error) {
	if profileID == "" {
		profileID = "default"
	}
	const q = `SELECT id, profile_id, backend, workflow_context, runtime_id, provider_id, model, session_id, history_key, created_at, updated_at
		FROM ai_chat_conversations WHERE id = ? AND profile_id = ?`
	var c Conversation
	err := s.db.QueryRow(q, id, profileID).Scan(
		&c.ID, &c.ProfileID, &c.Backend, &c.WorkflowContext, &c.RuntimeID, &c.ProviderID, &c.Model, &c.SessionID, &c.HistoryKey, &c.CreatedAt, &c.UpdatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return Conversation{}, ErrConversationNotFound
	}
	if err != nil {
		return Conversation{}, fmt.Errorf("get conversation: %w", err)
	}
	return c, nil
}

// ListConversations returns profileID's conversations newest-updated-first.
// cursor is the previous page's last updated_at+id (opaque to the caller);
// empty starts from the top. Returns the next cursor, or "" when exhausted.
func (s *AIStore) ListConversations(profileID, cursor string, limit int) ([]Conversation, string, error) {
	if profileID == "" {
		profileID = "default"
	}
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	args := []any{profileID}
	q := `SELECT id, profile_id, backend, workflow_context, runtime_id, provider_id, model, session_id, history_key, created_at, updated_at
		FROM ai_chat_conversations WHERE profile_id = ?`
	if cursor != "" {
		q += ` AND updated_at || '|' || id < ?`
		args = append(args, cursor)
	}
	q += ` ORDER BY updated_at DESC, id DESC LIMIT ?`
	args = append(args, limit+1)

	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, "", fmt.Errorf("list conversations: %w", err)
	}
	defer rows.Close()

	var out []Conversation
	for rows.Next() {
		var c Conversation
		if err := rows.Scan(&c.ID, &c.ProfileID, &c.Backend, &c.WorkflowContext, &c.RuntimeID, &c.ProviderID, &c.Model, &c.SessionID, &c.HistoryKey, &c.CreatedAt, &c.UpdatedAt); err != nil {
			return nil, "", fmt.Errorf("scan conversation: %w", err)
		}
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		return nil, "", err
	}

	nextCursor := ""
	if len(out) > limit {
		last := out[limit-1]
		nextCursor = last.UpdatedAt + "|" + last.ID
		out = out[:limit]
	}
	return out, nextCursor, nil
}

// BindConversationSession records a runtime-reported resumable session id
// on session.bound. A new backend/runtime/model always creates a fresh
// conversation (see the plan's "Conversation and model context" section) —
// this only ever updates an existing conversation's own binding.
func (s *AIStore) BindConversationSession(id, profileID, runtimeID, sessionID string) error {
	if profileID == "" {
		profileID = "default"
	}
	res, err := s.db.Exec(
		`UPDATE ai_chat_conversations SET runtime_id = ?, session_id = ?, updated_at = ? WHERE id = ? AND profile_id = ?`,
		runtimeID, sessionID, nowRFC3339(), id, profileID,
	)
	if err != nil {
		return fmt.Errorf("bind conversation session: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrConversationNotFound
	}
	return nil
}

// DeleteConversation removes a conversation and all its turns/events,
// scoped to profileID. Refuses while any turn is still active — a
// supervisor may be mid-write.
func (s *AIStore) DeleteConversation(id, profileID string) error {
	if profileID == "" {
		profileID = "default"
	}
	if _, err := s.GetConversation(id, profileID); err != nil {
		return err
	}
	var activeCount int
	if err := s.db.QueryRow(
		`SELECT COUNT(*) FROM ai_chat_turns WHERE conversation_id = ? AND profile_id = ? AND status = ?`,
		id, profileID, turnStatusActive,
	).Scan(&activeCount); err != nil {
		return fmt.Errorf("check active turns: %w", err)
	}
	if activeCount > 0 {
		return ErrTurnActive
	}

	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("begin delete conversation: %w", err)
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`DELETE FROM ai_chat_events WHERE conversation_id = ? AND profile_id = ?`, id, profileID); err != nil {
		return fmt.Errorf("delete events: %w", err)
	}
	if _, err := tx.Exec(`DELETE FROM ai_chat_turns WHERE conversation_id = ? AND profile_id = ?`, id, profileID); err != nil {
		return fmt.Errorf("delete turns: %w", err)
	}
	if _, err := tx.Exec(`DELETE FROM ai_chat_conversations WHERE id = ? AND profile_id = ?`, id, profileID); err != nil {
		return fmt.Errorf("delete conversation: %w", err)
	}
	return tx.Commit()
}

// CreateTurn registers a turn, idempotent by ID: "Duplicate Start with the
// same ID returns existing admission/status, never starts a second
// process." A caller must still check the returned Turn's Status/whether it
// pre-existed via the second return value if it needs to distinguish
// "just created" from "already existed" — the row itself is identical
// either way.
func (s *AIStore) CreateTurn(conversationID, profileID, turnID, ownerInstanceID, prompt string) (turn Turn, alreadyExisted bool, err error) {
	if profileID == "" {
		profileID = "default"
	}
	if existing, getErr := s.GetTurn(turnID, profileID); getErr == nil {
		return existing, true, nil
	} else if !errors.Is(getErr, ErrTurnNotFound) {
		return Turn{}, false, getErr
	}

	now := nowRFC3339()
	t := Turn{
		ID:              turnID,
		ConversationID:  conversationID,
		ProfileID:       profileID,
		OwnerInstanceID: ownerInstanceID,
		Prompt:          prompt,
		Status:          turnStatusActive,
		CreatedAt:       now,
		UpdatedAt:       now,
	}
	const q = `INSERT INTO ai_chat_turns (id, conversation_id, profile_id, owner_instance_id, prompt, status, reason, last_committed_seq, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, '', 0, ?, ?)`
	if _, err := s.db.Exec(q, t.ID, t.ConversationID, t.ProfileID, t.OwnerInstanceID, t.Prompt, t.Status, t.CreatedAt, t.UpdatedAt); err != nil {
		// A racing duplicate insert (two near-simultaneous Start calls with
		// the same client-generated ID) loses the INSERT but should still
		// resolve to "already existed", not an error.
		if existing, getErr := s.GetTurn(turnID, profileID); getErr == nil {
			return existing, true, nil
		}
		return Turn{}, false, fmt.Errorf("create turn: %w", err)
	}
	return t, false, nil
}

// GetTurn returns a turn scoped to profileID.
func (s *AIStore) GetTurn(turnID, profileID string) (Turn, error) {
	if profileID == "" {
		profileID = "default"
	}
	const q = `SELECT id, conversation_id, profile_id, owner_instance_id, prompt, status, reason, last_committed_seq, created_at, updated_at
		FROM ai_chat_turns WHERE id = ? AND profile_id = ?`
	var t Turn
	err := s.db.QueryRow(q, turnID, profileID).Scan(
		&t.ID, &t.ConversationID, &t.ProfileID, &t.OwnerInstanceID, &t.Prompt, &t.Status, &t.Reason, &t.LastCommittedSeq, &t.CreatedAt, &t.UpdatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return Turn{}, ErrTurnNotFound
	}
	if err != nil {
		return Turn{}, fmt.Errorf("get turn: %w", err)
	}
	return t, nil
}

// QueryActiveTurns returns every turn across every profile and conversation
// currently left in "active" status — the startup reconciliation query
// (app_chat.go's reconcileOrphanedTurns), deliberately unscoped by profile
// since it exists to sweep the WHOLE database for a previous process's
// unfinished turns, not to serve one profile's view.
func (s *AIStore) QueryActiveTurns() ([]Turn, error) {
	const q = `SELECT id, conversation_id, profile_id, owner_instance_id, prompt, status, reason, last_committed_seq, created_at, updated_at
		FROM ai_chat_turns WHERE status = ?`
	rows, err := s.db.Query(q, turnStatusActive)
	if err != nil {
		return nil, fmt.Errorf("query active turns: %w", err)
	}
	defer rows.Close()

	var turns []Turn
	for rows.Next() {
		var t Turn
		if err := rows.Scan(&t.ID, &t.ConversationID, &t.ProfileID, &t.OwnerInstanceID, &t.Prompt, &t.Status, &t.Reason, &t.LastCommittedSeq, &t.CreatedAt, &t.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan active turn: %w", err)
		}
		turns = append(turns, t)
	}
	return turns, rows.Err()
}

// ListTurns returns a conversation's turns newest-first, scoped to
// profileID.
func (s *AIStore) ListTurns(conversationID, profileID, cursor string, limit int) ([]Turn, string, error) {
	if profileID == "" {
		profileID = "default"
	}
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	args := []any{conversationID, profileID}
	q := `SELECT id, conversation_id, profile_id, owner_instance_id, prompt, status, reason, last_committed_seq, created_at, updated_at
		FROM ai_chat_turns WHERE conversation_id = ? AND profile_id = ?`
	if cursor != "" {
		q += ` AND created_at || '|' || id < ?`
		args = append(args, cursor)
	}
	q += ` ORDER BY created_at DESC, id DESC LIMIT ?`
	args = append(args, limit+1)

	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, "", fmt.Errorf("list turns: %w", err)
	}
	defer rows.Close()

	var out []Turn
	for rows.Next() {
		var t Turn
		if err := rows.Scan(&t.ID, &t.ConversationID, &t.ProfileID, &t.OwnerInstanceID, &t.Prompt, &t.Status, &t.Reason, &t.LastCommittedSeq, &t.CreatedAt, &t.UpdatedAt); err != nil {
			return nil, "", fmt.Errorf("scan turn: %w", err)
		}
		out = append(out, t)
	}
	if err := rows.Err(); err != nil {
		return nil, "", err
	}

	nextCursor := ""
	if len(out) > limit {
		last := out[limit-1]
		nextCursor = last.CreatedAt + "|" + last.ID
		out = out[:limit]
	}
	return out, nextCursor, nil
}

// AppendEvent allocates the next sequence number for (conversationID,
// turnID), inserts the event, and advances the turn's last_committed_seq —
// all in one transaction, committed before the caller may emit the
// equivalent live Wails event ("commit before emitting the identical
// event").
//
// This does NOT by itself make the MAX(seq)+1 read-then-insert race-free:
// modernc.org/sqlite opens plain "begin" (deferred) transactions unless the
// DSN sets _txlock=immediate (it doesn't, and changing that is a
// connection-wide behavior change out of scope here), so two concurrent
// AppendEvent calls on the same turn can both read the same MAX(seq) before
// either writes — the PRIMARY KEY then fails the second INSERT (or SQLite
// returns "database is locked") rather than silently corrupting data, but
// it is a hard failure, not a clean serialization. Callers must not invoke
// AppendEvent concurrently for the same (profileID, conversationID, turnID);
// the GUI supervisor is responsible for serializing writes per turn (single
// active writer per turn), matching this store's transactional guarantees.
func (s *AIStore) AppendEvent(profileID, conversationID, turnID string, typ chatevents.EventType, payload any) (chatevents.Event, error) {
	if profileID == "" {
		profileID = "default"
	}
	tx, err := s.db.Begin()
	if err != nil {
		return chatevents.Event{}, fmt.Errorf("begin append event: %w", err)
	}
	defer tx.Rollback()

	ev, err := appendEventTx(tx, profileID, conversationID, turnID, typ, payload, time.Now())
	if err != nil {
		return chatevents.Event{}, err
	}
	if _, err := tx.Exec(
		`UPDATE ai_chat_turns SET last_committed_seq = ?, updated_at = ? WHERE id = ? AND profile_id = ?`,
		ev.Seq, nowRFC3339(), turnID, profileID,
	); err != nil {
		return chatevents.Event{}, fmt.Errorf("advance last_committed_seq: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return chatevents.Event{}, fmt.Errorf("commit append event: %w", err)
	}
	return ev, nil
}

// appendEventTx does the sequence-allocation-and-insert step within an
// already-open transaction, shared by AppendEvent and FinalizeTurn (which
// needs its terminal-event insert in the SAME transaction as its
// compare-and-set status update).
func appendEventTx(tx *sql.Tx, profileID, conversationID, turnID string, typ chatevents.EventType, payload any, at time.Time) (chatevents.Event, error) {
	var maxSeq sql.NullInt64
	if err := tx.QueryRow(
		`SELECT MAX(seq) FROM ai_chat_events WHERE profile_id = ? AND conversation_id = ? AND turn_id = ?`,
		profileID, conversationID, turnID,
	).Scan(&maxSeq); err != nil {
		return chatevents.Event{}, fmt.Errorf("allocate sequence: %w", err)
	}
	seq := maxSeq.Int64 + 1

	ev, err := chatevents.New(profileID, conversationID, turnID, seq, at, typ, payload)
	if err != nil {
		return chatevents.Event{}, err
	}
	if _, err := tx.Exec(
		`INSERT INTO ai_chat_events (profile_id, conversation_id, turn_id, seq, created_at, version, type, payload) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		profileID, conversationID, turnID, ev.Seq, ev.At, ev.Version, string(ev.Type), string(ev.Payload),
	); err != nil {
		return chatevents.Event{}, fmt.Errorf("insert event: %w", err)
	}
	return ev, nil
}

// GetEvents returns a turn's events with seq > afterSeq, ascending — the
// shape both initial hydration (afterSeq=0) and gap catch-up (afterSeq=last
// known) use.
func (s *AIStore) GetEvents(conversationID, turnID, profileID string, afterSeq int64, limit int) ([]chatevents.Event, error) {
	if profileID == "" {
		profileID = "default"
	}
	if limit <= 0 || limit > 1000 {
		limit = 500
	}
	const q = `SELECT profile_id, conversation_id, turn_id, seq, created_at, version, type, payload
		FROM ai_chat_events WHERE profile_id = ? AND conversation_id = ? AND turn_id = ? AND seq > ?
		ORDER BY seq ASC LIMIT ?`
	rows, err := s.db.Query(q, profileID, conversationID, turnID, afterSeq, limit)
	if err != nil {
		return nil, fmt.Errorf("get events: %w", err)
	}
	defer rows.Close()

	var out []chatevents.Event
	for rows.Next() {
		var ev chatevents.Event
		var payload string
		var typ string
		if err := rows.Scan(&ev.ProfileID, &ev.ConversationID, &ev.TurnID, &ev.Seq, &ev.At, &ev.Version, &typ, &payload); err != nil {
			return nil, fmt.Errorf("scan event: %w", err)
		}
		ev.Type = chatevents.EventType(typ)
		ev.Payload = []byte(payload)
		out = append(out, ev)
	}
	return out, rows.Err()
}

// FinalizeTurn transitions a turn from active to a terminal status and
// inserts its turn.finished event in one transaction, compare-and-set from
// "active" so a duplicate finalize (e.g. a race between EOF handling and a
// stale Stop) is a harmless no-op rather than a double terminal event —
// "exactly once after supervisor completion". Returns alreadyFinalized=true
// when the compare-and-set found the turn already in a terminal state.
// FinalizeTurn returns the committed turn.finished event so the caller (the
// GUI supervisor) can emit the identical event live after this call commits
// — "commit before emit" — without a second read. ev is the zero value when
// alreadyFinalized is true (nothing was written this call) or err != nil.
func (s *AIStore) FinalizeTurn(profileID, conversationID, turnID string, status chatevents.TurnStatus, reason string, exitCode *int, historySaved bool) (ev chatevents.Event, alreadyFinalized bool, err error) {
	if profileID == "" {
		profileID = "default"
	}
	tx, err := s.db.Begin()
	if err != nil {
		return chatevents.Event{}, false, fmt.Errorf("begin finalize turn: %w", err)
	}
	defer tx.Rollback()

	res, err := tx.Exec(
		`UPDATE ai_chat_turns SET status = ?, reason = ?, updated_at = ? WHERE id = ? AND profile_id = ? AND status = ?`,
		string(status), reason, nowRFC3339(), turnID, profileID, turnStatusActive,
	)
	if err != nil {
		return chatevents.Event{}, false, fmt.Errorf("compare-and-set turn status: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return chatevents.Event{}, true, tx.Commit() // already terminal; nothing to do, not an error
	}

	ev, err = appendEventTx(tx, profileID, conversationID, turnID, chatevents.EventTurnFinished, chatevents.TurnFinishedPayload{
		Status:       status,
		Reason:       reason,
		ExitCode:     exitCode,
		HistorySaved: historySaved,
	}, time.Now())
	if err != nil {
		return chatevents.Event{}, false, err
	}
	if _, err := tx.Exec(
		`UPDATE ai_chat_turns SET last_committed_seq = ? WHERE id = ? AND profile_id = ?`,
		ev.Seq, turnID, profileID,
	); err != nil {
		return chatevents.Event{}, false, fmt.Errorf("advance last_committed_seq on finalize: %w", err)
	}
	return ev, false, tx.Commit()
}
