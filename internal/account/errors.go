package account

import (
	"errors"
	"fmt"
)

// LoginRequiredMessage is the first line of every refusal. The doors that
// must not carry more (/v1, MCP) use it as the whole message.
const LoginRequiredMessage = "Log in to monoes.me first: monoagentcli account login"

// LoginRequiredError is what every gate returns. Its message starts with
// LoginRequiredMessage and, for the reasons that have one, adds a second line.
type LoginRequiredError struct{ Status Status }

func (e *LoginRequiredError) Error() string {
	if line := reasonLine(e.Status.Reason); line != "" {
		return LoginRequiredMessage + "\n" + line
	}
	return LoginRequiredMessage
}

// JSONErrorFields is the machine-readable form of the refusal for `--json`
// callers: the CLI's JSON error wrappers add these keys to {"error": …}, so a
// refusal that comes out of any command prints the document of index §3.4
// item 2, not only one that comes out of the CLI gate. A raw error of this type
// is not classified by the CLI (exit 1), so the code is carried here.
func (e *LoginRequiredError) JSONErrorFields() map[string]any {
	return map[string]any{
		"login_required": true,
		"code":           "auth_or_connection",
		"account":        map[string]any{"state": string(e.Status.State), "reason": string(e.Status.Reason)},
	}
}

// IsLoginRequired reports whether err is, or wraps, a *LoginRequiredError.
func IsLoginRequired(err error) bool {
	var e *LoginRequiredError
	return errors.As(err, &e)
}

func reasonLine(r Reason) string {
	switch r {
	case ReasonExpired:
		return fmt.Sprintf("This login expired: monoes.me has not been reachable for %g hours.", GraceWindow.Hours())
	case ReasonRefused:
		return "monoes.me ended this login (the account was blocked or the login was revoked)."
	case ReasonClockRollback:
		return "The system clock went back. Fix the clock, then sign in again."
	case ReasonClockSkew:
		return fmt.Sprintf("The system clock is more than %g minutes behind monoes.me. Fix the clock.", ClockSkew.Minutes())
	case ReasonKeyUnknown:
		return "This build cannot verify the login. Update it: monoagentcli update"
	case ReasonInvalid:
		return "The stored login is not valid. Sign in again."
	}
	return ""
}

// RefusedError is what a Refresher returns when monoes.me answered
// invalid_grant to a refresh-token grant, and only then (spec D27): every
// other failure is a TransientError.
type RefusedError struct{ Description string }

func (e *RefusedError) Error() string {
	if e == nil {
		return ""
	}
	if e.Description == "" {
		return "account: monoes.me refused the refresh token (invalid_grant)"
	}
	return "account: monoes.me refused the refresh token (invalid_grant): " + e.Description
}

// TransientError is any other refresh failure: no network, a timeout, another
// OAuth error, a 4xx or 5xx answer. It is not a decision about the account, so
// the grace applies.
//
// Settled says whether the outcome of the grant is KNOWN: true when the request
// never left this machine (DNS, dial, TLS, or a cancellation before the request
// was written) or monoes.me answered with an HTTP status, so the refresh token
// was not consumed by an answer that nobody saw. The zero value means UNKNOWN:
// the request may have been processed, and monoes.me may have rotated the refresh
// token while the answer was lost. It is the fail-safe default, so a Refresher
// that does not set it, like any error that is not a *TransientError, is read as
// one whose outcome is unknown, and the guard then never presents that token
// again after the reuse window (A24).
type TransientError struct {
	Reason  Reason // ReasonUnreachable or ReasonServerError
	Settled bool
	Err     error
}

func (e *TransientError) Error() string {
	if e == nil {
		return ""
	}
	if e.Err == nil {
		return fmt.Sprintf("account: refresh failed (%s)", e.Reason)
	}
	return fmt.Sprintf("account: refresh failed (%s): %v", e.Reason, e.Err)
}

func (e *TransientError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}
