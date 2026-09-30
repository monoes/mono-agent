package main

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/spf13/cobra"

	"github.com/monoes/mono-agent/internal/ai"
	"github.com/monoes/mono-agent/internal/ai/chatevents"
	"github.com/monoes/mono-agent/internal/dynorg"
)

// `chat history …` reads and writes the desktop chat's conversation, turn
// and event journal (ai_chat_conversations/turns/events). It sits under one
// subcommand word so it takes as little as possible from `chat <prompt>`:
// only a prompt whose first unquoted word is exactly "history" routes here,
// and `chat -- history …` (what the app passes) never does.

// openChatHistory opens the DB once and returns the store and the resolved
// profile id (initDB resolves --profile or the active profile).
func openChatHistory(cfg *globalConfig) (*ai.AIStore, string, func(), error) {
	db, err := initDB(cfg)
	if err != nil {
		return nil, "", nil, fmt.Errorf("initializing database: %w", err)
	}
	store, err := ai.NewAIStore(db.DB)
	if err != nil {
		db.Close()
		return nil, "", nil, fmt.Errorf("initializing AI store: %w", err)
	}
	return store, cfg.ProfileID, func() { db.Close() }, nil
}

// chatStoreErr maps the chat store's sentinels to CLI exit codes.
func chatStoreErr(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, ai.ErrConversationNotFound), errors.Is(err, ai.ErrTurnNotFound):
		return errNotFound("%v", err)
	case errors.Is(err, ai.ErrTurnActive):
		return errInvalidInput("%v: stop the turn before deleting the conversation", err)
	case errors.Is(err, ai.ErrTurnOwnedByOtherInstance):
		return errInvalidInput("%v", err)
	}
	return err
}

// chatLimit validates a --limit value: 0 means def, above max is refused.
func chatLimit(limit, def, max int) (int, error) {
	if limit == 0 {
		return def, nil
	}
	if limit < 0 || limit > max {
		return 0, errInvalidInput("--limit must be between 1 and %d", max)
	}
	return limit, nil
}

// turnInConversation returns turnID, or not-found when it belongs to a
// different conversation (or profile).
func turnInConversation(store *ai.AIStore, profileID, conversationID, turnID string) (ai.Turn, error) {
	t, err := store.GetTurn(turnID, profileID)
	if err != nil {
		return ai.Turn{}, chatStoreErr(err)
	}
	if t.ConversationID != conversationID {
		return ai.Turn{}, errNotFound("%v", ai.ErrTurnNotFound)
	}
	return t, nil
}

type chatConversationPage struct {
	Items      []ai.ConversationRecord `json:"items"`
	NextCursor string                  `json:"next_cursor"`
}

type chatTurnPage struct {
	Items      []ai.TurnRecord `json:"items"`
	NextCursor string          `json:"next_cursor"`
}

type chatEventPage struct {
	Items            []chatevents.Record `json:"items"`
	Turn             ai.TurnRecord       `json:"turn"`
	LastCommittedSeq int64               `json:"last_committed_seq"`
	HasMore          bool                `json:"has_more"`
}

type chatFinishResult struct {
	Finalized bool               `json:"finalized"`
	Event     *chatevents.Record `json:"event"`
	Turn      ai.TurnRecord      `json:"turn"`
}

type chatReconcileResult struct {
	Reconciled []ai.TurnRecord `json:"reconciled"`
	Errors     []string        `json:"errors"`
	// Worktrees are the isolated org writers' worktrees of turns no longer
	// active that were cleaned up, with the branches kept because their
	// work was never merged (#230).
	Worktrees []dynorg.TreeCleanup `json:"worktrees"`
}

