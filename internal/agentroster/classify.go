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
	StatusOK           = "ok"
	StatusOKUnexpected = "ok_unexpected"
	StatusAuth         = "auth"
	StatusQuota        = "quota"
	// StatusRateLimited is a transient 429 (agent exec already retried
	// it): the model can't run right now, but nothing is wrong with it,
	// so the roster shows it stale (re-checked) rather than failed.
	StatusRateLimited      = "rate_limited"
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
	notLoggedIn = regexp.MustCompile(`(?i)not logged in|not signed in|please run /login|use /login|run: \S+ login|unauthori[sz]ed|invalid api key|no api key|missing api key|api key (not found|missing|required)|no (inference )?providers? configured|authentication|401\b|login required|please (log|sign) ?in`)
	quotaHit    = regexp.MustCompile(`(?i)quota|usage limit|credit balance|insufficient (credit|balance|funds)|billing`)
	rateLimited = regexp.MustCompile(`(?i)rate.?limit|too many requests|429\b`)
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
		case monomind.ErrQuota:
			return StatusQuota, msg
		case monomind.ErrRateLimited:
			return StatusRateLimited, msg
		case monomind.ErrMissingBinary, monomind.ErrNoRunner:
			return StatusMissingBinary, msg
		case monomind.ErrTimeout:
			return StatusTimeout, msg
		case monomind.ErrCancelled:
			return StatusCancelled, msg
		}
		// A key that was never set is `auth` since protocol rev 27; an older
		// monomind sent it as a runner-error, classified by its text — but
		// never by text the runner only attached (a model's own words).
		return classifyMessage(monomind.ClassifiableMessage(pe.Message)), msg
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
	case rateLimited.MatchString(msg):
		return StatusRateLimited
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
