package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/spf13/cobra"

	"monoagent/internal/ai"
	aichat "monoagent/internal/ai/chat"
	"monoagent/internal/monomind"
	"monoagent/internal/noderegistry"
	"monoagent/internal/profiledir"
	"monoagent/internal/storage"
)

// parseDurationFlag accepts plain seconds ("90") or suffixed ("90s", "10m", "2h").
func parseDurationFlag(s string) (time.Duration, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, nil
	}
	if d, err := time.ParseDuration(s); err == nil {
		return d, nil
	}
	var secs float64
	if _, err := fmt.Sscanf(s, "%f", &secs); err == nil {
		return time.Duration(secs * float64(time.Second)), nil
	}
	return 0, fmt.Errorf("invalid duration %q (use e.g. 90s, 10m, 2h)", s)
}

func newChatCmd(cfg *globalConfig) *cobra.Command {
	var (
		runtime   string
		model     string
		resume    string
		canvasID  string
		historyID string
		timeoutS  string
		budget    float64
		tools     string
	)
	cmd := &cobra.Command{
		Use:   "chat [prompt]",
		Short: "Chat with a locally-installed AI agent (via the monomind engine)",
		Long: "One agent turn delegated through monomind's Agent Exec Protocol. Streams NDJSON " +
			"events to stdout (start, session, assistant, tool_call, tool_result, usage, result, " +
			"done) — this stream is the interface the desktop app consumes.\n\n" +
			"With --canvas <workflowID> the turn runs as a workflow-builder assistant: the eight " +
			"canvas tools (create workflow/nodes/connections, …) execute against this machine's " +
			"database over the stdio tool bridge, and the conversation is persisted to the chat " +
			"history table.",
		Args: cobra.MinimumNArgs(1),
		Example: `  monoagentcli chat --runtime claude "summarize the output folder"
  monoagentcli chat --runtime codex --canvas general "build a gmail digest workflow"
  monoagentcli chat --runtime claude --resume th_9f2a "continue"`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if runtime == "" {
				return fmt.Errorf("--runtime is required (see `agent scan --installed`)")
			}
			prompt := strings.Join(args, " ")
			timeout, err := parseDurationFlag(timeoutS)
			if err != nil {
				return err
			}
			ctx := cmd.Context()
			bin, _, err := monomind.Ensure(ctx)
			if err != nil {
				return err
			}

			wantMonoagentTools := tools == "monoagent"
			// effectiveHistoryID is the persistence/session bucket key —
			// decoupled from --canvas, which only controls whether
			// workflow-builder tools are wired in. Falls back to canvasID
			// so canvas-only manual CLI usage still persists as before.
			effectiveHistoryID := historyID
			if effectiveHistoryID == "" {
				effectiveHistoryID = canvasID
			}
			// canvasExplicit distinguishes the real --canvas mode (Workflows
			// editor, building/editing one specific workflow) from
			// --tools monoagent auto-wiring canvas tools for the general
			// assistant below — the two want different system-prompt framing
			// (see the toolSpecs/systemPromptParts block further down).
			canvasExplicit := canvasID != ""
			// effectiveCanvasID: without this, --tools monoagent alone (the
			// global/general assistant, no --canvas) had zero tools capable
			// of creating a workflow or nodes — MonoagentTools only
			// lists/reads/deletes existing ones. Asked to build something,
			// the model would discover no create_workflow tool exists, try
			// Bash as a fallback (denied — see agent-runner.ts's
			// allowedTools restriction), and burn the whole turn on dead
			// ends before giving up. "draft" is the same placeholder
			// CanvasTools already treats as ownership-check-exempt for a
			// not-yet-created workflow (see checkWorkflowOwnership) — the
			// model's own create_workflow call replaces it with a real id
			// for every subsequent tool call in the turn.
			effectiveCanvasID := canvasID
			if effectiveCanvasID == "" && wantMonoagentTools {
				effectiveCanvasID = "draft"
			}

			var (
				store     *ai.AIStore
				closeDB   func()
				canvas    *aichat.CanvasTools
				monoTools *aichat.MonoagentTools
				nodeTypes []aichat.NodeTypeInfo
				profileID string
				db        *storage.Database
			)
			if effectiveCanvasID != "" || wantMonoagentTools || effectiveHistoryID != "" {
				store, closeDB, err = openAIStore(cfg)
				if err != nil {
					return err
				}
				defer closeDB()
				db, err = initDB(cfg)
				if err != nil {
					return fmt.Errorf("initializing database: %w", err)
				}
				defer db.Close()
				profileID, err = resolveProfileID(db.DB, cfg.ProfileID)
				if err != nil {
					return fmt.Errorf("resolving profile: %w", err)
				}

				if effectiveCanvasID != "" {
					canvas = aichat.NewCanvasTools(db.DB)
					canvas.SetProfileID(profileID)
					nodeTypes = registryNodeTypes(db.DB)
					canvas.SetNodeTypes(nodeTypes)
					if err := canvas.CheckWorkflowOwnership(effectiveCanvasID); err != nil {
						return err
					}
				}

				if wantMonoagentTools {
					// selfBin lets run_workflow/run_action shell back into this
					// same binary's already-wired execution path (engine/
					// scheduler/DI) rather than duplicating it — best-effort:
					// those two tools just report themselves unavailable if
					// os.Executable() fails, everything else still works.
					selfBin, _ := os.Executable()
					monoTools = aichat.NewMonoagentTools(db.DB, selfBin)
					monoTools.SetProfileID(profileID)
				}
			}

			// Deliberately no --cwd override: an earlier version of this
			// pointed every turn at a dedicated working directory so agent
			// CLIs' own CLAUDE.md/AGENTS.md conventions would auto-load
			// monoagent guidance — measured to add anywhere from ~20s to
			// ~40s of pure overhead (and tens of thousands of tokens) to
			// every single turn, even a bare "hi": the file's presence
			// pushed several agent CLIs into an unprompted "let me check
			// the workspace guidance first" exploration step before
			// answering, on every call, not just the first. --canvas mode
			// never had this problem because it only ever used
			// opts.SystemPrompt (injected directly, not read from a file) —
			// monoagentSystemPrompt() below does the same for tool
			// guidance, so there's nothing left that needs a stable cwd.
			opts := monomind.ExecOptions{
				Bin:       bin,
				Runtime:   runtime,
				Prompt:    prompt,
				Model:     model,
				Resume:    resume,
				Timeout:   timeout,
				BudgetUSD: budget,
			}

			var toolSpecs []monomind.ToolSpec
			var systemPromptParts []string
			if canvas != nil {
				// canvasSystemPrompt's framing ("You are a workflow builder
				// AI... You ONLY communicate by calling tool functions") is
				// correct for the real Workflows-editor session (the user
				// opened it specifically to build/edit canvasID) but wrong
				// for the general assistant's auto-wired case — injecting
				// "only respond via tool calls" into every plain "hi"
				// message would force tool-only behavior onto ordinary
				// conversation. The auto-wired case only needs the tools
				// themselves plus the mention already in
				// monoagentSystemPrompt() below.
				if canvasExplicit {
					systemPromptParts = append(systemPromptParts, canvasSystemPrompt(effectiveCanvasID, nodeTypes))
				}
				toolSpecs = append(toolSpecs, canvasToolSpecs(canvas)...)
			}
			if monoTools != nil {
				// MONOMIND_CWD (not --cwd — see the load-bearing comment
				// above) scopes monograph_search/memory_kg_search below to
				// this profile's own knowledge-graph databases, independent
				// of the chat subprocess's actual working directory.
				opts.Env = map[string]string{"MONOMIND_CWD": profiledir.MonomindDir(db.DB, profileID)}
				systemPromptParts = append(systemPromptParts, monoagentSystemPrompt(canvas != nil))
				toolSpecs = append(toolSpecs, monoagentToolSpecs(monoTools)...)
				toolSpecs = append(toolSpecs, monographSearchToolSpec(), memoryKGSearchToolSpec())
			}
			if len(toolSpecs) > 0 {
				opts.SystemPrompt = strings.Join(systemPromptParts, "\n\n---\n\n")
				opts.Tools = toolSpecs
				opts.ToolTimeout = 2 * time.Minute
				opts.OnToolCall = func(ctx context.Context, name string, args json.RawMessage) (string, error) {
					if name == "monograph_search" || name == "memory_kg_search" {
						return runKGTool(ctx, bin, name, args, profiledir.MonomindDir(db.DB, profileID))
					}
					if canvas != nil {
						if text, err := canvas.Execute(name, string(args)); err == nil || !strings.HasPrefix(err.Error(), "unknown tool:") {
							return text, err
						}
					}
					if monoTools != nil {
						return monoTools.Execute(name, string(args))
					}
					return "", fmt.Errorf("unknown tool: %s", name)
				}
			}

			emit := func(v interface{}) {
				b, _ := json.Marshal(v)
				fmt.Println(string(b))
			}
			// Subprocess runners' result events carry usage but not always
			// text — accumulate assistant prose for history persistence.
			var assistantText strings.Builder
			res, err := monomind.Exec(ctx, opts, func(ev monomind.Event) {
				emit(ev)
				if ev.Type == monomind.EventAssistant && ev.Text != "" {
					assistantText.WriteString(ev.Text)
					if !strings.HasSuffix(ev.Text, "\n") {
						assistantText.WriteString("\n")
					}
				}
			})
			if err != nil {
				return err
			}
			if res.Err != nil {
				// Error events already streamed; surface the failure on stderr
				// and exit with the turn's protocol code.
				fmt.Fprintf(os.Stderr, "chat: %v\n", res.Err)
				os.Exit(res.Err.ExitCode)
			}
			// Both messages are saved together, after the turn completes,
			// so the (possibly newly-assigned) res.SessionID is known for
			// both — simpler than saving the user turn before running and
			// risking it never getting tagged with the right session.
			if store != nil && effectiveHistoryID != "" {
				if err := store.SaveChatMessage(ai.ChatMessage{
					ID:         uuid.NewString(),
					WorkflowID: effectiveHistoryID,
					Role:       "user",
					Content:    prompt,
					ProviderID: runtime,
					Model:      model,
					SessionID:  res.SessionID,
				}); err != nil {
					fmt.Fprintf(os.Stderr, "warning: saving chat message: %v\n", err)
				}
				final := res.ResultText
				if final == "" {
					final = assistantText.String()
				}
				if final != "" {
					if err := store.SaveChatMessage(ai.ChatMessage{
						ID:         uuid.NewString(),
						WorkflowID: effectiveHistoryID,
						Role:       "assistant",
						Content:    final,
						ProviderID: runtime,
						Model:      model,
						SessionID:  res.SessionID,
					}); err != nil {
						fmt.Fprintf(os.Stderr, "warning: saving chat message: %v\n", err)
					}
				}
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&runtime, "runtime", "", "Agent runtime id (claude, codex, kimi, … — see `agent scan`)")
	cmd.Flags().StringVar(&model, "model", "", "Model override for the runtime")
	cmd.Flags().StringVar(&resume, "resume", "", "Session/thread id to resume (from the session event)")
	cmd.Flags().StringVar(&canvasID, "canvas", "", "Workflow-builder mode for this workflow id")
	cmd.Flags().StringVar(&historyID, "history-id", "", "Persistence/session bucket key (defaults to --canvas's id when unset)")
	cmd.Flags().StringVar(&tools, "tools", "", `Tool surface to enable: "monoagent" gives the agent access to workflows, vault, people, actions, communications (composable with --canvas)`)
	cmd.Flags().StringVar(&timeoutS, "timeout", "", "Overall timeout (e.g. 90s, 10m)")
	cmd.Flags().Float64Var(&budget, "budget-usd", 0, "Spend cap for this turn")
	return cmd
}

// canvasSystemPrompt rebuilds the workflow-builder prompt with a node
// catalog generated from the live node registry — it cannot drift from the
// registry the way a hardcoded list can.
func canvasSystemPrompt(workflowID string, types []aichat.NodeTypeInfo) string {
	var b strings.Builder
	b.WriteString(`You are a workflow builder AI inside Mono Agent. You ONLY communicate by calling tool functions. NEVER describe what you would do — ALWAYS call the tools directly.

RULES:
1. If workflow_id is "general" or "draft", call create_workflow FIRST.
2. Then call create_nodes with all needed nodes in ONE call.
3. Then call connect_nodes for each connection.
4. Respond with a brief summary ONLY after all tool calls are done.
5. Use "main" as source_handle and target_handle for connections.
6. Space nodes: increment position_x by 250 per node.

NODE TYPES (use these exact type values in create_nodes):
`)
	byCat := map[string][]string{}
	for _, t := range types {
		byCat[t.Category] = append(byCat[t.Category], t.Type)
	}
	cats := make([]string, 0, len(byCat))
	for c := range byCat {
		cats = append(cats, c)
	}
	sort.Strings(cats)
	for _, c := range cats {
		list := byCat[c]
		sort.Strings(list)
		fmt.Fprintf(&b, "%s: %s\n", c, strings.Join(list, ", "))
	}
	fmt.Fprintf(&b, "\nCurrent workflow_id: %s", workflowID)
	return b.String()
}

// canvasToolSpecs converts the CanvasTools OpenAI-shaped definitions to
// protocol ToolSpecs (§4.1) — schemas pass through as JSON Schema.
func canvasToolSpecs(ct *aichat.CanvasTools) []monomind.ToolSpec {
	defs := ct.ToolDefs()
	specs := make([]monomind.ToolSpec, 0, len(defs))
	for _, d := range defs {
		schema, _ := d.Function.Parameters.(map[string]interface{})
		specs = append(specs, monomind.ToolSpec{
			Name:        d.Function.Name,
			Description: d.Function.Description,
			Schema:      schema,
		})
	}
	return specs
}

// monoagentSystemPrompt tells the model it has real tool access into the
// rest of monoagent, complementing the static guidance already sitting in
// the project root's CLAUDE.md/AGENTS.md (monomind.EnsureProjectRoot).
func monoagentSystemPrompt(canvasAvailable bool) string {
	s := `You have tool access into this monoagent installation: workflows, ` +
		`the vault, people, actions, communications, lists, and templates. ` +
		`Call the tools directly rather than describing what you would do. ` +
		`list_workflows/list_people/list_actions/etc return real current data — ` +
		`use them instead of guessing IDs. Credential values are never exposed to ` +
		`you; add_secret returns a reference token for use in workflow node ` +
		`configs instead. run_workflow and run_action require an explicit ` +
		`confirm:true argument — without it they only describe what would run, ` +
		`since they can drive real automation against real accounts.`
	if canvasAvailable {
		s += ` You can also build new automation workflows: call create_workflow, ` +
			`then create_nodes and connect_nodes to add its steps — call ` +
			`list_available_nodes first if you're not sure of an exact node type ` +
			`string. Only build a workflow when the user actually asks for one; ` +
			`for ordinary questions just answer directly.`
	}
	return s
}

// monoagentToolSpecs converts MonoagentTools' OpenAI-shaped definitions to
// protocol ToolSpecs — mirrors canvasToolSpecs above.
func monoagentToolSpecs(mt *aichat.MonoagentTools) []monomind.ToolSpec {
	defs := mt.ToolDefs()
	specs := make([]monomind.ToolSpec, 0, len(defs))
	for _, d := range defs {
		schema, _ := d.Function.Parameters.(map[string]interface{})
		specs = append(specs, monomind.ToolSpec{
			Name:        d.Function.Name,
			Description: d.Function.Description,
			Schema:      schema,
		})
	}
	return specs
}

// registryNodeTypes derives the canvas node catalog from the node registry.
func registryNodeTypes(db *sql.DB) []aichat.NodeTypeInfo {
	reg := noderegistry.Build(db)
	var out []aichat.NodeTypeInfo
	for _, t := range reg.Types() {
		parts := strings.SplitN(t, ".", 2)
		cat := "other"
		if len(parts) == 2 {
			cat = parts[0]
		}
		out = append(out, aichat.NodeTypeInfo{Type: t, Label: t, Category: cat, Description: ""})
	}
	return out
}
