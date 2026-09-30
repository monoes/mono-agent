package ai

import (
	"context"
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

// ErrTurnOwnedByOtherInstance is returned by CreateTurn when the target
// conversation already has an active turn owned by a DIFFERENT, identified
// live app instance. Two GUI processes sharing one ~/.monoagent database
// must not each admit and run a turn against the same conversation —
// whichever finished last would otherwise silently overwrite the other's
// --resume session binding via BindConversationSession. See
// docs/mastermind/plans/2026-09-12-interactive-agent-chat-followups.md,
// "OwnerInstanceID is written and read back but never compared to anything".
var ErrTurnOwnedByOtherInstance = errors.New("chat: conversation has an active turn owned by another instance")

// Conversation is one server-created app-level chat conversation — see the
// plan's "three distinct identifiers" section. HistoryKey is the opaque
// provider model-context bucket; it is deliberately excluded from JSON so
// it never reaches the frontend or a Wails response.
type Conversation struct {
	ID              string `json:"id"`
	ProfileID       string `json:"profileId"`
	Backend         string `json:"backend"` // "agent", or "provider" for a read-only legacy conversation
	WorkflowContext string `json:"workflowContext"`
	RuntimeID       string `json:"runtimeId,omitempty"`
	ProviderID      string `json:"providerId,omitempty"`
	Model           string `json:"model,omitempty"`
	Effort          string `json:"effort,omitempty"`
	SessionID       string `json:"sessionId,omitempty"`
	// Mode is "assistant" or "coder". A coder conversation runs every turn
	// with full access inside Cwd, fixed at creation: Claude Code keys its
	// sessions by folder, so resuming needs the same one.
	Mode string `json:"mode"`
	Cwd  string `json:"cwd"`
	// OrgMode is a coder conversation's org: "solo" (the agent works
	// alone) or "dynamic" (it can spawn workers, #226).
	OrgMode    string `json:"orgMode,omitempty"`
	HistoryKey string `json:"-"`
	CreatedAt  string `json:"createdAt"`
	UpdatedAt  string `json:"updatedAt"`
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
	if err := s.ensureAnswersTable(); err != nil {
		return err
	}
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
	// conversation_id carries a real foreign key back to
	// ai_chat_conversations(id) ON DELETE CASCADE (added, for an existing
	// database, by data/migrations/040_ai_chat_conversation_foreign_keys.sql
	// via the standard SQLite rebuild recipe — SQLite cannot ALTER TABLE ADD
	// FOREIGN KEY). It is declared here too so a caller that constructs
	// AIStore directly against a database that never ran ApplyMigrations
	// still gets the constraint on a genuinely fresh CREATE TABLE; on any
	// database that DID run migration 040 first (every real CLI/GUI startup
	// path), this CREATE TABLE IF NOT EXISTS is a no-op and the migration's
	// shape is what's actually in effect. See
	// docs/mastermind/plans/2026-09-12-interactive-agent-chat-followups.md,
	// "DeleteConversation isn't atomic against a concurrent StartChatTurn".
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
		updated_at TEXT NOT NULL,
		FOREIGN KEY (conversation_id) REFERENCES ai_chat_conversations(id) ON DELETE CASCADE
	)`
	// Composite primary key doubles as the unique (profile_id,
	// conversation_id, turn_id, seq) constraint the plan requires, and gives
	// an efficient natural index for "events after seq N" catch-up queries.
	// conversation_id and turn_id each carry a real foreign key (see turnsSQL
	// above for why they are declared here as well as in migration 040) —
	// turn_id's target, ai_chat_turns, is safe to require because every
	// AppendEvent call in this codebase (cmd/monoagentcli's turnJournal, the
	// only production writer) is already structurally
	// downstream of a successful CreateTurn for that same turn ID.
	const eventsSQL = `CREATE TABLE IF NOT EXISTS ai_chat_events (
		profile_id TEXT NOT NULL,
		conversation_id TEXT NOT NULL,
		turn_id TEXT NOT NULL,
		seq INTEGER NOT NULL,
		created_at TEXT NOT NULL,
		version INTEGER NOT NULL,
		type TEXT NOT NULL,
		payload TEXT NOT NULL,
		PRIMARY KEY (profile_id, conversation_id, turn_id, seq),
		FOREIGN KEY (conversation_id) REFERENCES ai_chat_conversations(id) ON DELETE CASCADE,
		FOREIGN KEY (turn_id) REFERENCES ai_chat_turns(id) ON DELETE CASCADE
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
	for _, alter := range []string{
		`ALTER TABLE ai_chat_conversations ADD COLUMN mode TEXT NOT NULL DEFAULT 'assistant'`,
		`ALTER TABLE ai_chat_conversations ADD COLUMN cwd TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE ai_chat_conversations ADD COLUMN effort TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE ai_chat_conversations ADD COLUMN org_mode TEXT NOT NULL DEFAULT 'solo'`,
	} {
		if err := addColumnIfMissing(s.db, alter); err != nil {
			return err
		}
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
	return s.CreateConversationModeEffort(profileID, backend, workflowContext, runtimeID, providerID, model, ModeAssistant, "", "")
}

// CreateConversationEffort is CreateConversation with an explicit effort level.
func (s *AIStore) CreateConversationEffort(profileID, backend, workflowContext, runtimeID, providerID, model, effort string) (Conversation, error) {
	return s.CreateConversationModeEffort(profileID, backend, workflowContext, runtimeID, providerID, model, ModeAssistant, "", effort)
}

// Conversation modes.
const (
	ModeAssistant = "assistant"
	ModeCoder     = "coder"
)

// CreateConversationMode is CreateConversation with an explicit mode and
// working folder (coder conversations need one).
func (s *AIStore) CreateConversationMode(profileID, backend, workflowContext, runtimeID, providerID, model, mode, cwd string) (Conversation, error) {
	return s.CreateConversationModeEffort(profileID, backend, workflowContext, runtimeID, providerID, model, mode, cwd, "")
}

// CreateConversationModeEffort is CreateConversationMode with an explicit effort level.
func (s *AIStore) CreateConversationModeEffort(profileID, backend, workflowContext, runtimeID, providerID, model, mode, cwd, effort string) (Conversation, error) {
	switch mode {
	case ModeAssistant:
		cwd = ""
	case ModeCoder:
		if cwd == "" {
			return Conversation{}, fmt.Errorf("create conversation: coder mode needs a folder")
		}
	default:
		return Conversation{}, fmt.Errorf("create conversation: unknown mode %q", mode)
	}
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
		Effort:          effort,
		Mode:            mode,
		Cwd:             cwd,
		OrgMode:         OrgModeSolo,
		HistoryKey:      uuid.NewString(),
		CreatedAt:       now,
		UpdatedAt:       now,
	}
	const q = `INSERT INTO ai_chat_conversations
		(id, profile_id, backend, workflow_context, runtime_id, provider_id, model, session_id, mode, cwd, history_key, created_at, updated_at, effort)
		VALUES (?, ?, ?, ?, ?, ?, ?, '', ?, ?, ?, ?, ?, ?)`
	if _, err := s.db.Exec(q, c.ID, c.ProfileID, c.Backend, c.WorkflowContext, c.RuntimeID, c.ProviderID, c.Model, c.Mode, c.Cwd, c.HistoryKey, c.CreatedAt, c.UpdatedAt, c.Effort); err != nil {
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
	const q = `SELECT id, profile_id, backend, workflow_context, runtime_id, provider_id, model, session_id, mode, cwd, history_key, created_at, updated_at, effort, org_mode
		FROM ai_chat_conversations WHERE id = ? AND profile_id = ?`
	var c Conversation
	err := s.db.QueryRow(q, id, profileID).Scan(
		&c.ID, &c.ProfileID, &c.Backend, &c.WorkflowContext, &c.RuntimeID, &c.ProviderID, &c.Model, &c.SessionID, &c.Mode, &c.Cwd, &c.HistoryKey, &c.CreatedAt, &c.UpdatedAt, &c.Effort, &c.OrgMode,
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
	q := `SELECT id, profile_id, backend, workflow_context, runtime_id, provider_id, model, session_id, mode, cwd, history_key, created_at, updated_at, effort, org_mode
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
		if err := rows.Scan(&c.ID, &c.ProfileID, &c.Backend, &c.WorkflowContext, &c.RuntimeID, &c.ProviderID, &c.Model, &c.SessionID, &c.Mode, &c.Cwd, &c.HistoryKey, &c.CreatedAt, &c.UpdatedAt, &c.Effort, &c.OrgMode); err != nil {
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

// Org modes of a coder conversation.
const (
	OrgModeSolo    = "solo"
	OrgModeDynamic = "dynamic"
)

// SetConversationOrgMode switches a conversation between the solo and the
// dynamic org; it takes effect from the next turn.
func (s *AIStore) SetConversationOrgMode(id, profileID, mode string) error {
	if mode != OrgModeSolo && mode != OrgModeDynamic {
		return fmt.Errorf("org mode must be %q or %q, got %q", OrgModeSolo, OrgModeDynamic, mode)
	}
	if profileID == "" {
		profileID = "default"
	}
	res, err := s.db.Exec(`UPDATE ai_chat_conversations SET org_mode = ?, updated_at = ? WHERE id = ? AND profile_id = ?`,
		mode, nowRFC3339(), id, profileID)
	if err != nil {
		return fmt.Errorf("set org mode: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrConversationNotFound
	}
	return nil
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
//
// The existence check, the active-turn check, and the delete itself all run
// inside ONE BEGIN IMMEDIATE transaction — see internal/vault/vault.go's
// Register for why IMMEDIATE, not database/sql's default DEFERRED, is
// required: a DEFERRED transaction only acquires SQLite's write lock lazily,
// at its first write, so the previous plain "check via one statement, delete
// via a later separate transaction" shape left an unprotected window between
// the two where a concurrent CreateTurn's own INSERT could commit — admitting
// an active turn this check never saw — before the delete proceeded anyway.
// BEGIN IMMEDIATE acquires the write lock up front, before the active-turn
// SELECT even runs, so for the duration of this whole function no concurrent
// writer can commit a turn for this conversation without first waiting for
// this transaction to finish (subject to the connection's busy_timeout):
// either CreateTurn's insert lands (and commits) entirely before this
// transaction begins, in which case the active-turn check below sees it and
// refuses with ErrTurnActive, or it is forced to wait until after this
// transaction has committed or rolled back — there is no interleaving in
// between where the delete can proceed past a turn it failed to see.
//
// This closes the race for any CreateTurn attempt whose insert would
// otherwise land WHILE this function's own check-then-delete was in
// progress. It does not by itself stop a CreateTurn insert that starts only
// AFTER this transaction has already committed and released the lock, from
// an admission that began racing before the delete decided anything (see the
// followups doc's "admitted in-memory but not yet DB-committed" framing) —
// that residual gap is why ai_chat_turns.conversation_id and
// ai_chat_events.conversation_id/turn_id also gained real foreign keys back
// to ai_chat_conversations (data/migrations/040_ai_chat_conversation_foreign_keys.sql):
// a CreateTurn/AppendEvent that reaches the database only after the
// conversation is already gone now fails outright instead of silently
// writing an orphan. See chat_events_test.go's
// TestChatEvents_DeleteConversationRaceAgainstConcurrentCreateTurn, which
// exercises both layers together.
func (s *AIStore) DeleteConversation(id, profileID string) error {
	if profileID == "" {
		profileID = "default"
	}
	ctx := context.Background()

	conn, err := s.db.Conn(ctx)
	if err != nil {
		return fmt.Errorf("delete conversation: get conn: %w", err)
	}
	defer conn.Close()

	if _, err := conn.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		return fmt.Errorf("begin delete conversation: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_, _ = conn.ExecContext(context.Background(), "ROLLBACK")
		}
	}()

	var exists int
	if err := conn.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM ai_chat_conversations WHERE id = ? AND profile_id = ?`,
		id, profileID,
	).Scan(&exists); err != nil {
		return fmt.Errorf("check conversation exists: %w", err)
	}
	if exists == 0 {
		return ErrConversationNotFound
	}

	var activeCount int
	if err := conn.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM ai_chat_turns WHERE conversation_id = ? AND profile_id = ? AND status = ?`,
		id, profileID, turnStatusActive,
	).Scan(&activeCount); err != nil {
		return fmt.Errorf("check active turns: %w", err)
	}
	if activeCount > 0 {
		return ErrTurnActive
	}

	if _, err := conn.ExecContext(ctx, `DELETE FROM ai_chat_events WHERE conversation_id = ? AND profile_id = ?`, id, profileID); err != nil {
		return fmt.Errorf("delete events: %w", err)
	}
	if _, err := conn.ExecContext(ctx, `DELETE FROM ai_chat_turns WHERE conversation_id = ? AND profile_id = ?`, id, profileID); err != nil {
		return fmt.Errorf("delete turns: %w", err)
	}
	if _, err := conn.ExecContext(ctx, `DELETE FROM ai_chat_conversations WHERE id = ? AND profile_id = ?`, id, profileID); err != nil {
		return fmt.Errorf("delete conversation: %w", err)
	}
	if _, err := conn.ExecContext(ctx, "COMMIT"); err != nil {
		return fmt.Errorf("commit delete conversation: %w", err)
	}
	committed = true
	return nil
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

	// Refuse a NEW turn when the conversation already has an active turn
	// owned by a different, identified live instance (see
	// ErrTurnOwnedByOtherInstance) — two live app instances sharing this
	// database must not both admit a turn on the same conversation. An
	// empty stored OwnerInstanceID (e.g. a row from before this check
	// existed) or a match against this same requesting instance is not
	// treated as foreign.
	//
	// This is a plain read-then-decide check, not wrapped in the same
	// transaction as the INSERT below: like AppendEvent's documented
	// sequence-allocation race (this same file, above), two near-
	// simultaneous CreateTurn calls from two different instances could both
	// pass this check before either inserts, admitting two active turns for
	// one conversation. That residual window is accepted here for the same
	// reason it already is there and in DeleteConversation's own,
	// similarly non-atomic active-turn check: a real fix needs a DB-level
	// constraint (e.g. a partial unique index on active turns per
	// conversation), which was deliberately NOT added — retrofitting one
	// against an already-installed database that (pre-fix) may already
	// have two active rows for one conversation would fail migration
	// outright rather than degrade gracefully. Two truly simultaneous live
	// instances racing this exact window is a narrower trigger still than
	// the already-accepted risk this closes (two live instances existing
	// at all).
	if active, getErr := s.activeTurnForConversation(conversationID, profileID); getErr == nil {
		if active.OwnerInstanceID != "" && active.OwnerInstanceID != ownerInstanceID {
			return Turn{}, false, ErrTurnOwnedByOtherInstance
		}
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

// activeTurnForConversation returns conversationID's currently active turn,
// if any — CreateTurn's admission-check helper (see
// ErrTurnOwnedByOtherInstance). Returns ErrTurnNotFound when none is
// active. Deliberately unexported: it exists to answer "is this
// conversation currently claimed by a turn?", not as a general-purpose
// query — GetChatTurns/GetChatEvents callers use ListTurns/GetTurn as they
// already do. Under the normal invariant this check itself enforces, at
// most one row can be active per conversation; if that invariant were ever
// violated (e.g. by data predating this check), LIMIT 1 with the newest
// row deterministically picks one rather than erroring.
func (s *AIStore) activeTurnForConversation(conversationID, profileID string) (Turn, error) {
	if profileID == "" {
		profileID = "default"
	}
	const q = `SELECT id, conversation_id, profile_id, owner_instance_id, prompt, status, reason, last_committed_seq, created_at, updated_at
		FROM ai_chat_turns WHERE conversation_id = ? AND profile_id = ? AND status = ? ORDER BY created_at DESC, id DESC LIMIT 1`
	var t Turn
	err := s.db.QueryRow(q, conversationID, profileID, turnStatusActive).Scan(
		&t.ID, &t.ConversationID, &t.ProfileID, &t.OwnerInstanceID, &t.Prompt, &t.Status, &t.Reason, &t.LastCommittedSeq, &t.CreatedAt, &t.UpdatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return Turn{}, ErrTurnNotFound
	}
	if err != nil {
		return Turn{}, fmt.Errorf("get active turn for conversation: %w", err)
	}
	return t, nil
}

// QueryActiveTurns returns every turn across every profile and conversation
// currently left in "active" status — the startup reconciliation query
// (ReconcileActiveTurns, behind `chat history reconcile`), deliberately unscoped by profile
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
// the turn's journal (cmd/monoagentcli) serializes writes per turn (single
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
// turn's journal) can print the identical event live after this call commits
// — "commit before emit" — without a second read. ev is the zero value when
// alreadyFinalized is true (nothing was written this call) or err != nil.
func (s *AIStore) FinalizeTurn(profileID, conversationID, turnID string, status chatevents.TurnStatus, reason string, exitCode *int, historySaved bool) (ev chatevents.Event, alreadyFinalized bool, err error) {
	return s.FinalizeTurnCode(profileID, conversationID, turnID, status, reason, "", exitCode, historySaved, "")
}

// FinalizeTurnCode is FinalizeTurn with a failure code and the turn's
// sandbox for the turn.finished payload (chatevents.TurnFinishedPayload's
// Code and Sandbox).
func (s *AIStore) FinalizeTurnCode(profileID, conversationID, turnID string, status chatevents.TurnStatus, reason, code string, exitCode *int, historySaved bool, sandbox string) (ev chatevents.Event, alreadyFinalized bool, err error) {
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
		Code:         code,
		ExitCode:     exitCode,
		HistorySaved: historySaved,
		Sandbox:      sandbox,
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

// ReconciledReason is the reason ReconcileActiveTurns records on a turn it
// marks interrupted.
const ReconciledReason = "interrupted at backend startup (previous app instance)"

// ReconcileActiveTurns marks every turn still "active", across every
// profile, as interrupted — the sweep a starting app runs over turns a
// previous instance left unfinished. A turn owned by exceptOwner (the
// starting instance itself, which may already have admitted a turn by the
// time the sweep runs) is skipped; an empty exceptOwner skips nothing.
// It cannot tell a dead previous instance from a second live one, since no
// liveness signal exists. Per-row failures are collected, not fatal.
func (s *AIStore) ReconcileActiveTurns(exceptOwner string) (reconciled []Turn, errs []error) {
	rows, err := s.QueryActiveTurns()
	if err != nil {
		return nil, []error{fmt.Errorf("list active turns: %w", err)}
	}
	reconciled = []Turn{}
	for _, row := range rows {
		if exceptOwner != "" && row.OwnerInstanceID == exceptOwner {
			continue
		}
		_, already, err := s.FinalizeTurn(row.ProfileID, row.ConversationID, row.ID, chatevents.StatusInterrupted, ReconciledReason, nil, true)
		if err != nil {
			errs = append(errs, fmt.Errorf("reconcile turn %s: %w", row.ID, err))
			continue
		}
		if !already {
			row.Status = string(chatevents.StatusInterrupted)
			row.Reason = ReconciledReason
			reconciled = append(reconciled, row)
		}
	}
	return reconciled, errs
}

// ConversationRecord is a Conversation as `monoagentcli chat history`
// prints it: snake_case keys, and no history key.
type ConversationRecord struct {
	ID              string `json:"id"`
	ProfileID       string `json:"profile_id"`
	Backend         string `json:"backend"`
	WorkflowContext string `json:"workflow_context"`
	RuntimeID       string `json:"runtime_id"`
	ProviderID      string `json:"provider_id"`
	Model           string `json:"model"`
	Effort          string `json:"effort,omitempty"`
	SessionID       string `json:"session_id"`
	Mode            string `json:"mode"`
	Cwd             string `json:"cwd"`
	OrgMode         string `json:"org_mode,omitempty"`
	CreatedAt       string `json:"created_at"`
	UpdatedAt       string `json:"updated_at"`
}

// Record converts c to its CLI shape.
func (c Conversation) Record() ConversationRecord {
	return ConversationRecord{
		ID: c.ID, ProfileID: c.ProfileID, Backend: c.Backend, WorkflowContext: c.WorkflowContext,
		RuntimeID: c.RuntimeID, ProviderID: c.ProviderID, Model: c.Model, Effort: c.Effort, SessionID: c.SessionID,
		Mode: c.Mode, Cwd: c.Cwd, OrgMode: c.OrgMode, CreatedAt: c.CreatedAt, UpdatedAt: c.UpdatedAt,
	}
}

// Conversation converts a CLI record back (HistoryKey stays empty).
func (r ConversationRecord) Conversation() Conversation {
	return Conversation{
		ID: r.ID, ProfileID: r.ProfileID, Backend: r.Backend, WorkflowContext: r.WorkflowContext,
		RuntimeID: r.RuntimeID, ProviderID: r.ProviderID, Model: r.Model, Effort: r.Effort, SessionID: r.SessionID,
		Mode: r.Mode, Cwd: r.Cwd, OrgMode: r.OrgMode, CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt,
	}
}

// TurnRecord is a Turn as the CLI prints it. Unlike Turn's own JSON it
// carries the owner instance id, so the app can tell its own active turns
// from another window's.
type TurnRecord struct {
	ID               string `json:"id"`
	ConversationID   string `json:"conversation_id"`
	ProfileID        string `json:"profile_id"`
	OwnerInstanceID  string `json:"owner_instance_id"`
	Prompt           string `json:"prompt"`
	Status           string `json:"status"`
	Reason           string `json:"reason"`
	LastCommittedSeq int64  `json:"last_committed_seq"`
	CreatedAt        string `json:"created_at"`
	UpdatedAt        string `json:"updated_at"`
}

// Record converts t to its CLI shape.
func (t Turn) Record() TurnRecord {
	return TurnRecord{
		ID: t.ID, ConversationID: t.ConversationID, ProfileID: t.ProfileID, OwnerInstanceID: t.OwnerInstanceID,
		Prompt: t.Prompt, Status: t.Status, Reason: t.Reason, LastCommittedSeq: t.LastCommittedSeq,
		CreatedAt: t.CreatedAt, UpdatedAt: t.UpdatedAt,
	}
}

// Turn converts a CLI record back.
func (r TurnRecord) Turn() Turn {
	return Turn{
		ID: r.ID, ConversationID: r.ConversationID, ProfileID: r.ProfileID, OwnerInstanceID: r.OwnerInstanceID,
		Prompt: r.Prompt, Status: r.Status, Reason: r.Reason, LastCommittedSeq: r.LastCommittedSeq,
		CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt,
	}
}
