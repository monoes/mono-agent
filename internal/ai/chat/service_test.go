package chat

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/zalando/go-keyring"

	"github.com/monoes/mono-agent/internal/ai"
	"github.com/monoes/mono-agent/internal/storage"
)

// --- mock AI client ---

type mockAIClient struct {
	response string
}

func (m *mockAIClient) Complete(ctx context.Context, req ai.CompletionRequest) (ai.CompletionResponse, error) {
	return ai.CompletionResponse{Content: m.response, FinishReason: "stop"}, nil
}

func (m *mockAIClient) StreamComplete(ctx context.Context, req ai.CompletionRequest, onChunk func(ai.StreamChunk)) error {
	onChunk(ai.StreamChunk{Content: m.response, Done: true})
	return nil
}

// --- helpers ---

func openTestDB(t *testing.T) *sql.DB {
	t.Helper()
	keyring.MockInit()
	db, err := storage.NewDatabase(t.TempDir() + "/chat-test.db")
	if err != nil {
		t.Fatalf("NewDatabase: %v", err)
	}
	if err := db.ApplyMigrations(); err != nil {
		t.Fatalf("ApplyMigrations: %v", err)
	}
	t.Cleanup(func() { db.DB.Close() })
	return db.DB
}

// seedWorkflow registers a workflow row owned by the default profile, so
// checkWorkflowOwnership (called by StreamChat/GetHistory/ClearHistory) allows it.
func seedWorkflow(t *testing.T, db *sql.DB, workflowID string) {
	t.Helper()
	if _, err := db.Exec(`INSERT INTO workflows (id, name, profile_id) VALUES (?, 'Test Workflow', 'default')`, workflowID); err != nil {
		t.Fatalf("seed workflow %s: %v", workflowID, err)
	}
}

func newTestService(t *testing.T, mockResp string, workflowIDs ...string) *ChatService {
	t.Helper()
	db := openTestDB(t)
	for _, id := range workflowIDs {
		seedWorkflow(t, db, id)
	}
	store, err := ai.NewAIStore(db)
	if err != nil {
		t.Fatalf("NewAIStore: %v", err)
	}

	// Seed a provider so GetProvider succeeds.
	if err := store.SaveProvider(ai.AIProvider{
		ID:         "test-provider",
		Name:       "Test",
		ProviderID: "openai",
		Tier:       "known",
		APIKey:     "sk-test",
		Status:     "active",
	}); err != nil {
		t.Fatalf("SaveProvider: %v", err)
	}

	svc := NewChatService(store, db)
	// Override the client factory so we don't need real provider wiring.
	mock := &mockAIClient{response: mockResp}
	svc.newClientFn = func(provider ai.AIProvider) (ai.AIClient, error) {
		return mock, nil
	}
	return svc
}

// --- tests ---

func TestStreamChatBasic(t *testing.T) {
	svc := newTestService(t, "Hello from AI!", "wf-1")

	var mu sync.Mutex
	var chunks []ai.StreamChunk

	err := svc.StreamChat(
		context.Background(),
		"wf-1",
		"Hi there",
		"test-provider",
		"gpt-4o",
		func(chunk ai.StreamChunk) {
			mu.Lock()
			chunks = append(chunks, chunk)
			mu.Unlock()
		},
		nil, // no tool calls expected
		nil,
	)
	if err != nil {
		t.Fatalf("StreamChat: %v", err)
	}

	// Verify onChunk was called at least once with the AI response.
	mu.Lock()
	defer mu.Unlock()
	if len(chunks) == 0 {
		t.Fatal("expected at least one chunk, got none")
	}
	found := false
	for _, c := range chunks {
		if c.Content == "Hello from AI!" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected chunk with content %q, got %v", "Hello from AI!", chunks)
	}

	// Verify messages were persisted: user + assistant.
	history, err := svc.GetHistory("wf-1")
	if err != nil {
		t.Fatalf("GetHistory: %v", err)
	}
	if len(history) != 2 {
		t.Fatalf("expected 2 messages in history, got %d", len(history))
	}
	if history[0].Role != ai.RoleUser {
		t.Errorf("first message role = %q, want %q", history[0].Role, ai.RoleUser)
	}
	if history[0].Content != "Hi there" {
		t.Errorf("first message content = %q, want %q", history[0].Content, "Hi there")
	}
	if history[1].Role != ai.RoleAssistant {
		t.Errorf("second message role = %q, want %q", history[1].Role, ai.RoleAssistant)
	}
	if history[1].Content != "Hello from AI!" {
		t.Errorf("second message content = %q, want %q", history[1].Content, "Hello from AI!")
	}
}

