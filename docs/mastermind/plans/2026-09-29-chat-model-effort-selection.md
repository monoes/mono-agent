# Chat Model Effort Level Selection Implementation Plan

> **For agentic workers:** Use the `mastermind-execute` skill to implement this plan task-by-task. Steps use checkbox syntax for tracking.

**Goal:** Allow users in the MonoAgent Chat UI and CLI to dynamically select and set reasoning effort levels (e.g. low, medium, high, max) for models that support extended thinking, while cleanly hiding the selector for models that do not.

**Architecture:** 
1. **Model Discovery:** Extend `monomind.RuntimeModel` to include `EffortLevels []string`. Parse `effort_levels` from `monomind agent models --json` (already supplied by Claude Agent SDK & Codex debug models) and include in static/curated fallbacks.
2. **Persistence:** Add an `effort` column to `ai_chat_conversations` SQLite table (with schema migration). Store and retrieve effort level in `internal/ai/store.go` and `monoagentcli chat history`.
3. **Execution:** Extend `monomind.ExecOptions` to support `Effort string`. Forward effort to the runner subprocess via environment (`CLAUDE_EFFORT`) and protocol flags.
4. **App Supervisor & API:** Update `App.CreateChatConversation` and `services/api.js` to accept `effort`.
5. **UI Component:** In `AIChatPanel.jsx`, detect whether `selectedModel` has `effort_levels`. If supported, render a styled effort dropdown next to the model selector. If unsupported, hide it.

**Tech Stack:** Go 1.24, SQLite 3, Wails v2, React 19, Vitest, Testing Library.

---

## Bite-Sized Implementation Tasks

### Task 1: Expose `effort_levels` in Model Discovery & RuntimeModel

- [ ] **File:** `internal/monomind/models.go`
  - Add `EffortLevels []string `json:"effort_levels,omitempty"`` to `RuntimeModel`.
  - Update `agentModelsResult` struct to include `EffortLevels []string `json:"effort_levels,omitempty"`` in its model list definition.
  - In `listAgentModels`, assign `EffortLevels: m.EffortLevels` when constructing `RuntimeModel`.
  - In `listCodexModels`, update `codexModelCatalog` to parse `supported_reasoning_levels: []struct { Effort string `json:"effort"` }` and map them to `EffortLevels`.
  - In `claudeModels` static list, populate `EffortLevels: []string{"low", "medium", "high", "xhigh", "max"}` for thinking models (`claude-opus-5-5`, `claude-sonnet-5`, etc.) while leaving `claude-haiku-4-5` without effort levels.
- [ ] **File:** `internal/monomind/models_test.go` & `internal/monomind/models_agent_test.go`
  - Add test asserting `EffortLevels` are parsed properly from `agent models` output.
  - Add test asserting `codex debug models` parses `supported_reasoning_levels`.
  - Verify with `go test -v ./internal/monomind/... -run TestListModels`.

---

### Task 2: Database Schema & Conversation Store Effort Column

- [ ] **File:** `data/migrations/055_ai_chat_conversations_effort.sql`
  - Add `ALTER TABLE ai_chat_conversations ADD COLUMN effort TEXT NOT NULL DEFAULT '';`
- [ ] **File:** `internal/ai/chat_events.go`
  - In `initChatEventTables()`, add `ALTER TABLE ai_chat_conversations ADD COLUMN effort TEXT NOT NULL DEFAULT ''` to `addColumnIfMissing` loop.
  - Add `Effort string `json:"effort,omitempty"`` to `Conversation` struct.
  - Add `Effort string `json:"effort,omitempty"`` to `ConversationRecord` struct.
  - Update `Record()` and `Conversation()` conversions to map `Effort`.
  - Update `CreateConversationMode`, `GetConversation`, and `ListConversations` SQL queries to insert, select, and scan `effort`.
- [ ] **File:** `internal/ai/chat_events_test.go` (or `store_test.go`)
  - Add test for creating, retrieving, and listing conversations with an `effort` level.
  - Verify with `go test -v ./internal/ai/...`.

---

### Task 3: CLI Flags & Subprocess Execution for Effort

