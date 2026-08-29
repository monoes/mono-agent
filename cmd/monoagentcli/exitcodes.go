package main

import (
	"errors"
	"fmt"

	"github.com/monoes/mono-agent/internal/workflow"
)

// Exit codes (see the exit-code table in AGENTS.md):
//
//	0 ok · 1 general error · 2 not-found · 3 invalid input / validation · 4 auth/connection
//
// Commands return sentinel-tagged errors; main.go maps them to the process
// exit code via exitCodeFor so agents can branch on the code instead of
// parsing stderr.

// cliError is an error carrying the process exit code it should map to.
type cliError struct {
	code int
	msg  string
}

func (e *cliError) Error() string { return e.msg }

// Sentinel errors — wrap or construct with the errNotFound / errInvalidInput /
// errAuthConnection helpers below, or test with errors.As(&cliError).
var (
	ErrNotFound       = &cliError{code: 2, msg: "not found"}
	ErrInvalidInput   = &cliError{code: 3, msg: "invalid input"}
	ErrAuthConnection = &cliError{code: 4, msg: "auth or connection failure"}
)

func errNotFound(format string, a ...interface{}) error {
	return &cliError{code: 2, msg: fmt.Sprintf(format, a...)}
}

func errInvalidInput(format string, a ...interface{}) error {
	return &cliError{code: 3, msg: fmt.Sprintf(format, a...)}
}

func errAuthConnection(format string, a ...interface{}) error {
	return &cliError{code: 4, msg: fmt.Sprintf(format, a...)}
}

// exitCodeFor maps a command error to the process exit code. cliError-based
// sentinels win; a few well-known internal/workflow sentinels are mapped so
// engine-returned errors (e.g. `workflow run` on an unknown id) classify
// without re-wrapping at every call site.
func exitCodeFor(err error) int {
	if err == nil {
		return 0
	}
	var ce *cliError
	if errors.As(err, &ce) {
		return ce.code
	}
	switch {
	case errors.Is(err, workflow.ErrWorkflowNotFound),
		errors.Is(err, workflow.ErrExecutionNotFound):
		return 2
	case errors.Is(err, workflow.ErrNoTriggerNode),
		errors.Is(err, workflow.ErrCycleDetected),
		errors.Is(err, workflow.ErrNodeTypeUnknown),
		errors.Is(err, workflow.ErrInvalidConfig):
		return 3
	default:
		return 1
	}
}