func TestGetHistory(t *testing.T) {
	svc := newTestService(t, "Response 1", "wf-2")

	// Send two messages to build history.
	err := svc.StreamChat(context.Background(), "wf-2", "First message", "test-provider", "gpt-4o", nil, nil, nil)
	if err != nil {
		t.Fatalf("StreamChat 1: %v", err)
	}
	svc.newClientFn = func(provider ai.AIProvider) (ai.AIClient, error) {
		return &mockAIClient{response: "Response 2"}, nil
	}
	err = svc.StreamChat(context.Background(), "wf-2", "Second message", "test-provider", "gpt-4o", nil, nil, nil)
	if err != nil {
		t.Fatalf("StreamChat 2: %v", err)
	}

	history, err := svc.GetHistory("wf-2")
	if err != nil {
		t.Fatalf("GetHistory: %v", err)
	}
	// 2 user messages + 2 assistant messages = 4
	if len(history) != 4 {
		t.Fatalf("expected 4 messages, got %d", len(history))
	}
	if history[0].Content != "First message" {
		t.Errorf("history[0].Content = %q, want %q", history[0].Content, "First message")
	}
	if history[1].Content != "Response 1" {
		t.Errorf("history[1].Content = %q, want %q", history[1].Content, "Response 1")
	}
	if history[2].Content != "Second message" {
		t.Errorf("history[2].Content = %q, want %q", history[2].Content, "Second message")
	}
	if history[3].Content != "Response 2" {
		t.Errorf("history[3].Content = %q, want %q", history[3].Content, "Response 2")
	}
}

// capturingAIClient records every request it serves, for windowing checks.
type capturingAIClient struct {
	mockAIClient
	requests []ai.CompletionRequest
}

func (c *capturingAIClient) Complete(ctx context.Context, req ai.CompletionRequest) (ai.CompletionResponse, error) {
	c.requests = append(c.requests, req)
	return ai.CompletionResponse{Content: c.mockAIClient.response, FinishReason: "stop"}, nil
}

func (c *capturingAIClient) StreamComplete(ctx context.Context, req ai.CompletionRequest, onChunk func(ai.StreamChunk)) error {
	c.requests = append(c.requests, req)
	onChunk(ai.StreamChunk{Content: c.mockAIClient.response, Done: true})
	return nil
}

