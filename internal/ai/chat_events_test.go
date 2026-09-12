package ai

import (
	"database/sql"
	"testing"

	"github.com/zalando/go-keyring"

	"github.com/monoes/mono-agent/internal/ai/chatevents"
	"github.com/monoes/mono-agent/internal/storage"
)

func newChatEventStore(t *testing.T) (*AIStore, *sql.DB) {
	t.Helper()
	db := openTestDB(t)
	s, err := NewAIStore(db)
	if err != nil {
		t.Fatalf("NewAIStore: %v", err)
	}
	return s, db
}

func TestChatEvents_TwoProfilesDoNotSeeEachOthersGeneralConversation(t *testing.T) {
	s, _ := newChatEventStore(t)
	c1, err := s.CreateConversation("profile-a", "agent", "general", "claude", "", "")
	if err != nil {
		t.Fatalf("CreateConversation(a): %v", err)
	}
	c2, err := s.CreateConversation("profile-b", "agent", "general", "claude", "", "")
	if err != nil {
		t.Fatalf("CreateConversation(b): %v", err)
	}
	if c1.ID == c2.ID {
		t.Fatal("two profiles' general conversations collided on ID")
	}
	if _, err := s.GetConversation(c1.ID, "profile-b"); err != ErrConversationNotFound {
		t.Errorf("profile-b reading profile-a's conversation: err = %v, want ErrConversationNotFound", err)
	}
	listA, _, err := s.ListConversations("profile-a", "", 10)
	if err != nil {
		t.Fatalf("ListConversations(a): %v", err)
	}
	if len(listA) != 1 || listA[0].ID != c1.ID {
		t.Errorf("profile-a's list = %+v, want exactly [c1]", listA)
	}
}

