package openaiapi

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
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

// errShuttingDown is runTurn's answer once the gateway is shutting down.
var errShuttingDown = errors.New("the server is shutting down")

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
// reach the next. A folder that cannot be emptied is set aside, for the
// operator to delete, and replaced by an empty one; if that fails too, the
// request fails.
func (g *Gateway) slotDir(profileID string, slot int) (string, error) {
	profileDir := filepath.Join(g.cfg.ScratchRoot, profileFolder(profileID))
	dir := filepath.Join(profileDir, fmt.Sprintf("%s%d", slotPrefix, slot))
	if err := os.MkdirAll(g.cfg.ScratchRoot, 0o700); err != nil {
		return "", fmt.Errorf("creating the turn's folder: %w", err)
	}
	// Both must be real directories: a link planted in place of either would
	// send the emptying, and the turn, somewhere else. Each is looked at before
	// anything is created in it, so nothing is created behind a link.
	for _, d := range []string{profileDir, dir} {
		if err := plainDir(d); err != nil {
			return "", fmt.Errorf("the turn's folder: %w", err)
		}
	}
	if !emptyDir(dir) {
		if err := g.quarantine(dir); err != nil {
			return "", fmt.Errorf("the turn's folder %s could not be emptied or set aside: %w", dir, err)
		}
	}
	return dir, nil
}

// plainDir makes sure path is a directory of its own, creating it when it is
// missing. A link or a file there is refused.
func plainDir(path string) error {
	fi, err := os.Lstat(path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return os.Mkdir(path, 0o700)
	case err != nil:
		return err
	case !fi.IsDir():
		return fmt.Errorf("%s is not a plain directory", path)
	}
	return nil
}

// turnTempDir makes the private folder for the files Exec writes for one turn:
// its prompt and system prompt (which carry a context key's excerpts). They
// must not sit in the system temp directory, which a sandboxed runtime can
// write, where another turn could rewrite them between Exec creating them and
// monomind reading them. This folder is outside every turn's writable area.
func (g *Gateway) turnTempDir() (string, error) {
	root := filepath.Join(g.cfg.ScratchRoot, tmpDirName)
	if err := os.MkdirAll(root, 0o700); err != nil {
		return "", fmt.Errorf("creating the private folder of the turn's files: %w", err)
	}
	_ = os.Chmod(root, 0o700)          // MkdirAll leaves the mode of a folder that was already there
	return os.MkdirTemp(root, "turn-") // mode 0700
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
	if !g.turnStarted() {
		return nil, errShuttingDown
	}
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
	tmp, err := g.turnTempDir()
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(tmp)

	tctx, cancel := context.WithTimeout(ctx, g.cfg.TurnTimeout+turnGrace)
	defer cancel()
	defer context.AfterFunc(g.shutdownCtx, cancel)() // the server is stopping: end the turn

	opts := monomind.ExecOptions{
		Runtime:          t.Runtime,
		Prompt:           t.Prompt,
		SystemPrompt:     t.System,
		Cwd:              dir,
		Bin:              bin,
		TempDir:          tmp,
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
