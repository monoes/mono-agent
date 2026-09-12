// Package chatevents defines the GUI chat journal's event contract — pure
// types, envelope construction, and (in normalize.go) protocol-to-event
// normalization. No Wails imports, no database connection, no dependency on
// the parent ai package: it is a leaf package both internal/ai (the store)
// and wails-app (the supervisor) import without a cycle.
//
// See docs/mastermind/plans/2026-09-11-interactive-agent-chat.md §"Event
// contract" for the authoritative field-by-field spec this file implements.
package chatevents

import (
	"encoding/json"
	"fmt"
	"time"
)

// Version is the event envelope's schema version (the "version" field on
// every Event). Bump only on a breaking payload-shape change.
const Version = 1

// EventType names one of the fixed set of GUI chat journal event types.
type EventType string

const (
	EventTurnStarted    EventType = "turn.started"
	EventSessionBound   EventType = "session.bound"
	EventAssistantDelta EventType = "assistant.delta"
	EventToolStarted    EventType = "tool.started"
	EventToolCompleted  EventType = "tool.completed"
	EventUsageUpdated   EventType = "usage.updated"
	EventNotice         EventType = "notice"
	EventTurnFinished   EventType = "turn.finished"
)

// Event is one journaled/emitted line — the outer envelope. Payload is kept
// as raw JSON so the envelope itself never needs to know every payload
// shape; callers marshal/unmarshal it via the typed Payload* structs below,
// keyed by Type.
type Event struct {
	Version        int             `json:"version"`
	ProfileID      string          `json:"profileId"`
	ConversationID string          `json:"conversationId"`
	TurnID         string          `json:"turnId"`
	Seq            int64           `json:"seq"`
	At             string          `json:"at"` // RFC3339 with millisecond precision, UTC
	Type           EventType       `json:"type"`
	Payload        json.RawMessage `json:"payload"`
}

// FormatAt renders t as the millisecond-precision UTC timestamp the event
// contract uses for "at" (e.g. "2026-09-11T09:00:00.000Z").
func FormatAt(t time.Time) string {
	return t.UTC().Format("2006-01-02T15:04:05.000Z07:00")
}

// MaxSafeSeq is the largest Seq value guaranteed to round-trip exactly
// through JSON into a JavaScript Number (2^53-1, Number.MAX_SAFE_INTEGER).
// The Wails bridge serializes Seq as a plain JSON number and the frontend
// reducer does numeric seq comparisons on whatever JSON.parse hands back, so
// a larger value risks silent precision loss. Real per-turn seq values are
// small monotonic counters far below this; MaxSafeSeq exists as a sentinel
// for an event that was never allocated a real seq by the store (e.g.
// wails-app/app_chat.go's finalize, when the FinalizeTurn write itself
// fails) but must still compare as newer than anything the frontend has
// already applied for its turn.
const MaxSafeSeq int64 = (1 << 53) - 1

// New builds a complete Event by marshaling payload into the envelope. seq
// and at are supplied by the caller (the store allocates seq transactionally;
// see internal/ai/chat_events.go) rather than computed here, since this
// package has no database connection and must stay pure/deterministic.
func New(profileID, conversationID, turnID string, seq int64, at time.Time, typ EventType, payload any) (Event, error) {
	b, err := json.Marshal(payload)
	if err != nil {
		return Event{}, fmt.Errorf("chatevents: marshal %s payload: %w", typ, err)
	}
	return Event{
		Version:        Version,
		ProfileID:      profileID,
		ConversationID: conversationID,
		TurnID:         turnID,
		Seq:            seq,
		At:             FormatAt(at),
		Type:           typ,
		Payload:        b,
	}, nil
}

// ── Payload types, one per EventType ────────────────────────────────────

// TurnStartedPayload is turn.started's payload — sequence 1 for a turn,
// emitted before the runtime/provider process is even launched, so a crash
// before any other event still leaves a record of what was asked.
type TurnStartedPayload struct {
	Backend  string `json:"backend"` // "agent" | "provider"
	Runtime  string `json:"runtime,omitempty"`
	Provider string `json:"provider,omitempty"`
	Model    string `json:"model,omitempty"`
	Text     string `json:"text"` // the accepted user message
}