func TestChatEvents_CreateTurnIsIdempotentByID(t *testing.T) {
	s, _ := newChatEventStore(t)
	conv, _ := s.CreateConversation("p1", "agent", "general", "claude", "", "")

	t1, existed1, err := s.CreateTurn(conv.ID, "p1", "turn-1", "instance-a", "hello")
	if err != nil || existed1 {
		t.Fatalf("first CreateTurn: turn=%+v existed=%v err=%v", t1, existed1, err)
	}
	t2, existed2, err := s.CreateTurn(conv.ID, "p1", "turn-1", "instance-a", "hello again — ignored")
	if err != nil {
		t.Fatalf("duplicate CreateTurn: %v", err)
	}
	if !existed2 {
		t.Error("duplicate Start with the same turn ID must report it already existed")
	}
	if t2.Prompt != "hello" {
		t.Errorf("duplicate Start must never overwrite the original admission: Prompt = %q, want %q", t2.Prompt, "hello")
	}

	var count int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM ai_chat_turns WHERE id = ?`, "turn-1").Scan(&count); err != nil {
		t.Fatalf("count turns: %v", err)
	}
	if count != 1 {
		t.Errorf("duplicate Start created %d rows, want exactly 1 (never a second process/row)", count)
	}
}

func TestChatEvents_FinalizeTurnCompareAndSetIsExactlyOnce(t *testing.T) {
	s, _ := newChatEventStore(t)
	conv, _ := s.CreateConversation("p1", "agent", "general", "claude", "", "")
	turn, _, _ := s.CreateTurn(conv.ID, "p1", "turn-1", "instance-a", "hello")

	ev1, already1, err := s.FinalizeTurn("p1", conv.ID, turn.ID, chatevents.StatusCompleted, "", nil, true)
	if err != nil || already1 {
		t.Fatalf("first FinalizeTurn: already=%v err=%v", already1, err)
	}
	if ev1.Type != chatevents.EventTurnFinished {
		t.Errorf("first FinalizeTurn returned event type %q, want %q", ev1.Type, chatevents.EventTurnFinished)
	}
	// A second, "duplicate terminal" finalize (e.g. a stale Stop racing EOF
	// handling) must be a harmless no-op, never a second turn.finished event
	// and never overwriting the first outcome.
	ev2, already2, err := s.FinalizeTurn("p1", conv.ID, turn.ID, chatevents.StatusCancelled, "stale stop", nil, true)
	if err != nil {
		t.Fatalf("second FinalizeTurn: %v", err)
	}
	if !already2 {
		t.Error("second FinalizeTurn on an already-terminal turn must report alreadyFinalized")
	}
	if ev2.Type != "" {
		t.Errorf("second (already-finalized) FinalizeTurn returned a non-zero event %+v, want the zero value (nothing written)", ev2)
	}

	got, err := s.GetTurn(turn.ID, "p1")
	if err != nil {
		t.Fatalf("GetTurn: %v", err)
	}
	if got.Status != string(chatevents.StatusCompleted) {
		t.Errorf("Status = %q, want the FIRST finalize's %q to stick", got.Status, chatevents.StatusCompleted)
	}

	events, err := s.GetEvents(conv.ID, turn.ID, "p1", 0, 100)
	if err != nil {
		t.Fatalf("GetEvents: %v", err)
	}
	finishedCount := 0
	for _, ev := range events {
		if ev.Type == chatevents.EventTurnFinished {
			finishedCount++
		}
	}
	if finishedCount != 1 {
		t.Errorf("turn.finished appeared %d times, want exactly once", finishedCount)
	}
}

func TestChatEvents_AppendEventAllocatesStrictlyIncreasingSequence(t *testing.T) {
	s, _ := newChatEventStore(t)
	conv, _ := s.CreateConversation("p1", "agent", "general", "claude", "", "")
	turn, _, _ := s.CreateTurn(conv.ID, "p1", "turn-1", "instance-a", "hello")

	var lastSeq int64
	for i := 0; i < 5; i++ {
		ev, err := s.AppendEvent("p1", conv.ID, turn.ID, chatevents.EventAssistantDelta, chatevents.AssistantDeltaPayload{PartID: "part-1", Text: "x"})
		if err != nil {
			t.Fatalf("AppendEvent #%d: %v", i, err)
		}
		if ev.Seq <= lastSeq {
			t.Fatalf("Seq #%d = %d, want strictly greater than previous %d", i, ev.Seq, lastSeq)
		}
		lastSeq = ev.Seq
	}
	got, err := s.GetTurn(turn.ID, "p1")
	if err != nil {
		t.Fatalf("GetTurn: %v", err)
	}
	if got.LastCommittedSeq != lastSeq {
		t.Errorf("LastCommittedSeq = %d, want %d (the last appended seq)", got.LastCommittedSeq, lastSeq)
	}
}

// Regression guard: sequence ordering must not depend on wall-clock
// timestamps distinguishing events — several appends can share the same
// RFC3339-second timestamp and must still come back in insertion order.
func TestChatEvents_EqualTimestampsPreserveSequenceOrder(t *testing.T) {
	s, _ := newChatEventStore(t)
	conv, _ := s.CreateConversation("p1", "agent", "general", "claude", "", "")
	turn, _, _ := s.CreateTurn(conv.ID, "p1", "turn-1", "instance-a", "hello")

	for i := 0; i < 3; i++ {
		if _, err := s.AppendEvent("p1", conv.ID, turn.ID, chatevents.EventAssistantDelta, chatevents.AssistantDeltaPayload{PartID: "p", Text: "x"}); err != nil {
			t.Fatalf("AppendEvent #%d: %v", i, err)
		}
	}
	events, err := s.GetEvents(conv.ID, turn.ID, "p1", 0, 100)
	if err != nil {
		t.Fatalf("GetEvents: %v", err)
	}
	if len(events) != 3 {
		t.Fatalf("got %d events, want 3", len(events))
	}
	for i := 1; i < len(events); i++ {
		if events[i].Seq <= events[i-1].Seq {
			t.Errorf("event %d Seq %d not greater than event %d Seq %d", i, events[i].Seq, i-1, events[i-1].Seq)
		}
	}
}

func TestChatEvents_ConversationWithNoSessionBoundIsStillUsable(t *testing.T) {
	s, _ := newChatEventStore(t)
	conv, err := s.CreateConversation("p1", "agent", "general", "claude", "", "")
	if err != nil {
		t.Fatalf("CreateConversation: %v", err)
	}
	if conv.SessionID != "" {
		t.Errorf("SessionID = %q, want empty until session.bound", conv.SessionID)
	}
	list, _, err := s.ListConversations("p1", "", 10)
	if err != nil {
		t.Fatalf("ListConversations: %v", err)
	}
	if len(list) != 1 {
		t.Errorf("a sessionless conversation must still appear in history: got %d entries", len(list))
	}
}

func TestChatEvents_DeleteConversationBlockedWhileTurnActive(t *testing.T) {
	s, _ := newChatEventStore(t)
	conv, _ := s.CreateConversation("p1", "agent", "general", "claude", "", "")
	s.CreateTurn(conv.ID, "p1", "turn-1", "instance-a", "hello")

	if err := s.DeleteConversation(conv.ID, "p1"); err != ErrTurnActive {
		t.Fatalf("DeleteConversation while active: err = %v, want ErrTurnActive", err)
	}
	if _, err := s.GetConversation(conv.ID, "p1"); err != nil {
		t.Errorf("conversation must survive a refused delete: %v", err)
	}
}

func TestChatEvents_DeleteConversationRemovesTurnsAndEventsTransactionally(t *testing.T) {
	s, _ := newChatEventStore(t)
	conv, _ := s.CreateConversation("p1", "agent", "general", "claude", "", "")
	turn, _, _ := s.CreateTurn(conv.ID, "p1", "turn-1", "instance-a", "hello")
	s.AppendEvent("p1", conv.ID, turn.ID, chatevents.EventAssistantDelta, chatevents.AssistantDeltaPayload{PartID: "p", Text: "x"})
	if _, _, err := s.FinalizeTurn("p1", conv.ID, turn.ID, chatevents.StatusCompleted, "", nil, true); err != nil {
		t.Fatalf("FinalizeTurn: %v", err)
	}

	if err := s.DeleteConversation(conv.ID, "p1"); err != nil {
		t.Fatalf("DeleteConversation: %v", err)
	}
	if _, err := s.GetConversation(conv.ID, "p1"); err != ErrConversationNotFound {
		t.Errorf("conversation row survived delete: err = %v", err)
	}
	if _, err := s.GetTurn(turn.ID, "p1"); err != ErrTurnNotFound {
		t.Errorf("turn row survived conversation delete: err = %v", err)
	}
	events, err := s.GetEvents(conv.ID, turn.ID, "p1", 0, 100)
	if err != nil {
		t.Fatalf("GetEvents after delete: %v", err)
	}
	if len(events) != 0 {
		t.Errorf("got %d events after conversation delete, want 0", len(events))
	}
}

// "a reopened temporary database returns exactly the committed event
// sequence" — the store's Gate criterion, verified directly: close the DB,
// reopen the same file, confirm nothing committed was lost or duplicated.
func TestChatEvents_ReopenedDatabaseReturnsExactCommittedSequence(t *testing.T) {
	keyring.MockInit()
	path := t.TempDir() + "/reopen-test.db"

	db1, err := storage.NewDatabase(path)
	if err != nil {
		t.Fatalf("NewDatabase: %v", err)
	}
	if err := db1.ApplyMigrations(); err != nil {
		t.Fatalf("ApplyMigrations: %v", err)
	}
	s1, err := NewAIStore(db1.DB)
	if err != nil {
		t.Fatalf("NewAIStore: %v", err)
	}
	conv, err := s1.CreateConversation("p1", "agent", "general", "claude", "", "")
	if err != nil {
		t.Fatalf("CreateConversation: %v", err)
	}
	turn, _, err := s1.CreateTurn(conv.ID, "p1", "turn-1", "instance-a", "hello")
	if err != nil {
		t.Fatalf("CreateTurn: %v", err)
	}
	var wantSeqs []int64
	for i := 0; i < 4; i++ {
		ev, err := s1.AppendEvent("p1", conv.ID, turn.ID, chatevents.EventAssistantDelta, chatevents.AssistantDeltaPayload{PartID: "p", Text: "x"})
		if err != nil {
			t.Fatalf("AppendEvent #%d: %v", i, err)
		}
		wantSeqs = append(wantSeqs, ev.Seq)
	}
	if _, _, err := s1.FinalizeTurn("p1", conv.ID, turn.ID, chatevents.StatusCompleted, "", nil, true); err != nil {
		t.Fatalf("FinalizeTurn: %v", err)
	}
	if err := db1.DB.Close(); err != nil {
		t.Fatalf("close db1: %v", err)
	}

	db2, err := storage.NewDatabase(path)
	if err != nil {
		t.Fatalf("reopen NewDatabase: %v", err)
	}
	t.Cleanup(func() { db2.DB.Close() })
	if err := db2.ApplyMigrations(); err != nil {
		t.Fatalf("reopen ApplyMigrations: %v", err)
	}
	s2, err := NewAIStore(db2.DB)
	if err != nil {
		t.Fatalf("reopen NewAIStore: %v", err)
	}
	events, err := s2.GetEvents(conv.ID, turn.ID, "p1", 0, 100)
	if err != nil {
		t.Fatalf("reopen GetEvents: %v", err)
	}
	if len(events) != len(wantSeqs)+1 { // +1 for turn.finished
		t.Fatalf("reopened DB has %d events, want %d (deltas) + 1 (finished)", len(events), len(wantSeqs))
	}
	for i, want := range wantSeqs {
		if events[i].Seq != want {
			t.Errorf("event %d Seq = %d, want %d", i, events[i].Seq, want)
		}
	}
	if events[len(events)-1].Type != chatevents.EventTurnFinished {
		t.Errorf("last event type = %q, want turn.finished", events[len(events)-1].Type)
	}
}

// Schema reinitialization: opening a second AIStore against the same
// already-initialized DB must not error (CREATE TABLE IF NOT EXISTS /
// duplicate-column tolerance covers the new tables too).
func TestChatEvents_SchemaReinitializationIsIdempotent(t *testing.T) {
	db := openTestDB(t)
	if _, err := NewAIStore(db); err != nil {
		t.Fatalf("first NewAIStore: %v", err)
	}
	if _, err := NewAIStore(db); err != nil {
		t.Fatalf("second NewAIStore on the same DB: %v", err)
	}
}
