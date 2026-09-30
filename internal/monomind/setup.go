package monomind

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// AgentNotSetupCode classifies a failure caused by the local AI agent not
// being set up: monomind missing or unusable (e.g. too old), no runtime installed, the
// requested runtime missing or unknown, or the runtime not logged in. The
// CLI reports it as {"error","code":"agent_not_setup"} and on a chat turn's
// turn.finished event; the desktop app answers it with a link to its AI
// agents page.
const AgentNotSetupCode = "agent_not_setup"

// AgentNotSetupMarker is appended to the text of a MarkNotSetup error, so
// the classification survives where only the message is stored (a workflow
// run's error_message).
const AgentNotSetupMarker = "[" + AgentNotSetupCode + "]"

// ErrNoRuntime is returned when a runtime must be picked automatically and
// no agent runtime is installed at all.
var ErrNoRuntime = errors.New("no AI agent runtime is installed (see `monoagentcli agent scan`)")

// ErrRuntimeNotInstalled is wrapped by callers that checked `agent scan`
// and found the requested runtime missing.
var ErrRuntimeNotInstalled = errors.New("agent runtime is not installed")

// unusableError is a Handshake failure: an installed monomind this client
// cannot drive (too old, missing a capability, not speaking the protocol,
// or failing to start).
type unusableError struct{ err error }

func (e *unusableError) Error() string { return e.err.Error() }
func (e *unusableError) Unwrap() error { return e.err }

func unusable(format string, a ...any) error {
	return &unusableError{fmt.Errorf(format, a...)}
}

// notLoggedIn matches a runtime's own "sign in first" answer. Claude Code
// reports it as an error result ("Not logged in · Please run /login"),
// which monomind forwards as a non-fatal runner-error rather than `auth`.
var notLoggedIn = regexp.MustCompile(`(?i)not logged in|please run /login|run: \S+ login`)

// unclassifiedMarker introduces text a runner attached to an error but did
// not write itself (hermes's stdout, cline's final model text; protocol
// rev 27). It never decides the error's class: a model saying "401" must
// not turn a failure into a sign-in problem.
const unclassifiedMarker = "[output below is not classified]"

// ClassifiableMessage is an error message without the unclassified text a
// runner attached after unclassifiedMarker.
func ClassifiableMessage(msg string) string {
	before, _, _ := strings.Cut(msg, unclassifiedMarker)
	return before
}

// IsAgentNotSetup reports whether err means the AI agent is not set up (see
// AgentNotSetupCode).
func IsAgentNotSetup(err error) bool {
	if err == nil {
		return false
	}
	var ns *notSetupError
	var ue *unusableError
	if errors.As(err, &ns) || errors.As(err, &ue) || IsNotFound(err) ||
		errors.Is(err, ErrNoRuntime) || errors.Is(err, ErrRuntimeNotInstalled) {
		return true
	}
	var pe *ProtocolError
	if errors.As(err, &pe) {
		switch pe.Code {
		case ErrAuth, ErrMissingBinary, ErrNoRunner:
			return true
		}
		return notLoggedIn.MatchString(ClassifiableMessage(pe.Message))
	}
	return strings.Contains(err.Error(), AgentNotSetupMarker)
}

// MarkNotSetup returns err with AgentNotSetupMarker appended to its text
// when IsAgentNotSetup classifies it, and err unchanged otherwise. The
// result still unwraps to err.
func MarkNotSetup(err error) error {
	if !IsAgentNotSetup(err) || strings.Contains(err.Error(), AgentNotSetupMarker) {
		return err
	}
	return &notSetupError{err: err}
}

type notSetupError struct{ err error }

func (e *notSetupError) Error() string { return e.err.Error() + " " + AgentNotSetupMarker }
func (e *notSetupError) Unwrap() error { return e.err }

// ReclassifyMissingRuntime gives a failed turn's error the missing-binary
// code when `agent scan` lists its runtime as not installed. Some runners
// report a missing CLI as a plain runner-error ("GrokAgentRunner requires
// the Grok Build CLI (grok) on PATH"), which would otherwise not read as
// agent_not_setup. Only a failed turn not already classified pays for the
// scan; a scan that fails leaves the error as it is.
func ReclassifyMissingRuntime(ctx context.Context, runtime string, res *TurnResult) {
	if res == nil || res.Err == nil || ctx.Err() != nil || IsAgentNotSetup(res.Err) {
		return
	}
	scan, err := Scan(ctx)
	if err != nil || scan == nil {
		return
	}
	if e := scan.Find(runtime); e != nil && !e.Installed {
		res.Err.Code = ErrMissingBinary
	}
}