func newChatHistoryCmd(cfg *globalConfig) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "history",
		Short: "Read and manage the desktop chat's conversations, turns and events",
		Long: "The desktop chat's history: conversations (one runtime and model each), their turns, " +
			"and each turn's event journal. Everything except reconcile is scoped to the active " +
			"profile (or --profile). A turn runs with `chat --conversation <id> --turn <id> -- <prompt>`.",
		Example: `  monoagentcli --json chat history list
  monoagentcli --json chat history create --runtime claude --model sonnet
  monoagentcli --json chat history turns <conversation-id>
  monoagentcli --json chat history events <conversation-id> <turn-id> --after-seq 12`,
	}
	cmd.AddCommand(
		newChatHistoryListCmd(cfg),
		newChatHistoryShowCmd(cfg),
		newChatHistoryCreateCmd(cfg),
		newChatHistoryTurnsCmd(cfg),
		newChatHistoryTurnCmd(cfg),
		newChatHistoryEventsCmd(cfg),
		newChatHistoryDeleteCmd(cfg),
		newChatHistoryFinishCmd(cfg),
		newChatHistoryReconcileCmd(cfg),
		newChatHistoryTranscriptCmd(cfg),
		newChatHistorySetOrgCmd(cfg),
	)
	return cmd
}

func newChatHistoryListCmd(cfg *globalConfig) *cobra.Command {
	var cursor string
	var limit int
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List conversations, most recently updated first",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			n, err := chatLimit(limit, 50, 200)
			if err != nil {
				return err
			}
			store, profileID, closeDB, err := openChatHistory(cfg)
			if err != nil {
				return err
			}
			defer closeDB()
			convs, next, err := store.ListConversations(profileID, cursor, n)
			if err != nil {
				return err
			}
			page := chatConversationPage{Items: []ai.ConversationRecord{}, NextCursor: next}
			for _, c := range convs {
				page.Items = append(page.Items, c.Record())
			}
			if cfg.JSONOutput {
				return printJSON(page)
			}
			for _, c := range page.Items {
				fmt.Printf("%s  %-8s %-10s %-12s %s\n", c.ID, c.Backend, c.RuntimeID, c.WorkflowContext, c.UpdatedAt)
			}
			if next != "" {
				fmt.Printf("more: --cursor %q\n", next)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&cursor, "cursor", "", "next_cursor from the previous page")
	cmd.Flags().IntVar(&limit, "limit", 0, "Page size (default 50, max 200)")
	return cmd
}

func newChatHistoryShowCmd(cfg *globalConfig) *cobra.Command {
	return &cobra.Command{
		Use:   "show <conversation-id>",
		Short: "Show one conversation",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			store, profileID, closeDB, err := openChatHistory(cfg)
			if err != nil {
				return err
			}
			defer closeDB()
			conv, err := store.GetConversation(args[0], profileID)
			if err != nil {
				return chatStoreErr(err)
			}
			return printConversation(cfg, conv.Record())
		},
	}
}

func printConversation(cfg *globalConfig, c ai.ConversationRecord) error {
	if cfg.JSONOutput {
		return printJSON(c)
	}
	fmt.Printf("id:        %s\nbackend:   %s\nruntime:   %s\nmodel:     %s\neffort:    %s\nworkflow:  %s\nsession:   %s\ncreated:   %s\nupdated:   %s\n",
		c.ID, c.Backend, c.RuntimeID, c.Model, c.Effort, c.WorkflowContext, c.SessionID, c.CreatedAt, c.UpdatedAt)
	return nil
}