// TestStreamChatWindowsHistory is the RB3 unbounded-history regression
// test: a long persisted history is replayed to the provider windowed to
// the last maxHistoryMessages entries, not in full.
func TestStreamChatWindowsHistory(t *testing.T) {
	svc := newTestService(t, "ok", "wf-win")
	mock := &capturingAIClient{mockAIClient: mockAIClient{response: "ok"}}
	svc.newClientFn = func(provider ai.AIProvider) (ai.AIClient, error) {
		return mock, nil
	}

	// Seed 60 history messages directly in the store.
	base := time.Date(2025, 6, 15, 12, 0, 0, 0, time.UTC)
	for i := 0; i < 60; i++ {
		if err := svc.aiStore.SaveChatMessage(ai.ChatMessage{
			ID:         fmt.Sprintf("h%03d", i),
			WorkflowID: "wf-win",
			Role:       ai.RoleUser,
			Content:    fmt.Sprintf("history %d", i),
			CreatedAt:  base.Add(time.Duration(i) * time.Second).Format(time.RFC3339),
		}); err != nil {
			t.Fatalf("seed h%d: %v", i, err)
		}
	}

	if err := svc.StreamChat(context.Background(), "wf-win", "new message", "test-provider", "gpt-4o", nil, nil, nil); err != nil {
		t.Fatalf("StreamChat: %v", err)
	}
	if len(mock.requests) == 0 {
		t.Fatal("no requests captured")
	}

	msgs := mock.requests[0].Messages
	wantTotal := 1 + maxHistoryMessages + 1 // system + windowed history + new user msg
	if len(msgs) != wantTotal {
		t.Fatalf("request carries %d messages, want %d (windowed)", len(msgs), wantTotal)
	}
	if msgs[0].Role != ai.RoleSystem {
		t.Errorf("msgs[0].Role = %q, want system", msgs[0].Role)
	}
	if msgs[len(msgs)-1].Content != "new message" {
		t.Errorf("last message = %q, want the new user message", msgs[len(msgs)-1].Content)
	}
	// The window must contain the NEWEST history: first history entry is #20
	// of 60 (indexes 0-59, window drops the oldest 20).
	if got := msgs[1].Content; got != "history 20" {
		t.Errorf("window starts at %q, want %q", got, "history 20")
	}

	// Full history stays persisted and unwindowed via GetHistory.
	full, err := svc.GetHistory("wf-win")
	if err != nil {
		t.Fatalf("GetHistory: %v", err)
	}
	if len(full) != 62 { // 60 seeded + new user + final assistant
		t.Errorf("persisted history = %d messages, want 62", len(full))
	}
}

func TestClearHistory(t *testing.T) {
	svc := newTestService(t, "Some response", "wf-3")

	// Create some history.
	err := svc.StreamChat(context.Background(), "wf-3", "Hello", "test-provider", "gpt-4o", nil, nil, nil)
	if err != nil {
		t.Fatalf("StreamChat: %v", err)
	}

	history, err := svc.GetHistory("wf-3")
	if err != nil {
		t.Fatalf("GetHistory: %v", err)
	}
	if len(history) == 0 {
		t.Fatal("expected non-empty history before clear")
	}

	// Clear.
	if err := svc.ClearHistory("wf-3"); err != nil {
		t.Fatalf("ClearHistory: %v", err)
	}

	history, err = svc.GetHistory("wf-3")
	if err != nil {
		t.Fatalf("GetHistory after clear: %v", err)
	}
	if len(history) != 0 {
		t.Errorf("expected empty history after clear, got %d messages", len(history))
	}
}

// TestStreamChatScoped_ImmuneToConcurrentSetProfileID is the regression test
// for the mutable-shared-CanvasTools-profile bug (plan §142/§236):
// SetProfileID on the shared service must never affect an in-flight
// StreamChatScoped call for a different, explicitly-passed profile. The
// seeded provider belongs to profile "default" (newTestService's
// SaveProvider call never sets ProfileID, which normalizes to "default"),
// so if StreamChatScoped leaked into using the shared canvasTools field
// anywhere, the provider lookup itself would fail against "profile-b"
// instead of merely mis-attributing history — a more sensitive failure
// signal than checking saved rows alone.
func TestStreamChatScoped_ImmuneToConcurrentSetProfileID(t *testing.T) {
	svc := newTestService(t, "hi")
	// Simulate a profile switch on the shared service happening around the
	// same time as an in-flight scoped call under a different profile.
	svc.SetProfileID("profile-b")

	if err := svc.StreamChatScoped(context.Background(), "default", "general", "hello", "test-provider", "gpt-4o", nil, nil, nil); err != nil {
		t.Fatalf("StreamChatScoped: %v", err)
	}

	historyDefault, err := svc.GetHistoryScoped("default", "general")
	if err != nil {
		t.Fatalf("GetHistoryScoped(default): %v", err)
	}
	if len(historyDefault) != 2 {
		t.Fatalf("GetHistoryScoped(default) = %d messages, want 2 (user + assistant)", len(historyDefault))
	}

	historyB, err := svc.GetHistoryScoped("profile-b", "general")
	if err != nil {
		t.Fatalf("GetHistoryScoped(profile-b): %v", err)
	}
	if len(historyB) != 0 {
		t.Errorf("GetHistoryScoped(profile-b) = %d messages, want 0 — StreamChatScoped(default,...) must never write under the shared field's profile-b", len(historyB))
	}
}

