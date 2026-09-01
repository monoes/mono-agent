package main

import (
	"bufio"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/wailsapp/wails/v2/pkg/runtime"

	"github.com/monoes/mono-agent/internal/monomind"
	"github.com/monoes/mono-agent/internal/profiledir"
)

// ─────────────────────────────────────────────────────────────────────────────
// Per-profile monomind initialization — distinct from app_orgs.go's
// "always shell to monoagentcli" doctrine (which is about not reaching into
// monomind's TypeScript internals from Go), this shells to the monomind
// binary directly via internal/monomind.Find(), the same discovery ladder
// already used elsewhere (e.g. cmd/monoagentcli/crashreport.go). Adding a
// monoagentcli subcommand just to immediately re-shell to the same binary
// would be an unnecessary hop for a feature monoagentcli otherwise has no
// involvement in.
// ─────────────────────────────────────────────────────────────────────────────

// monomindInitTimeout is generous on purpose: init copies skill/command
// files and may shell out further (npx) itself — minutes, not seconds,
// unlike the 60s org-CLI timeout.
const monomindInitTimeout = 10 * time.Minute

// isMonomindInitializedAt is the pure check, split out from
// IsMonomindInitialized so it's testable without an *App/*sql.DB — checks
// the same on-disk marker monomind's own CLI uses (.monomind/config.yaml).
func isMonomindInitializedAt(root string) bool {
	_, err := os.Stat(filepath.Join(root, ".monomind", "config.yaml"))
	return err == nil
}

// IsMonomindInitialized reports whether the active profile's folder has
// already been set up by `monomind init` — a direct file check, not a
// subprocess call, since this is checked on every Orgs/Agents tab load.
func (a *App) IsMonomindInitialized() bool {
	return isMonomindInitializedAt(profiledir.Root(a.db, a.getActiveProfileID()))
}

// emitMonomindInitEvent reports init progress to the frontend. kind is
// "line" (a streamed stdout/stderr line), "error" (init failed — message is
// the error text), or "done" (init finished successfully).
func (a *App) emitMonomindInitEvent(kind, message string) {
	runtime.EventsEmit(a.ctx, "monomind:initProgress", map[string]interface{}{
		"kind":    kind,
		"message": message,
	})
}

// InitializeMonomindProfile runs `monomind init` scoped to the active
// profile's folder, streaming progress via monomind:initProgress events and
// returning immediately (fire-and-forget, mirroring StreamAgentChat in
// app_ai.go) rather than blocking the Wails call for up to 10 minutes.
func (a *App) InitializeMonomindProfile() string {
	bin, err := monomind.Find()
	if err != nil {
		a.emitMonomindInitEvent("error", err.Error())
		return `{"ok":true}`
	}
	root := profiledir.Root(a.db, a.getActiveProfileID())

	go func() {
		ctx, cancel := context.WithTimeout(a.ctx, monomindInitTimeout)
		defer cancel()

		// --yes suppresses the "already initialized, reinitialize?" prompt;
		// --no-watch avoids leaving a background monograph watcher process
		// running from a single GUI click; --no-install skips a potential
		// global `npm install -g @anthropic-ai/claude-code`. CI=true is
		// belt-and-braces on top of --yes: monomind's own interactive check
		// is `stdin.isTTY ?? false`, already false for an exec.Command child,
		// but CI=true guarantees every prompt path treats this as
		// non-interactive even if that check changes upstream.
		cmd := exec.CommandContext(ctx, bin, "init", "--yes", "--no-watch", "--no-install")
		cmd.Dir = root // monomind init has no --project flag and does not honor MONOMIND_CWD — cwd is the only way to scope it
		cmd.Env = append(os.Environ(), "CI=true")

		stdout, err := cmd.StdoutPipe()
		if err != nil {
			a.emitMonomindInitEvent("error", err.Error())
			return
		}
		cmd.Stderr = cmd.Stdout

		if err := cmd.Start(); err != nil {
			a.emitMonomindInitEvent("error", err.Error())
			return
		}

		sc := bufio.NewScanner(stdout)
		sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
		for sc.Scan() {
			a.emitMonomindInitEvent("line", sc.Text())
		}

		if err := cmd.Wait(); err != nil {
			a.emitMonomindInitEvent("error", err.Error())
			return
		}

		a.registerClaudeCodeProject(root)
		a.emitMonomindInitEvent("done", "")
	}()

	return `{"ok":true}`
}

// registerClaudeCodeProject makes root show up in monomind's own web
// dashboard project list. That list (GET /api/projects in monomind's
// server.mjs) is sourced entirely from ~/.claude/projects/<slug>/ — the
// per-directory session folder Claude Code itself creates the first time
// `claude` runs with that directory as cwd. monomind's own init never
// touches this (it writes to a separate, unrelated ~/.monomind-projects.json
// used only by `init upgrade --all`), so a freshly-initialized profile is
// otherwise invisible in that dashboard until someone happens to open a
// real Claude Code session there by hand. A single lightweight --print
// turn is enough to create the slug directory + a session file — best
// effort: if the `claude` binary isn't on PATH, or the call fails, this is
// reported as a warning line (not an "error" event), since monomind init
// itself already succeeded and this is a secondary nicety, not something
// worth failing the whole "Initiate monomind" action over.
func (a *App) registerClaudeCodeProject(root string) {
	claudeBin, err := exec.LookPath("claude")
	if err != nil {
		a.emitMonomindInitEvent("line", "(skipped: claude CLI not found on PATH — this profile won't appear in monomind's dashboard project list until a Claude Code session is opened here)")
		return
	}
	ctx, cancel := context.WithTimeout(a.ctx, 60*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, claudeBin, "-p", "monomind initialized")
	cmd.Dir = root
	if err := cmd.Run(); err != nil {
		a.emitMonomindInitEvent("line", "(claude CLI registration step failed, non-fatal: "+err.Error()+")")
		return
	}
	a.emitMonomindInitEvent("line", "Registered with Claude Code's project list.")
}