func newChatHistoryCreateCmd(cfg *globalConfig) *cobra.Command {
	var runtimeID, model, effort, workflowID, mode, cwd string
	var newWorkspace, coderRoot bool
	var orgMode string
	cmd := &cobra.Command{
		Use:   "create",
		Short: "Create an agent conversation",
		Long: "Create an agent conversation. --workflow is the tool and ownership context: " +
			`"general" (the default), "draft", or a workflow id.` + "\n\n" +
			"--mode coder creates a full-access conversation (see `coder`) that works in --cwd, or in a " +
			"--coder-root (the coder root folder itself, shared), or a fresh test folder with --new-workspace. " +
			"The folder is fixed for the conversation's life.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if mode == ai.ModeCoder {
				if orgMode != ai.OrgModeSolo && orgMode != ai.OrgModeDynamic {
					return errInvalidInput("--org must be solo or dynamic")
				}
				conv, err := createCoderConversation(cmd, cfg, runtimeID, model, effort, workflowID, coderFolderChoice{cwd: cwd, root: coderRoot, newWorkspace: newWorkspace})
				if err != nil {
					return err
				}
				if orgMode == ai.OrgModeDynamic {
					if conv, err = setOrgMode(cfg, conv.ID, orgMode); err != nil {
						return err
					}
				}
				return printConversation(cfg, conv.Record())
			}
			if runtimeID == "" {
				return errInvalidInput("--runtime is required (see `agent scan --installed`)")
			}
			if cmd.Flags().Changed("org") {
				return errInvalidInput("--org only applies to --mode coder")
			}
			switch mode {
			case ai.ModeAssistant:
				if cwd != "" || newWorkspace || coderRoot {
					return errInvalidInput("--cwd, --coder-root and --new-workspace only apply to --mode coder")
				}
			default:
				return errInvalidInput("unknown --mode %q (assistant or coder)", mode)
			}
			store, profileID, closeDB, err := openChatHistory(cfg)
			if err != nil {
				return err
			}
			defer closeDB()
			conv, err := store.CreateConversationEffort(profileID, "agent", workflowID, runtimeID, "", model, effort)
			if err != nil {
				return err
			}
			return printConversation(cfg, conv.Record())
		},
	}
	cmd.Flags().StringVar(&runtimeID, "runtime", "", "Agent runtime id (claude, codex, …); --mode coder defaults to claude (see `coder status` for the ready ones)")
	cmd.Flags().StringVar(&model, "model", "", "Model for the runtime")
	cmd.Flags().StringVar(&effort, "effort", "", "Reasoning effort level for the model (e.g. low, medium, high, max)")
	cmd.Flags().StringVar(&workflowID, "workflow", "general", "Workflow context")
	cmd.Flags().StringVar(&mode, "mode", ai.ModeAssistant, "assistant, or coder for a full-access conversation")
	cmd.Flags().StringVar(&cwd, "cwd", "", "Coder mode: the folder the agent works in (any existing folder)")
	cmd.Flags().BoolVar(&coderRoot, "coder-root", false, "Coder mode: work in the coder root folder itself (see `coder set --workspace-root`)")
	cmd.Flags().BoolVar(&newWorkspace, "new-workspace", false, "Coder mode: work in a fresh, randomly named test folder")
	cmd.Flags().StringVar(&orgMode, "org", ai.OrgModeSolo, "Coder mode: solo, or dynamic to let the agent spawn worker agents")
	withJSONErrors(cfg, cmd)
	return cmd
}

func newChatHistoryTurnsCmd(cfg *globalConfig) *cobra.Command {
	var cursor string
	var limit int
	cmd := &cobra.Command{
		Use:   "turns <conversation-id>",
		Short: "List a conversation's turns, newest first",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			n, err := chatLimit(limit, 50, 200)
			if err != nil {
				return err
			}
			store, profileID, closeDB, err := openChatHistory(cfg)
			if err != nil {
				return err
			}
			defer closeDB()
			if _, err := store.GetConversation(args[0], profileID); err != nil {
				return chatStoreErr(err)
			}
			turns, next, err := store.ListTurns(args[0], profileID, cursor, n)
			if err != nil {
				return err
			}
			page := chatTurnPage{Items: []ai.TurnRecord{}, NextCursor: next}
			for _, t := range turns {
				page.Items = append(page.Items, t.Record())
			}
			if cfg.JSONOutput {
				return printJSON(page)
			}
			for _, t := range page.Items {
				fmt.Printf("%s  %-11s %s  %s\n", t.ID, t.Status, t.CreatedAt, firstLineOf(t.Prompt))
			}
			if next != "" {
				fmt.Printf("more: --cursor %q\n", next)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&cursor, "cursor", "", "next_cursor from the previous page")
	cmd.Flags().IntVar(&limit, "limit", 0, "Page size (default 50, max 200)")
	return cmd
}

func newChatHistoryTurnCmd(cfg *globalConfig) *cobra.Command {
	return &cobra.Command{
		Use:   "turn <conversation-id> <turn-id>",
		Short: "Show one turn",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			store, profileID, closeDB, err := openChatHistory(cfg)
			if err != nil {
				return err
			}
			defer closeDB()
			t, err := turnInConversation(store, profileID, args[0], args[1])
			if err != nil {
				return err
			}
			if cfg.JSONOutput {
				return printJSON(t.Record())
			}
			fmt.Printf("id:       %s\nstatus:   %s\nreason:   %s\nlast seq: %d\ncreated:  %s\nprompt:   %s\n",
				t.ID, t.Status, t.Reason, t.LastCommittedSeq, t.CreatedAt, t.Prompt)
			return nil
		},
	}
}

