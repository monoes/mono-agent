package openaiapi

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/monoes/mono-agent/internal/agentroster"
	"github.com/monoes/mono-agent/internal/monomind"
)

// turnGrace is how long past the turn timeout the request context lives, so
// monomind's own --timeout fires first and reports a clean error. A variable
// so a test can shorten it.
var turnGrace = 30 * time.Second

// turn is one validated request ready to run.
type turn struct {
	Runtime, Model, Effort string
	System, Prompt         string
	// Policy is the listener's, capped for a context key: the turn is
	// cancelled if its start event reports a confinement the policy does not
	// allow.
	Policy Policy
	// ProfileID is the profile the request authenticated as. The turn works in
	// that profile's folder, so no two profiles share one.
	ProfileID string
	// Slot is the limiter slot the turn holds. Its folder inside the
	// profile's is the turn's working directory, and nothing else runs in it
	// meanwhile.
	Slot int
	// RequireSandbox makes Exec refuse to start the turn when the sandbox the
	// model's class depends on cannot be applied right now, instead of
	// running it unconfined.
	RequireSandbox bool
	// OnDelta receives incremental assistant text, only from a runtime that
	// streams incrementally.
	OnDelta func(text string)
}

// slotDir returns the working folder of a profile's limiter slot, created
// empty. A turn gets a fixed folder per profile and slot rather than a new one
// per request: agent CLIs keep per-folder session state (claude's
// ~/.claude/projects/<folder>), which would pile up without bound under a
// folder per request, and which this keeps apart between profiles. The folder
// is emptied before and after every turn, so nothing one request leaves can
// reach the next. A folder that cannot be emptied fails the request.
func (g *Gateway) slotDir(profileID string, slot int) (string, error) {
	dir := filepath.Join(g.cfg.ScratchRoot, profileFolder(profileID), fmt.Sprintf("%s%d", slotPrefix, slot))
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("creating the turn's folder: %w", err)
	}
	if !emptyDir(dir) {
		return "", fmt.Errorf("the turn's folder %s could not be emptied", dir)
	}
	return dir, nil
}

// runTurn runs t through monomind.Exec in its profile's slot folder, with exactly the
// posture of agent.ask and chat without tools: default (scoped) access, the
// workspace-write sandbox where the runtime has one, no caller tools, no
// settings, nothing from the client but the prompt, the model and the effort
// (all validated before this point).
//
// Exec's own error (the turn never started) is returned as is. When the
// start event reports a confinement weaker than the policy allows, the turn
// is cancelled and errPolicyDenied returned. A turn ended by the gateway's
// own deadline reports a timeout, not a cancellation.
func (g *Gateway) runTurn(ctx context.Context, t turn) (*monomind.TurnResult, error) {
	g.turnStarted()
	defer g.turnEnded()

	bin, err := g.bin.get(ctx)
	if err != nil {
		return nil, err
	}
	dir, err := g.slotDir(t.ProfileID, t.Slot)
	if err != nil {
		return nil, err
	}
	defer emptyDir(dir)

	tctx, cancel := context.WithTimeout(ctx, g.cfg.TurnTimeout+turnGrace)
	defer cancel()
	defer context.AfterFunc(g.shutdownCtx, cancel)() // the server is stopping: end the turn

	opts := monomind.ExecOptions{
		Runtime:          t.Runtime,
		Prompt:           t.Prompt,
		SystemPrompt:     t.System,
		Cwd:              dir,
		Bin:              bin,
		Sandbox:          monomind.TurnSandboxMode,
		RequireSandbox:   t.RequireSandbox,
		WorkspacePurpose: scratchPurpose,
		Timeout:          g.cfg.TurnTimeout,
	}
	if t.Model != "" && t.Model != agentroster.DefaultModel {
		opts.Model = t.Model
	}
	if t.Effort != "" {
		opts.Effort = t.Effort
		if caps := g.caps(tctx); caps.Has(monomind.CapAgentExecEffort) {
			opts.EffortFlag = true
		}
	}

	// onEvent runs in Exec's single event goroutine, and Exec returns only
	// after that goroutine ends, so these two need no lock.
	streams, denied := false, false
	res, err := g.deps.Exec(tctx, opts, func(ev monomind.Event) {
		if denied {
			return // monomind may deliver events already in flight; none of them reaches the client
		}
		switch ev.Type {
		case monomind.EventStart:
			streams = ev.StreamsIncrementally
			// An older monomind does not report native_sandbox; the
			// scan-based classification already decided for it.
			if ev.NativeSandbox != "" && !t.Policy.Allows(ClassFromNativeSandbox(ev.NativeSandbox)) {
				denied = true
				cancel()
			}
		case monomind.EventAssistant:
			if streams && t.OnDelta != nil && ev.Text != "" && ev.ParentToolUseID == "" {
				t.OnDelta(ev.Text)
			}
		}
	})
	if denied {
		return nil, errPolicyDenied
	}
	if err == nil && res != nil && res.Err != nil && res.Err.Code == monomind.ErrCancelled &&
		ctx.Err() == nil && errors.Is(tctx.Err(), context.DeadlineExceeded) {
		// Our own deadline ended the turn, not a caller who left: a timeout.
		res.Err = &monomind.ProtocolError{Code: monomind.ErrTimeout, Message: "the turn exceeded the time limit of " + g.cfg.TurnTimeout.String()}
	}
	return res, err
}

// caps is the handshake, nil when it can't be read.
func (g *Gateway) caps(ctx context.Context) *monomind.CapabilitySet {
	if g.deps.Catalog.Caps == nil {
		return nil
	}
	caps, err := g.deps.Catalog.Caps(ctx)
	if err != nil {
		return nil
	}
	return caps
}
