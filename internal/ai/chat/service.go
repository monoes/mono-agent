package chat

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"
	"github.com/monoes/mono-agent/internal/ai"
)

const systemPromptTemplate = `You are a workflow builder AI inside Mono Agent. You ONLY communicate by calling tool functions. NEVER describe what you would do — ALWAYS call the tools directly.

RULES:
1. If workflow_id is "general" or "draft", call create_workflow FIRST.
2. Then call create_nodes with all needed nodes in ONE call.
3. Then call connect_nodes for each connection.
4. Respond with a brief summary ONLY after all tool calls are done.
5. Use "main" as source_handle and target_handle for connections.
6. Space nodes: increment position_x by 250 per node.

NODE TYPES (use these exact type values in create_nodes):
Triggers: trigger.manual, trigger.schedule, trigger.webhook
Control: core.if, core.switch, core.merge, core.split_in_batches, core.wait, core.stop_error, core.set, core.code, core.filter, core.sort, core.limit, core.remove_duplicates, core.compare_datasets, core.aggregate
HTTP: http.request, http.ftp, http.ssh
Data: data.datetime, data.crypto, data.html, data.xml, data.markdown, data.spreadsheet, data.compression, data.write_binary_file
DB: db.mysql, db.postgres, db.mongodb, db.redis
Comm: comm.email_send, comm.email_read, comm.slack, comm.telegram, comm.discord, comm.twilio, comm.whatsapp
Services: service.github, service.airtable, service.notion, service.jira, service.linear, service.asana, service.stripe, service.shopify, service.salesforce, service.hubspot, service.google_sheets, service.gmail, service.google_drive
AI: agent.ask (local AI agent turn), ai.choose (route items to cases via TypeSafe Jev), browser.jev, ai.read_page, ai.extract_page
Instagram: instagram.find_by_keyword, instagram.export_followers, instagram.scrape_profile_info, instagram.engage_with_posts, instagram.engage_user_posts, instagram.send_dms, instagram.auto_reply_dms, instagram.publish_post, instagram.like_posts, instagram.comment_on_posts, instagram.like_comments_on_posts, instagram.follow_users, instagram.unfollow_users, instagram.extract_post_data, instagram.watch_stories
LinkedIn: linkedin.find_by_keyword, linkedin.export_followers, linkedin.scrape_profile_info, linkedin.engage_with_posts, linkedin.send_dms, linkedin.auto_reply_dms, linkedin.publish_post
X: x.find_by_keyword, x.export_followers, x.scrape_profile_info, x.engage_with_posts, x.send_dms, x.auto_reply_dms, x.publish_post
TikTok: tiktok.find_by_keyword, tiktok.export_followers, tiktok.scrape_profile_info, tiktok.engage_with_posts, tiktok.send_dms, tiktok.auto_reply_dms, tiktok.publish_post`

// maxToolRounds limits how many tool-call round-trips we allow before stopping,
// preventing infinite loops.
const maxToolRounds = 10

// maxHistoryMessages caps how many prior messages are replayed to the
// provider per turn. Older history stays persisted (GetHistory returns it)
// but leaves the window, bounding prompt size on long conversations.
const maxHistoryMessages = 40

// NewClientFunc creates an AIClient from a provider config. It is a field on
// ChatService so tests can inject a mock client without needing real providers.
type NewClientFunc func(provider ai.AIProvider) (ai.AIClient, error)

// ToolExecFunc executes one resolved tool call against the given
// CanvasTools instance, returning the result text destined for the model's
// own tool-result message (a human-readable {"error":...} JSON body when
// the call failed, unchanged from before) and, separately, a non-nil error
// exactly when the call failed. It is a field on ChatService, mirroring
// NewClientFunc's existing injection pattern, so tests can substitute a
// fake tool (artificially slow, or one that fails) without needing a real
// registered CanvasTools tool.
type ToolExecFunc func(ct *CanvasTools, name, argsJSON string) (string, error)

// execToolViaCanvasTools is ChatService's default ToolExecFunc: dispatch to
// the real CanvasTools registry. A failure is still rendered into the
// {"error":...} JSON body the model sees as this tool call's result text
// (unchanged conversational behavior), but the raw error is now also
// returned so callers (e.g. the provider-backend tool.completed event) can
// report ok:false without parsing that text.
func execToolViaCanvasTools(ct *CanvasTools, name, argsJSON string) (string, error) {
	result, err := ct.Execute(name, argsJSON)
	if err != nil {
		return fmt.Sprintf(`{"error": %q}`, err.Error()), err
	}
	return result, nil
}