// SessionBoundPayload is session.bound's payload — the runtime-assigned
// resumable session id, used only for that runtime's own --resume. Absence
// of this event for a conversation must not prevent it from appearing in
// history; a session binds only once the runtime actually reports one.
type SessionBoundPayload struct {
	Runtime   string `json:"runtime"`
	SessionID string `json:"sessionId"`
}

// AssistantDeltaPayload is assistant.delta's payload. PartID is a locally
// assigned identifier for one contiguous run of assistant prose — adjacent
// deltas sharing a PartID join into one growing text block; a tool-start
// event closes the current part and any further assistant text opens a new
// PartID, so text/tool ordering survives even though tool events flow on a
// separate lane.
type AssistantDeltaPayload struct {
	PartID string `json:"partId"`
	Text   string `json:"text"`
}

// ToolStartedPayload is tool.started's payload — one call, appended once as
// a new timeline step. CallID (not name, not array position) is the only
// stable handle a later tool.completed uses to update this same step:
// "Tool identity is (turnId,callId), never array position or name."
type ToolStartedPayload struct {
	CallID    string          `json:"callId"`
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments,omitempty"`
}

// ToolCompletedPayload is tool.completed's payload. OK is nullable (some
// adapters never report explicit success/failure); Result is always
// present once this event fires — an empty string is a valid result, not a
// missing one, so it is a plain string rather than a pointer.
type ToolCompletedPayload struct {
	CallID string `json:"callId"`
	OK     *bool  `json:"ok"`
	Result string `json:"result"`
}

// UsageUpdatedPayload is usage.updated's payload. Every metric is nullable
// and independently optional — "missing cost means unavailable, not $0" —
// so each is a pointer, never defaulted to zero. Source names which
// underlying protocol event reported this snapshot (e.g. "assistant" |
// "result"), since usage semantics differ by source and must not be summed
// across sources without verified delta semantics.
type UsageUpdatedPayload struct {
	InputTokens  *int64   `json:"inputTokens"`
	OutputTokens *int64   `json:"outputTokens"`
	CostUSD      *float64 `json:"costUsd"`
	Source       string   `json:"source"`
}

// NoticeSeverity classifies a notice event for display — a warning/info
// notice must never erase already-observed work the way a fatal error does.
type NoticeSeverity string

const (
	SeverityInfo    NoticeSeverity = "info"
	SeverityWarning NoticeSeverity = "warning"
	SeverityError   NoticeSeverity = "error"
)

// NoticePayload is notice's payload — a nonfatal condition worth surfacing
// (a malformed tool-call fence, a stale-stop, a persistence warning) without
// terminating the turn.
type NoticePayload struct {
	Code     string         `json:"code"`
	Message  string         `json:"message"`
	Severity NoticeSeverity `json:"severity"`
}

// TurnStatus is turn.finished's terminal classification — see the
// supervisor's precedence rules (Stop > fatal error > missing terminal
// evidence > completed) in wails-app/app_chat.go.
type TurnStatus string

const (
	StatusCompleted   TurnStatus = "completed"
	StatusFailed      TurnStatus = "failed"
	StatusCancelled   TurnStatus = "cancelled"
	StatusInterrupted TurnStatus = "interrupted"
)

// TurnFinishedPayload is turn.finished's payload — emitted exactly once per
// turn, after the supervisor has fully determined the outcome. ExitCode is
// nullable since a provider-backend turn has no process exit code at all.
// HistorySaved false means the live transcript is authoritative for this
// turn but durable persistence fell behind (see chat_events.go's
// last_committed_seq / historySaved semantics).
type TurnFinishedPayload struct {
	Status       TurnStatus `json:"status"`
	Reason       string     `json:"reason,omitempty"`
	ExitCode     *int       `json:"exitCode"`
	HistorySaved bool       `json:"historySaved"`
}
