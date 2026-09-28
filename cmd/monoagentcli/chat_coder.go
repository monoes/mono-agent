package main

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/monoes/mono-agent/internal/ai"
	"github.com/monoes/mono-agent/internal/ai/chatevents"
	"github.com/monoes/mono-agent/internal/monomind"
)

// coderTurn is one full-access turn: the agent runs as a Claude Code session
// in cwd with the user's normal setup loaded and no tool restrictions.
type coderTurn struct {
	prompt string
	model  string
	resume string
	cwd    string
}

// coderSystemPrompt is appended to Claude Code's own system prompt (not a
// replacement: monomind keeps the preset with --settings).
func coderSystemPrompt(cwd string) string {
	return "You are running as mono-agent's coder chat: a full Claude Code session with unrestricted " +
		"access to this computer, working in " + cwd + ". The user sees every tool call you make " +
		"(commands, edits, file writes) live in the chat. Work inside that folder unless the user " +
		"asks for something elsewhere, and say plainly what you changed."
}

// runCoderTurn runs t through monomind with full access. journal is nil for
// a plain `chat --mode coder` turn, which prints raw events instead.
func runCoderTurn(cmd *cobra.Command, cfg *globalConfig, journal *turnJournal, t coderTurn) error {
	ctx := cmd.Context()
	db, err := initDB(cfg)
	if err != nil {
		return fmt.Errorf("initializing database: %w", err)
	}
	settings, err := requireCoderReady(cmd, db.DB)
	db.Close()
	if err != nil {
		return err
	}
	if fi, err := os.Stat(t.cwd); err != nil || !fi.IsDir() {
		return errInvalidInput("this coder conversation's folder %s no longer exists; start a new conversation", t.cwd)
	}
	bin, _, err := monomind.Ensure(ctx)
	if err != nil {
		return err
	}
	opts := monomind.ExecOptions{
		Bin:          bin,
		Runtime:      coderRuntime,
		Prompt:       t.prompt,
		Model:        t.model,
		Resume:       t.resume,
		Cwd:          t.cwd,
		Access:       monomind.AccessFull,
		Settings:     monomind.CoderSettings,
		MaxTurns:     settings.MaxTurns,
		Timeout:      settings.timeout(),
		BudgetUSD:    settings.BudgetUSD,
		SystemPrompt: coderSystemPrompt(t.cwd),
	}
	onEvent := func(ev monomind.Event) {
		b, _ := json.Marshal(ev)
		fmt.Fprintln(cmd.OutOrStdout(), string(b))
	}
	if journal != nil {
		journal.cwd = t.cwd
		journal.notice(noticeCoderWorkspace, "Working in "+t.cwd, chatevents.SeverityInfo)
		onEvent = journal.handle
	}
	res, err := monomind.Exec(ctx, opts, onEvent)
	if journal != nil {
		if err != nil && res == nil {
			res = &monomind.TurnResult{Err: &monomind.ProtocolError{Code: monomind.ErrRunnerError, Message: err.Error()}}
		}
		journal.finish(ctx.Err() != nil, res)
	}
	if err != nil {
		return err
	}
	if res.Err != nil {
		return res.Err
	}
	return nil
}

// coderFolderChoice is where a new coder conversation or plain coder turn
// works: a folder the user picked (--cwd), the coder root itself
// (--coder-root), or a fresh test folder inside it (--new-workspace).
type coderFolderChoice struct {
	cwd          string
	root         bool
	newWorkspace bool
}

func (c coderFolderChoice) set() int {
	n := 0
	for _, on := range []bool{c.cwd != "", c.root, c.newWorkspace} {
		if on {
			n++
		}
	}
	return n
}

// coderFolder resolves c to a ready folder.
func coderFolder(cmd *cobra.Command, cfg *globalConfig, c coderFolderChoice) (string, error) {
	switch c.set() {
	case 0:
		return "", errInvalidInput("coder mode needs a folder: pass --cwd <dir>, --coder-root or --new-workspace")
	case 1:
	default:
		return "", errInvalidInput("--cwd, --coder-root and --new-workspace are alternatives; pass one")
	}
	db, err := initDB(cfg)
	if err != nil {
		return "", fmt.Errorf("initializing database: %w", err)
	}
	defer db.Close()
	settings, err := requireCoderReady(cmd, db.DB)
	if err != nil {
		return "", err
	}
	if c.newWorkspace || c.root {
		setUp := newCoderWorkspace
		if c.root {
			setUp = rootCoderWorkspace
		}
		ws, err := setUp(cmd.Context(), expandHome(settings.WorkspaceRoot))
		if err != nil {
			return "", err
		}
		return ws.Path, nil
	}
	dir, err := resolveCoderCwd(c.cwd)
	if err != nil {
		return "", err
	}
	// A folder the user picked may be their own repo: add only what is
	// missing, never touch what is there.
	if _, err := initWorkspace(cmd.Context(), "", dir); err != nil {
		return "", err
	}
	return dir, nil
}

// createCoderConversation is `chat history create --mode coder`.
func createCoderConversation(cmd *cobra.Command, cfg *globalConfig, runtimeID, model, workflowID string, folder coderFolderChoice) (ai.Conversation, error) {
	if runtimeID != coderRuntime {
		return ai.Conversation{}, errInvalidInput("coder mode runs on the %s runtime only (got %q)", coderRuntime, runtimeID)
	}
	dir, err := coderFolder(cmd, cfg, folder)
	if err != nil {
		return ai.Conversation{}, err
	}
	store, profileID, closeDB, err := openChatHistory(cfg)
	if err != nil {
		return ai.Conversation{}, err
	}
	defer closeDB()
	return store.CreateConversationMode(profileID, "agent", workflowID, runtimeID, "", model, ai.ModeCoder, dir)
}