// ChatService orchestrates AI chat interactions for workflows.
type ChatService struct {
	aiStore     *ai.AIStore
	db          *sql.DB
	newClientFn NewClientFunc
	execToolFn  ToolExecFunc
	canvasTools *CanvasTools
	// nodeTypes mirrors whatever was last passed to SetCanvasNodeTypes, kept
	// here (in addition to being set directly on canvasTools) so
	// StreamChatScoped can build a fresh per-turn CanvasTools with the same
	// registry without needing to read it back off the shared instance.
	nodeTypes []NodeTypeInfo
}

// NewChatService creates a ChatService wired to the given store and database.
func NewChatService(aiStore *ai.AIStore, db *sql.DB) *ChatService {
	return &ChatService{
		aiStore:     aiStore,
		db:          db,
		newClientFn: ai.NewClient,
		execToolFn:  execToolViaCanvasTools,
		canvasTools: NewCanvasTools(db),
	}
}

// SetCanvasNodeTypes provides the available node types to the canvas tools.
func (s *ChatService) SetCanvasNodeTypes(types []NodeTypeInfo) {
	s.nodeTypes = types
	s.canvasTools.SetNodeTypes(types)
}

// SetProfileID sets the active profile for new workflow creation via AI chat.
// Only affects the shared, compatibility StreamChat/GetHistory/ClearHistory
// path — StreamChatScoped/GetHistoryScoped/ClearHistoryScoped take their
// profile explicitly instead and never read this.
func (s *ChatService) SetProfileID(profileID string) {
	s.canvasTools.SetProfileID(profileID)
}

// StreamChat sends a user message to the AI provider and streams the
// response, scoped to whatever profile SetProfileID last set on the shared
// CanvasTools instance. Kept as the existing compatibility binding
// (app_ai.go's StreamAIChat) during migration — see StreamChatScoped for
// the race-free replacement new callers should use.
//
// onChunk is called for each streamed token. onToolStart is called with the
// tool call's ID/name/arguments immediately before that call executes, so
// callers can record a true start time. onToolCall is called once
// execution finishes, carrying the same call ID, the tool's result text and
// an explicit error — non-nil exactly when the call failed. Callers must
// use that error, not string-sniff result for an embedded error marker: the
// result text still carries a human-readable {"error":...} body on failure
// for the model's own conversational context, unchanged from before. Either
// callback may be nil.
func (s *ChatService) StreamChat(
	ctx context.Context,
	workflowID, userMessage, providerID, model string,
	onChunk func(ai.StreamChunk),
	onToolStart func(callID, name, args string),
	onToolCall func(callID, name, args, result string, err error),
) error {
	return s.streamChat(ctx, s.canvasTools, workflowID, userMessage, providerID, model, onChunk, onToolStart, onToolCall)
}

// StreamChatScoped behaves like StreamChat but takes profileID explicitly
// and builds a fresh CanvasTools scoped to it for the duration of this one
// call, instead of reading the shared s.canvasTools field. This is the fix
// for a real race: SetProfileID mutates state shared by every in-flight
// StreamChat call, so a profile switch arriving while a turn is still
// running (e.g. during a slow tool call) would silently redirect that
// ALREADY-RUNNING turn's remaining tool calls and history writes to the
// newly-active profile instead of the one it actually started under (plan
// §142/§236: "do not mutate the shared CanvasTools profile during a
// running turn" / "Use per-turn CanvasTools with a captured profile").
func (s *ChatService) StreamChatScoped(
	ctx context.Context,
	profileID, workflowID, userMessage, providerID, model string,
	onChunk func(ai.StreamChunk),
	onToolStart func(callID, name, args string),
	onToolCall func(callID, name, args, result string, err error),
) error {
	ct := NewCanvasTools(s.db)
	ct.SetProfileID(profileID)
	ct.SetNodeTypes(s.nodeTypes)
	return s.streamChat(ctx, ct, workflowID, userMessage, providerID, model, onChunk, onToolStart, onToolCall)
}