// TestClearHistoryScoped_DoesNotUseSharedProfile mirrors the StreamChatScoped
// regression above for ClearHistoryScoped specifically: clearing "default"'s
// history via the scoped method must not be redirected to whatever profile
// the shared canvasTools field currently holds.
func TestClearHistoryScoped_DoesNotUseSharedProfile(t *testing.T) {
	svc := newTestService(t, "hi")
	if err := svc.StreamChatScoped(context.Background(), "default", "general", "hello", "test-provider", "gpt-4o", nil, nil, nil); err != nil {
		t.Fatalf("StreamChatScoped: %v", err)
	}
	svc.SetProfileID("profile-b")

	if err := svc.ClearHistoryScoped("default", "general"); err != nil {
		t.Fatalf("ClearHistoryScoped(default): %v", err)
	}
	history, err := svc.GetHistoryScoped("default", "general")
	if err != nil {
		t.Fatalf("GetHistoryScoped(default): %v", err)
	}
	if len(history) != 0 {
		t.Errorf("GetHistoryScoped(default) after ClearHistoryScoped(default) = %d messages, want 0", len(history))
	}
}

// --- provider tool-call start/end signal regression tests ---
//
// Root cause (interactive-agent-chat followups, Round 1 Code Reviewer):
// the tool-calling loop below used to invoke a single onToolCall callback
// only AFTER executeTool had already fully run, with no separate start
// signal and no explicit error value — app_chat.go's caller could not
// derive a real elapsed time (both events were stamped back-to-back after
// the fact) and had no way to know a call had failed short of parsing the
// {"error":...} JSON text executeTool produces for the model's own benefit.

// toolCallingAIClient is a fake AIClient that requests exactly one named
// tool call on its first (streamed) response, then returns a plain final
// answer with no further tool calls on the non-streaming continuation
// round, so the tool loop in streamChat runs exactly once.
type toolCallingAIClient struct {
	toolName   string
	toolArgs   string
	toolCallID string
	final      string
}

func (c *toolCallingAIClient) StreamComplete(ctx context.Context, req ai.CompletionRequest, onChunk func(ai.StreamChunk)) error {
	onChunk(ai.StreamChunk{
		ToolCalls: []ai.ToolCall{{
			ID:       c.toolCallID,
			Type:     "function",
			Function: ai.ToolCallFunc{Name: c.toolName, Arguments: c.toolArgs},
		}},
		Done: true,
	})
	return nil
}

func (c *toolCallingAIClient) Complete(ctx context.Context, req ai.CompletionRequest) (ai.CompletionResponse, error) {
	return ai.CompletionResponse{Content: c.final, FinishReason: "stop"}, nil
}