func newChatHistoryEventsCmd(cfg *globalConfig) *cobra.Command {
	var afterSeq int64
	var limit int
	var agent string
	cmd := &cobra.Command{
		Use:   "events <conversation-id> <turn-id>",
		Short: "Show a turn's events after --after-seq, oldest first, with the turn's status",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			n, err := chatLimit(limit, 500, 1000)
			if err != nil {
				return err
			}
			if afterSeq < 0 {
				return errInvalidInput("--after-seq must not be negative")
			}
			store, profileID, closeDB, err := openChatHistory(cfg)
			if err != nil {
				return err
			}
			defer closeDB()
			t, err := turnInConversation(store, profileID, args[0], args[1])
			if err != nil {
				return err
			}
			evs, err := store.GetEvents(args[0], args[1], profileID, afterSeq, n)
			if err != nil {
				return err
			}
			page := chatEventPage{Items: []chatevents.Record{}, Turn: t.Record(), LastCommittedSeq: t.LastCommittedSeq, HasMore: len(evs) == n}
			for _, ev := range evs {
				// --agent keeps one dynamic-org agent's events; has_more
				// and paging still follow the unfiltered page.
				if r := ev.Record(); agent == "" || eventAgentID(r) == agent {
					page.Items = append(page.Items, r)
				}
			}
			if cfg.JSONOutput {
				return printJSON(page)
			}
			for _, ev := range page.Items {
				fmt.Printf("%4d  %s  %-15s %s\n", ev.Seq, ev.At, ev.Type, ev.Payload)
			}
			fmt.Printf("turn %s: %s (last seq %d)\n", t.ID, t.Status, t.LastCommittedSeq)
			return nil
		},
	}
	cmd.Flags().Int64Var(&afterSeq, "after-seq", 0, "Only events with a higher seq")
	cmd.Flags().IntVar(&limit, "limit", 0, "Maximum events (default 500, max 1000); has_more is true when a full page came back")
	cmd.Flags().StringVar(&agent, "agent", "", "Dynamic org: only this agent's events (a worker id such as w1, or lead)")
	return cmd
}

func newChatHistoryDeleteCmd(cfg *globalConfig) *cobra.Command {
	return &cobra.Command{
		Use:   "delete <conversation-id>",
		Short: "Delete a conversation with its turns and events (refused while a turn is active)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			store, profileID, closeDB, err := openChatHistory(cfg)
			if err != nil {
				return err
			}
			defer closeDB()
			if err := store.DeleteConversation(args[0], profileID); err != nil {
				return chatStoreErr(err)
			}
			if cfg.JSONOutput {
				return printJSON(map[string]any{"id": args[0], "deleted": true})
			}
			fmt.Printf("deleted conversation %s\n", args[0])
			return nil
		},
	}
}

// parseTurnStatus accepts only the terminal statuses.
func parseTurnStatus(s string) (chatevents.TurnStatus, error) {
	switch st := chatevents.TurnStatus(s); st {
	case chatevents.StatusCompleted, chatevents.StatusFailed, chatevents.StatusCancelled, chatevents.StatusInterrupted:
		return st, nil
	}
	return "", errInvalidInput("--status must be completed, failed, cancelled or interrupted (got %q)", s)
}