// streamChat is StreamChat's actual body, parameterized on which
// CanvasTools instance to use — ct is captured once by the caller (either
// the shared s.canvasTools, or a fresh scoped one from StreamChatScoped)
// and used consistently for the whole turn, never re-read mid-flight.
func (s *ChatService) streamChat(
	ctx context.Context,
	ct *CanvasTools,
	workflowID, userMessage, providerID, model string,
	onChunk func(ai.StreamChunk),
	onToolStart func(callID, name, args string),
	onToolCall func(callID, name, args, result string, err error),
) error {
	if err := ct.checkWorkflowOwnership(workflowID); err != nil {
		return err
	}

	// 1. Resolve provider and create client.
	provider, err := s.aiStore.GetProvider(providerID, ct.ProfileID())
	if err != nil {
		return fmt.Errorf("get provider %s: %w", providerID, err)
	}

	client, err := s.newClientFn(provider)
	if err != nil {
		return fmt.Errorf("create ai client: %w", err)
	}

	// 2. Load existing history, windowed to the most recent
	// maxHistoryMessages entries.
	history, err := s.aiStore.GetChatHistory(workflowID, ct.ProfileID())
	if err != nil {
		return fmt.Errorf("get chat history: %w", err)
	}
	if len(history) > maxHistoryMessages {
		history = history[len(history)-maxHistoryMessages:]
	}

	// 3. Build messages array: system + history + new user message.
	systemPrompt := systemPromptTemplate + fmt.Sprintf("\n\nCurrent workflow_id: %s", workflowID)
	messages := make([]ai.Message, 0, len(history)+2)
	messages = append(messages, ai.Message{
		Role:    ai.RoleSystem,
		Content: systemPrompt,
	})
	for _, h := range history {
		msg := ai.Message{
			Role:    h.Role,
			Content: h.Content,
		}
		if h.ToolCalls != "" {
			var tc []ai.ToolCall
			if err := json.Unmarshal([]byte(h.ToolCalls), &tc); err == nil {
				msg.ToolCalls = tc
			}
		}
		if h.ToolCallID != "" {
			msg.ToolCallID = h.ToolCallID
		}
		messages = append(messages, msg)
	}
	messages = append(messages, ai.Message{
		Role:    ai.RoleUser,
		Content: userMessage,
	})

	// 4. Save user message to store.
	if err := s.aiStore.SaveChatMessage(ai.ChatMessage{
		ID:         uuid.New().String(),
		WorkflowID: workflowID,
		ProfileID:  ct.ProfileID(),
		Role:       ai.RoleUser,
		Content:    userMessage,
	}); err != nil {
		return fmt.Errorf("save user message: %w", err)
	}

	// 5. Build tool definitions from canvas tools.
	toolDefs := ct.ToolDefs()

	// 6. Stream the first response.
	var accumulated string
	var toolCalls []ai.ToolCall

	req := ai.CompletionRequest{
		Model:    model,
		Messages: messages,
		Tools:    toolDefs,
		Stream:   true,
	}

	err = client.StreamComplete(ctx, req, func(chunk ai.StreamChunk) {
		accumulated += chunk.Content
		if len(chunk.ToolCalls) > 0 {
			toolCalls = append(toolCalls, chunk.ToolCalls...)
		}
		if onChunk != nil {
			onChunk(chunk)
		}
	})
	if err != nil {
		return fmt.Errorf("stream complete: %w", err)
	}

	// 7. Tool-call loop (up to maxToolRounds).
	for round := 0; round < maxToolRounds && len(toolCalls) > 0; round++ {
		// Save assistant message that contains tool calls.
		tcJSON, _ := json.Marshal(toolCalls)
		if err := s.aiStore.SaveChatMessage(ai.ChatMessage{
			ID:         uuid.New().String(),
			WorkflowID: workflowID,
			ProfileID:  ct.ProfileID(),
			Role:       ai.RoleAssistant,
			Content:    accumulated,
			ToolCalls:  string(tcJSON),
			ProviderID: providerID,
			Model:      model,
		}); err != nil {
			return fmt.Errorf("save assistant tool-call message: %w", err)
		}

		// Add assistant message with tool calls to conversation.
		messages = append(messages, ai.Message{
			Role:      ai.RoleAssistant,
			Content:   accumulated,
			ToolCalls: toolCalls,
		})

		// Execute each tool call and add results. callID identifies this
		// call to onToolStart/onToolCall so a caller (e.g. app_chat.go) can
		// pair a real start signal with its completion and derive a true
		// elapsed time — tc.ID normally provides this (already relied on
		// below for ToolCallID threading), with a generated fallback only
		// for the defensive case of an adapter that omits it, so the two
		// display callbacks never disagree on the call's identity.
		for _, tc := range toolCalls {
			callID := tc.ID
			if callID == "" {
				callID = uuid.New().String()
			}
			if onToolStart != nil {
				onToolStart(callID, tc.Function.Name, tc.Function.Arguments)
			}
			result, toolErr := s.executeTool(ct, tc.Function.Name, tc.Function.Arguments)
			if onToolCall != nil {
				onToolCall(callID, tc.Function.Name, tc.Function.Arguments, result, toolErr)
			}

			messages = append(messages, ai.Message{
				Role:       ai.RoleTool,
				Content:    result,
				ToolCallID: tc.ID,
			})

			// Persist tool result.
			if err := s.aiStore.SaveChatMessage(ai.ChatMessage{
				ID:         uuid.New().String(),
				WorkflowID: workflowID,
				ProfileID:  ct.ProfileID(),
				Role:       ai.RoleTool,
				Content:    result,
				ToolCallID: tc.ID,
			}); err != nil {
				return fmt.Errorf("save tool result message: %w", err)
			}
		}

		// Continue with a non-streaming Complete call.
		accumulated = ""
		toolCalls = nil

		contReq := ai.CompletionRequest{
			Model:    model,
			Messages: messages,
			Tools:    toolDefs,
		}
		resp, err := client.Complete(ctx, contReq)
		if err != nil {
			return fmt.Errorf("complete (tool continuation round %d): %w", round+1, err)
		}

		accumulated = resp.Content
		toolCalls = resp.ToolCalls

		// Stream the continuation text to the caller.
		if onChunk != nil && accumulated != "" {
			onChunk(ai.StreamChunk{Content: accumulated})
		}
	}

	// 8. Save final assistant message.
	if err := s.aiStore.SaveChatMessage(ai.ChatMessage{
		ID:         uuid.New().String(),
		WorkflowID: workflowID,
		ProfileID:  ct.ProfileID(),
		Role:       ai.RoleAssistant,
		Content:    accumulated,
		ProviderID: providerID,
		Model:      model,
	}); err != nil {
		return fmt.Errorf("save assistant message: %w", err)
	}

	return nil
}