// TestStreamChat_ToolStart_FiresBeforeExecution_ElapsedTimeIsReal is the
// regression test for "provider-backend tool cards always show ~0.0s
// elapsed time": onToolStart must fire BEFORE the tool actually executes,
// not be synthesized back-to-back with the completion callback afterward.
// A caller (app_chat.go) deriving elapsed time from two real wall-clock
// event timestamps only gets a true, non-zero duration if the start
// signal genuinely precedes execution.
func TestStreamChat_ToolStart_FiresBeforeExecution_ElapsedTimeIsReal(t *testing.T) {
	svc := newTestService(t, "", "wf-slow")
	svc.newClientFn = func(provider ai.AIProvider) (ai.AIClient, error) {
		return &toolCallingAIClient{toolName: "slow_tool", toolArgs: `{}`, toolCallID: "call-1", final: "done"}, nil
	}

	// A deliberately-slow fake tool: execToolFn stands in for the real
	// CanvasTools dispatch (mirroring newClientFn's existing injection
	// pattern) so the test can control exactly how long "execution" takes
	// without needing a naturally-slow real tool.
	const delay = 60 * time.Millisecond
	svc.execToolFn = func(ct *CanvasTools, name, argsJSON string) (string, error) {
		time.Sleep(delay)
		return `{"ok":true}`, nil
	}

	var startAt, endAt time.Time
	var startCallID, endCallID string
	err := svc.StreamChat(context.Background(), "wf-slow", "go", "test-provider", "gpt-4o",
		nil,
		func(callID, name, args string) {
			startAt = time.Now()
			startCallID = callID
		},
		func(callID, name, args, result string, toolErr error) {
			endAt = time.Now()
			endCallID = callID
			if toolErr != nil {
				t.Errorf("unexpected tool error: %v", toolErr)
			}
		},
	)
	if err != nil {
		t.Fatalf("StreamChat: %v", err)
	}
	if startAt.IsZero() {
		t.Fatal("onToolStart was never called")
	}
	if endAt.IsZero() {
		t.Fatal("onToolCall was never called")
	}
	if startCallID == "" || startCallID != endCallID {
		t.Errorf("onToolStart/onToolCall callID mismatch: start=%q end=%q, want matching non-empty IDs so a caller can pair them", startCallID, endCallID)
	}
	if elapsed := endAt.Sub(startAt); elapsed < delay {
		t.Errorf("elapsed between onToolStart and onToolCall = %v, want >= the tool's actual execution delay of %v (this is exactly the ~0.0s bug: both callbacks used to fire together after execution finished)", elapsed, delay)
	}
}

// TestStreamChat_ToolExecutionError_ReportedExplicitly_NotStringSniffed is
// the regression test for "provider-backend tool failures always render as
// success": onToolCall must receive a real, non-nil error when the tool
// call failed, distinct from the human-readable {"error":...} body that
// still goes into the tool result text for the model's own conversational
// context. No fake execToolFn override is needed here — requesting an
// unregistered tool name drives a real failure through CanvasTools.Execute
// unmodified, exercising the actual production error path end to end.
func TestStreamChat_ToolExecutionError_ReportedExplicitly_NotStringSniffed(t *testing.T) {
	svc := newTestService(t, "", "wf-err")
	svc.newClientFn = func(provider ai.AIProvider) (ai.AIClient, error) {
		return &toolCallingAIClient{toolName: "no_such_tool", toolArgs: `{}`, toolCallID: "call-err", final: "done"}, nil
	}

	var gotErr error
	var gotResult string
	called := false
	err := svc.StreamChat(context.Background(), "wf-err", "go", "test-provider", "gpt-4o",
		nil,
		nil,
		func(callID, name, args, result string, toolErr error) {
			called = true
			gotErr = toolErr
			gotResult = result
		},
	)
	if err != nil {
		t.Fatalf("StreamChat: %v", err)
	}
	if !called {
		t.Fatal("onToolCall was never called")
	}
	if gotErr == nil {
		t.Fatal("onToolCall received a nil error for a tool call that failed — this is the ok:=true-always bug: the caller has no explicit signal and would have to string-sniff `result` instead")
	}
	if !strings.Contains(gotErr.Error(), "unknown tool") {
		t.Errorf("onToolCall error = %q, want it to carry the underlying failure", gotErr.Error())
	}
	if !strings.Contains(gotResult, "unknown tool") {
		t.Errorf("result text = %q, want the model-facing {\"error\":...} body preserved unchanged", gotResult)
	}
}