func newChatHistoryFinishCmd(cfg *globalConfig) *cobra.Command {
	var status, reason, exitCode string
	cmd := &cobra.Command{
		Use:   "finish <conversation-id> <turn-id>",
		Short: "Finalize an active turn whose runner died, recording its turn.finished event",
		Long: "Moves an active turn to a terminal status and journals its turn.finished event, once: " +
			"a turn that is already finished is left alone (finalized: false). The desktop app calls " +
			"this when a `chat --turn` process it stopped or lost exits without finishing its own turn.",
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			st, err := parseTurnStatus(status)
			if err != nil {
				return err
			}
			var code *int
			if exitCode != "" {
				v, err := strconv.Atoi(exitCode)
				if err != nil {
					return errInvalidInput("--exit-code must be an integer")
				}
				code = &v
			}
			store, profileID, closeDB, err := openChatHistory(cfg)
			if err != nil {
				return err
			}
			defer closeDB()
			if _, err := turnInConversation(store, profileID, args[0], args[1]); err != nil {
				return err
			}
			ev, already, err := store.FinalizeTurn(profileID, args[0], args[1], st, reason, code, true)
			if err != nil {
				return err
			}
			t, err := store.GetTurn(args[1], profileID)
			if err != nil {
				return chatStoreErr(err)
			}
			res := chatFinishResult{Finalized: !already, Turn: t.Record()}
			if !already {
				rec := ev.Record()
				res.Event = &rec
			}
			if cfg.JSONOutput {
				return printJSON(res)
			}
			if already {
				fmt.Printf("turn %s was already %s\n", t.ID, t.Status)
			} else {
				fmt.Printf("turn %s: %s\n", t.ID, t.Status)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&status, "status", "", "Terminal status: completed, failed, cancelled or interrupted")
	cmd.Flags().StringVar(&reason, "reason", "", "Human-readable reason")
	cmd.Flags().StringVar(&exitCode, "exit-code", "", "Process exit code to record")
	return cmd
}

func newChatHistoryReconcileCmd(cfg *globalConfig) *cobra.Command {
	var exceptOwner string
	cmd := &cobra.Command{
		Use:   "reconcile",
		Short: "Mark every turn left active, in every profile, as interrupted",
		Long: "Marks every turn still active as interrupted, across all profiles, and removes the " +
			"`chat turn stop` mailbox folders of turn processes that are gone. The desktop app runs " +
			"this at startup with --except-owner set to its own instance id, so turns it has already " +
			"started are skipped. There is no liveness check: a second running app's turns are " +
			"interrupted too.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			store, _, closeDB, err := openChatHistory(cfg)
			if err != nil {
				return err
			}
			defer closeDB()
			turns, errs := store.ReconcileActiveTurns(exceptOwner)
			// Mailbox folders of turns whose process died (#255).
			sweepAgentControl(agentControlRoot(cfg), time.Now())
			res := chatReconcileResult{Reconciled: []ai.TurnRecord{}, Errors: []string{}, Worktrees: reconcileWorktrees(cmd.Context(), store)}
			for _, t := range turns {
				res.Reconciled = append(res.Reconciled, t.Record())
			}
			for _, e := range errs {
				res.Errors = append(res.Errors, e.Error())
			}
			if cfg.JSONOutput {
				if err := printJSON(res); err != nil {
					return err
				}
			} else {
				fmt.Printf("interrupted %d turn(s)\n", len(res.Reconciled))
				for _, w := range res.Worktrees {
					switch {
					case w.Error != "":
						fmt.Printf("kept worktree %s: %s\n", w.Path, w.Error)
					case w.BranchKept:
						fmt.Printf("removed worktree %s; kept unmerged branch %s\n", w.Path, w.Branch)
					}
				}
			}
			if len(errs) > 0 {
				return fmt.Errorf("reconcile: %d turn(s) failed: %s", len(errs), errs[0])
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&exceptOwner, "except-owner", "", "Skip turns owned by this app instance id")
	return cmd
}

func newChatHistoryTranscriptCmd(cfg *globalConfig) *cobra.Command {
	var byAgent bool
	cmd := &cobra.Command{
		Use:   "transcript <history-id> | --by-agent <conversation-id> <turn-id>",
		Short: "Show the legacy transcript `chat --history-id`/`--canvas` saved, or a dynamic-org turn by agent",
		Long: "Shows the messages a plain `chat --history-id <id>` (or `--canvas <id>`) turn saved to the " +
			"legacy ai_chat_messages transcript, oldest first. Conversations run through " +
			"`chat --conversation` are journaled as events instead and never appear here.\n\n" +
			"With --by-agent it shows a journaled dynamic-org turn split by agent instead: the lead, then " +
			"each worker with its role, model and status, the briefs and reports it exchanged, and the " +
			"text it wrote.",
		Args: func(cmd *cobra.Command, args []string) error {
			if byAgent {
				return cobra.ExactArgs(2)(cmd, args)
			}
			return cobra.ExactArgs(1)(cmd, args)
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			store, profileID, closeDB, err := openChatHistory(cfg)
			if err != nil {
				return err
			}
			defer closeDB()
			if byAgent {
				if _, err := turnInConversation(store, profileID, args[0], args[1]); err != nil {
					return err
				}
				recs, err := allTurnEvents(store, profileID, args[0], args[1])
				if err != nil {
					return err
				}
				items := agentTranscripts(recs)
				if cfg.JSONOutput {
					return printJSON(map[string]any{"items": items})
				}
				printAgentTranscripts(items)
				return nil
			}
			msgs, err := store.GetChatHistory(args[0], profileID)
			if err != nil {
				return err
			}
			if msgs == nil {
				msgs = []ai.ChatMessage{}
			}
			if cfg.JSONOutput {
				return printJSON(map[string]any{"items": msgs})
			}
			for _, m := range msgs {
				fmt.Printf("[%s] %s: %s\n", m.CreatedAt, m.Role, m.Content)
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&byAgent, "by-agent", false, "Show a journaled dynamic-org turn (<conversation-id> <turn-id>) split by agent")
	return cmd
}

func firstLineOf(s string) string {
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			return s[:i]
		}
	}
	return s
}

// newChatHistorySetOrgCmd switches a coder conversation between working
// alone and a dynamic org (#226), from its next turn on.
func newChatHistorySetOrgCmd(cfg *globalConfig) *cobra.Command {
	return &cobra.Command{
		Use:   "set-org <conversation-id> solo|dynamic",
		Short: "Let a coder conversation's agent spawn worker agents (dynamic) or work alone (solo)",
		Long: "In a dynamic org the coder chat's agent gets org tools to spawn worker agents, each with its own " +
			"role, skills, model and access, within the limits in `coder set --org-*`. It takes effect from the " +
			"next turn. The runtime must take caller tools with full access (see `agent scan --json`).",
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			conv, err := setOrgMode(cfg, args[0], args[1])
			if err != nil {
				return err
			}
			if cfg.JSONOutput {
				return printConversation(cfg, conv.Record())
			}
			fmt.Printf("conversation %s: org %s\n", conv.ID, conv.OrgMode)
			return nil
		},
	}
}

// setOrgMode stores a coder conversation's org mode.
func setOrgMode(cfg *globalConfig, id, mode string) (ai.Conversation, error) {
	if mode != ai.OrgModeSolo && mode != ai.OrgModeDynamic {
		return ai.Conversation{}, errInvalidInput("org mode must be solo or dynamic (got %q)", mode)
	}
	store, profileID, closeDB, err := openChatHistory(cfg)
	if err != nil {
		return ai.Conversation{}, err
	}
	defer closeDB()
	conv, err := store.GetConversation(id, profileID)
	if err != nil {
		return ai.Conversation{}, chatStoreErr(err)
	}
	if conv.Mode != ai.ModeCoder {
		return ai.Conversation{}, errInvalidInput("conversation %s is not a coder conversation; only coder chats can run a dynamic org", id)
	}
	if err := store.SetConversationOrgMode(id, profileID, mode); err != nil {
		return ai.Conversation{}, chatStoreErr(err)
	}
	conv.OrgMode = mode
	return conv, nil
}

// reconcileWorktrees cleans up, in every coder folder, the worktrees of
// isolated org writers whose turn is no longer active (#230).
func reconcileWorktrees(ctx context.Context, store *ai.AIStore) []dynorg.TreeCleanup {
	out := []dynorg.TreeCleanup{}
	active, err := activeTurnIDs(store)
	if err != nil {
		return out
	}
	folders, err := store.CoderFolders()
	if err != nil {
		return out
	}
	for _, cwd := range folders {
		out = append(out, dynorg.ReconcileWorktrees(ctx, cwd, func(id string) bool { return active[id] })...)
	}
	return out
}