// executeTool dispatches a tool call by name via s.execToolFn (defaulting
// to execToolViaCanvasTools, dispatching through the given CanvasTools
// instance — the shared one for StreamChat, a per-turn one for
// StreamChatScoped). Returns the result text for the model's tool message
// and a separate, explicit error — never string-sniffed — for callers that
// need to know whether the call actually failed.
func (s *ChatService) executeTool(ct *CanvasTools, name, argsJSON string) (string, error) {
	return s.execToolFn(ct, name, argsJSON)
}

// GetHistory returns the full chat history for a workflow, scoped to the
// active profile (set via SetProfileID). Compatibility binding — see
// GetHistoryScoped for the explicit-profile replacement.
func (s *ChatService) GetHistory(workflowID string) ([]ai.ChatMessage, error) {
	if err := s.canvasTools.checkWorkflowOwnership(workflowID); err != nil {
		return nil, err
	}
	return s.aiStore.GetChatHistory(workflowID, s.canvasTools.ProfileID())
}

// ClearHistory deletes all chat messages for a workflow, scoped to the
// active profile (set via SetProfileID). Compatibility binding — see
// ClearHistoryScoped for the explicit-profile replacement.
func (s *ChatService) ClearHistory(workflowID string) error {
	if err := s.canvasTools.checkWorkflowOwnership(workflowID); err != nil {
		return err
	}
	return s.aiStore.ClearChatHistory(workflowID, s.canvasTools.ProfileID())
}

// GetHistoryScoped is GetHistory with profileID passed explicitly instead
// of read off the shared, mutable canvasTools field.
func (s *ChatService) GetHistoryScoped(profileID, workflowID string) ([]ai.ChatMessage, error) {
	ct := NewCanvasTools(s.db)
	ct.SetProfileID(profileID)
	if err := ct.checkWorkflowOwnership(workflowID); err != nil {
		return nil, err
	}
	return s.aiStore.GetChatHistory(workflowID, profileID)
}

// ClearHistoryScoped is ClearHistory with profileID passed explicitly
// instead of read off the shared, mutable canvasTools field.
func (s *ChatService) ClearHistoryScoped(profileID, workflowID string) error {
	ct := NewCanvasTools(s.db)
	ct.SetProfileID(profileID)
	if err := ct.checkWorkflowOwnership(workflowID); err != nil {
		return err
	}
	return s.aiStore.ClearChatHistory(workflowID, profileID)
}