- [ ] **File:** `internal/monomind/exec.go`
  - Add `Effort string` to `ExecOptions`.
  - In `Exec()`, when `opts.Effort != ""`:
    - For `opts.Runtime == "claude"`, pass `--env CLAUDE_EFFORT=` + `opts.Effort` to `monomind agent exec` args and include `CLAUDE_EFFORT` in `cmd.Env`.
- [ ] **File:** `cmd/monoagentcli/chat_history.go`
  - In `newChatHistoryCreateCmd`, add `--effort` flag.
  - Pass `effort` to `store.CreateConversationMode`.
- [ ] **File:** `cmd/monoagentcli/chat.go`
  - In `newChatCmd`, add `--effort` flag.
  - When loading from stored conversation (`conv.Effort`), or flag override, pass `Effort` into `monomind.ExecOptions`.
- [ ] **File:** `cmd/monoagentcli/chat_test.go`
  - Add unit test verifying `--effort` is accepted and stored on `chat history create`.
  - Verify with `go test -v ./cmd/monoagentcli/... -run TestChat`.

---

### Task 4: Wails App Supervisor & Frontend API

- [ ] **File:** `wails-app/app_chat.go`
  - Update `CreateChatConversation(workflowID, runtimeID, model, effort string) string`.
  - When `effort != ""`, append `"--effort", effort` to `args`.
- [ ] **File:** `wails-app/app_chat_cli_test.go` & `wails-app/app_chat_e2e_test.go`
  - Update test calls to `CreateChatConversation` to match signature.
  - Add test verifying `CreateChatConversation` passes `--effort` to CLI.
- [ ] **File:** `wails-app/frontend/src/wailsjs/go/main/App.d.ts` & `App.js`
  - Update `CreateChatConversation` signature to accept 4 arguments `(arg1, arg2, arg3, arg4)`.
- [ ] **File:** `wails-app/frontend/src/services/api.js`
  - Update `createChatConversation: (workflowID, runtimeID, model, effort = '') => GoApp.CreateChatConversation(workflowID, runtimeID, model, effort).then(parseStreamResult)`.

---

### Task 5: Frontend UI — Dynamic Model-Aware Effort Selector

- [ ] **File:** `wails-app/frontend/src/components/AIChatPanel.jsx`
  - Add `selectedEffort` state (`const [selectedEffort, setSelectedEffort] = useState('')`).
  - Derive `currentModelObj = runtimeModels.find(m => m.id === selectedModel)`.
  - Determine available effort levels: `availableEfforts = currentModelObj?.effort_levels || []`.
  - In the footer model selection bar:
    - If `availableEfforts.length > 0`:
      - Render an Effort `<select>` dropdown next to the Model dropdown.
      - Styled consistent with MonoAgent dark design (`selectStyle`, matching colors/fonts).
      - Default option: `Effort: Default` (value `""`), followed by capitalized effort levels (e.g. `Low`, `Medium`, `High`, `Max`).
      - On change: update `selectedEffort` and invoke `startNewSession()`.
    - If `availableEfforts.length === 0`:
      - Do not render the Effort dropdown (cleanly omitted).
  - In `startNewSession` / `createChatConversation` call, pass `selectedEffort`:
    `api.createChatConversation(workflowID, selectedRuntime, selectedModel, selectedEffort)`.
- [ ] **File:** `wails-app/frontend/src/components/AIChatPanelRuntimeModels.test.jsx`
  - Add test: Model with `effort_levels` renders the effort dropdown.
  - Add test: Model without `effort_levels` does not render the effort dropdown.
  - Add test: Changing effort level creates conversation with chosen effort level.
  - Verify with `npm test -- src/components/AIChatPanelRuntimeModels.test.jsx`.

---

## Verification & Release Gate

1. `go test -v ./internal/monomind/...` passes.
2. `go test -v ./internal/ai/...` passes.
3. `go test -v ./cmd/monoagentcli/...` passes.
4. `go test -v ./wails-app/...` passes.
5. `npm test` in `wails-app/frontend/` passes.
6. Commit with clear conventional commit messages and push branch.
