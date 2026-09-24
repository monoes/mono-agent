package main

import (
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

// isMonomindInitializedAt reports whether root was set up by `monomind
// init` (see monomind.IsInitializedAt).
func isMonomindInitializedAt(root string) bool { return monomind.IsInitializedAt(root) }

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
// profile's folder (monomind.InitProfile — the same routine `monoagentcli
// doctor fix monomind.profile_init` uses), streaming progress via
// monomind:initProgress events and returning immediately (fire-and-forget,
// mirroring StreamAgentChat in app_ai.go) rather than blocking the Wails
// call for up to 10 minutes.
func (a *App) InitializeMonomindProfile() string {
	root := profiledir.Root(a.db, a.getActiveProfileID())
	go func() {
		err := monomind.InitProfile(a.ctx, monomind.InitOptions{
			Root:     root,
			Progress: func(line string) { a.emitMonomindInitEvent("line", line) },
			Prepare:  hideWindow,
		})
		if err != nil {
			a.emitMonomindInitEvent("error", err.Error())
			return
		}
		a.emitMonomindInitEvent("done", "")
	}()
	return `{"ok":true}`
}
