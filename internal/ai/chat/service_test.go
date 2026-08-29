package chat

import (
	"context"
	"database/sql"
	"fmt"
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
	err := svc.StreamChat(context.Background(), "wf-2", "First message", "test-provider", "gpt-4o", nil, nil)
	if err != nil {
		t.Fatalf("StreamChat 1: %v", err)
	}
	svc.newClientFn = func(provider ai.AIProvider) (ai.AIClient, error) {
		return &mockAIClient{response: "Response 2"}, nil
	}
	err = svc.StreamChat(context.Background(), "wf-2", "Second message", "test-provider", "gpt-4o", nil, nil)
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

	if err := svc.StreamChat(context.Background(), "wf-win", "new message", "test-provider", "gpt-4o", nil, nil); err != nil {
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
	err := svc.StreamChat(context.Background(), "wf-3", "Hello", "test-provider", "gpt-4o", nil, nil)
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
