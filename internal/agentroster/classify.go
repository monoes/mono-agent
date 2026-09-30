// Package agentroster is the validated agent roster (monoes/mono-agent#225):
// which runtime × model pairs actually answer, measured by a one-word test
// turn through `monomind agent exec`, stored so the rest of mono-agent (the
// AI agents page, the dynamic org's staffing) can use only models that work.
package agentroster

import (
	"regexp"
	"strings"

	"github.com/monoes/mono-agent/internal/monomind"
)

// Validation statuses.
const (
	StatusOK               = "ok"
	StatusOKUnexpected     = "ok_unexpected"
	StatusAuth             = "auth"
	StatusQuota            = "quota"
	StatusModelUnavailable = "model_unavailable"
	StatusTimeout          = "timeout"
	StatusMissingBinary    = "missing_binary"
	StatusCancelled        = "cancelled"
	StatusError            = "error"
	// StatusUntested is a model id the user added that has not run yet.
	StatusUntested = "untested"
)

// Works reports whether a status means the model answered.
func Works(status string) bool {
	return status == StatusOK || status == StatusOKUnexpected
}

var (
	notLoggedIn = regexp.MustCompile(`(?i)not logged in|not signed in|please run /login|use /login|run: \S+ login|unauthori[sz]ed|invalid api key|no api key|api key (not found|missing|required)|no providers? configured|authentication|401\b|login required|please (log|sign) ?in`)
	quotaHit    = regexp.MustCompile(`(?i)quota|rate.?limit|usage limit|too many requests|429\b|credit balance|insufficient (credit|balance|funds)|billing`)
	modelGone   = regexp.MustCompile(`(?i)unknown model|invalid model|model[^.\n]{0,80}(not found|not available|does not exist|isn'?t available|unsupported|not supported|no access)|model_not_found|not a valid model|no such model|unsupported model`)
)

// maxDetail caps the stored error text.
const maxDetail = 300

// Classify turns one test turn's outcome into a status and a short detail.
// execErr is Exec's own error (the turn could not start); res is its result.
func Classify(res *monomind.TurnResult, execErr error) (status, detail string) {
	if execErr != nil {
		return StatusError, clip(execErr.Error())
	}
	if res == nil {
		return StatusError, "no result"
	}
	if pe := res.Err; pe != nil {
		msg := clip(pe.Message)
		switch pe.Code {
		case monomind.ErrAuth:
			return StatusAuth, msg
		case monomind.ErrQuota, monomind.ErrRateLimited:
			// A 429 agent exec already retried: the model can't run now.
			return StatusQuota, msg
		case monomind.ErrMissingBinary, monomind.ErrNoRunner:
			return StatusMissingBinary, msg
		case monomind.ErrTimeout:
			return StatusTimeout, msg
		case monomind.ErrCancelled:
			return StatusCancelled, msg
		}
		return classifyMessage(pe.Message), msg
	}
	switch res.StopReason {
	case monomind.StopTimeout:
		return StatusTimeout, "timed out"
	case monomind.StopCancelled:
		return StatusCancelled, "cancelled"
	}
	if !res.SawDone {
		return StatusError, "the turn ended without finishing"
	}
	reply := strings.TrimSpace(res.ResultText)
	if isOK(reply) {
		return StatusOK, ""
	}
	if reply == "" {
		return StatusError, "empty reply"
	}
	// Some runtimes answer an error in the reply text instead of an error
	// event (a not-logged-in banner, an unknown-model notice).
	if s := classifyMessage(reply); s != StatusError {
		return s, clip(reply)
	}
	return StatusOKUnexpected, clip(reply)
}

// classifyMessage maps an error message to a status; StatusError when
// nothing matches.
func classifyMessage(msg string) string {
	switch {
	case notLoggedIn.MatchString(msg):
		return StatusAuth
	case modelGone.MatchString(msg):
		return StatusModelUnavailable
	case quotaHit.MatchString(msg):
		return StatusQuota
	}
	return StatusError
}

// isOK accepts "ok" with any case and surrounding punctuation or markdown.
func isOK(reply string) bool {
	return strings.EqualFold(strings.Trim(reply, " \t\r\n.!*`\"'_"), "ok")
}

func clip(s string) string {
	s = strings.TrimSpace(s)
	if r := []rune(s); len(r) > maxDetail {
		return string(r[:maxDetail]) + "…"
	}
	return s
}
