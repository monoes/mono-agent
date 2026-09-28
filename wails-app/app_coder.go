package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strconv"
	"strings"

	"github.com/wailsapp/wails/v2/pkg/runtime"

	"github.com/monoes/mono-agent/internal/ai"
)

// ─────────────────────────────────────────────────────────────────────────────
// Coder mode (issue #203): a chat conversation in which Claude Code runs with
// full access inside one folder. Everything here shells out to
// `monoagentcli coder …` / `chat history create --mode coder` (#202) and
// returns the CLI's stdout JSON verbatim; the only logic is building args.
// A coder conversation's turns go through StartChatTurn like any other,
// with tools=false: the CLI refuses --tools for them.
// ─────────────────────────────────────────────────────────────────────────────

// jsonCLI runs `monoagentcli [--profile P] --json <args>` and returns its
// stdout. Unlike chatSupervisor.cli it keeps the {"error","code"} object a
// --json failure prints on stdout, so codes such as coder_disabled and
// needs_monomind_update reach the frontend. On failure it still returns the
// stdout it got, for commands whose report is their output even then (org
// validate).
func (a *App) jsonCLI(args ...string) (string, error) {
	if a.chatSup == nil {
		return "", fmt.Errorf("chat supervisor not initialized")
	}
	bin, err := a.chatSup.findCLI()
	if err != nil {
		return "", err
	}
	full := []string{"--json"}
	if p := a.getActiveProfileID(); p != "" {
		full = []string{"--profile", p, "--json"}
	}
	ctx, cancel := context.WithTimeout(context.Background(), chatCLITimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, append(full, args...)...)
	hideWindow(cmd)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	trimmed := strings.TrimSpace(string(out))
	if err != nil {
		var refusal chatRefusal
		_ = json.Unmarshal([]byte(lastLine(string(out))), &refusal)
		msg := refusal.Error
		if msg == "" {
			msg = lastLine(stderr.String())
		}
		if msg == "" {
			msg = err.Error()
		}
		if refusal.Code != "" {
			return trimmed, &codedError{msg: msg, code: refusal.Code}
		}
		return trimmed, errors.New(msg)
	}
	if !looksLikeJSON(trimmed) {
		return "", fmt.Errorf("monoagentcli %s: unexpected output: %q", strings.Join(args, " "), trimmed)
	}
	return trimmed, nil
}

// jsonResult is jsonCLI's stdout, or its error in aiError's shape.
func (a *App) jsonResult(args ...string) string {
	out, err := a.jsonCLI(args...)
	if err != nil {
		return aiError(err)
	}
	return out
}

// CoderStatus returns `coder status`: enabled, workspace root, defaults,
// and whether monomind has the capabilities coder mode needs (ready).
func (a *App) CoderStatus() string { return a.jsonResult("coder", "status") }

// CoderEnable turns coder mode on. The frontend only calls it after the user
// confirmed the risk dialog, which is what --yes-i-understand records.
func (a *App) CoderEnable() string { return a.jsonResult("coder", "enable", "--yes-i-understand") }

func (a *App) CoderDisable() string { return a.jsonResult("coder", "disable") }

// coderSetArgs builds `coder set`. An empty workspace root, maxTurns <= 0 or
// an empty timeout leave that setting unchanged; budgetUsd 0 clears the
// budget and a negative one leaves it unchanged.
func coderSetArgs(workspaceRoot string, maxTurns int, timeout string, budgetUsd float64) []string {
	args := []string{"coder", "set"}
	if workspaceRoot != "" {
		args = append(args, "--workspace-root", workspaceRoot)
	}
	if maxTurns > 0 {
		args = append(args, "--max-turns", strconv.Itoa(maxTurns))
	}
	if timeout != "" {
		args = append(args, "--timeout", timeout)
	}
	if budgetUsd >= 0 {
		args = append(args, "--budget-usd", strconv.FormatFloat(budgetUsd, 'f', -1, 64))
	}
	return args
}

func (a *App) CoderSet(workspaceRoot string, maxTurns int, timeout string, budgetUsd float64) string {
	return a.jsonResult(coderSetArgs(workspaceRoot, maxTurns, timeout, budgetUsd)...)
}

// CoderWorkspaceNew creates and initializes a fresh random folder under the
// workspace root: {path, created, git, init:{created, skipped}}.
func (a *App) CoderWorkspaceNew() string { return a.jsonResult("coder", "workspace", "new") }

// CoderWorkspaceList returns the folders coder conversations used, newest
// first.
func (a *App) CoderWorkspaceList() string { return a.jsonResult("coder", "workspace", "list") }

// CoderStopBackground stops the background processes a coder turn left
// running (its coder.background notice): {stopped, gone, refused} pids. The
// CLI only touches pids that turn reported and that still carry its marker.
func (a *App) CoderStopBackground(conversationID, turnID string) string {
	return a.jsonResult("coder", "stop-background", "--conversation", conversationID, "--turn", turnID)
}

// coderConversationArgs builds `chat history create` for a coder
// conversation: either in cwd, or (newWorkspace) in a folder the CLI
// creates. Coder chats belong to the general assistant's history.
func coderConversationArgs(runtimeID, model, cwd string, newWorkspace bool) []string {
	args := []string{"chat", "history", "create", "--runtime", runtimeID, "--workflow", "general", "--mode", "coder"}
	if newWorkspace {
		args = append(args, "--new-workspace")
	} else {
		args = append(args, "--cwd", cwd)
	}
	if model != "" {
		args = append(args, "--model", model)
	}
	return args
}

// CreateCoderConversation creates a coder conversation and returns it in
// CreateChatConversation's shape (mode "coder", cwd its folder).
func (a *App) CreateCoderConversation(runtimeID, model, cwd string, newWorkspace bool) string {
	if !newWorkspace && cwd == "" {
		return aiError(fmt.Errorf("choose a folder for the coder conversation"))
	}
	out, err := a.jsonCLI(coderConversationArgs(runtimeID, model, cwd, newWorkspace)...)
	if err != nil {
		return aiError(err)
	}
	var rec ai.ConversationRecord
	if err := json.Unmarshal([]byte(out), &rec); err != nil {
		return aiError(fmt.Errorf("monoagentcli chat history create: unexpected output: %w", err))
	}
	b, _ := json.Marshal(rec.Conversation())
	return string(b)
}

// PickCoderFolder opens the native folder picker; "" when cancelled.
func (a *App) PickCoderFolder() string {
	dir, err := runtime.OpenDirectoryDialog(a.ctx, runtime.OpenDialogOptions{Title: "Choose a folder for Coder mode"})
	if err != nil {
		return ""
	}
	return dir
}
