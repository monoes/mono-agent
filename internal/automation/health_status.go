package automation

import (
	"strings"
	"time"
)

// Selector health statuses (contracts §5, `automation doctor`).
const (
	HealthOK       = "ok"
	HealthDecaying = "decaying"
	HealthBroken   = "broken"
	// HealthStale marks a health row whose key the installed package
	// version no longer declares (doctor only; never from SelectorStatus).
	HealthStale = "stale"
)

// Outcome letters of the recent-outcome ring (automation_selector_health.recent).
const (
	outcomeOK     = 'o' // matched on the first candidate
	outcomeHealed = 'h' // matched on a later candidate or the Jev fallback
	outcomeFail   = 'f' // nothing matched
)

// recentRingSize is how many outcomes the ring keeps.
const recentRingSize = 10

// Status thresholds. See SelectorStatus.
const (
	brokenRunLength      = 5   // this many failures in a row is always broken
	brokenRunAfterOK     = 3   // …or this many when the last ok is older than the last fail
	decayingHealedFactor = 0.3 // healed / successes in the ring above this is decaying
)

// SelectorHealth is one automation_selector_health row plus its status.
type SelectorHealth struct {
	AutomationID       string     `json:"automationId"`
	Key                string     `json:"key"`
	OK                 int        `json:"ok"`
	Fail               int        `json:"fail"`
	Healed             int        `json:"healed"`
	LastOK             *time.Time `json:"lastOk"`
	LastFail           *time.Time `json:"lastFail"`
	LastCandidateIndex int        `json:"lastCandidateIndex"`
	Recent             string     `json:"recent"`
	Status             string     `json:"status"`
}

// SelectorStatus derives ok / decaying / broken from a health row.
//
//   - broken: the ring ends in ≥5 failures, or in ≥3 failures while the
//     last success (if any) is older than the last failure. A selector
//     that works again after failing drops out of broken on its next
//     success.
//   - decaying: not broken, and within the recent ring (last 10 outcomes)
//     either at least one lookup failed, or more than 30% of the
//     successful lookups needed healing.
//   - ok: everything else, including a selector with no data yet.
//
// Only the ring and the last-ok/last-fail times are used, so a selector
// that was fixed long ago is not held down by its lifetime counters.
func SelectorStatus(h SelectorHealth) string {
	run := trailingFails(h.Recent)
	if run >= brokenRunLength {
		return HealthBroken
	}
	if run >= brokenRunAfterOK && h.Fail > 0 && (h.LastOK == nil || (h.LastFail != nil && h.LastOK.Before(*h.LastFail))) {
		return HealthBroken
	}
	fails := strings.Count(h.Recent, string(outcomeFail))
	if fails > 0 {
		return HealthDecaying
	}
	healed := strings.Count(h.Recent, string(outcomeHealed))
	successes := healed + strings.Count(h.Recent, string(outcomeOK))
	if successes > 0 && float64(healed)/float64(successes) > decayingHealedFactor {
		return HealthDecaying
	}
	return HealthOK
}

// trailingFails counts the failures at the end of the ring.
func trailingFails(recent string) int {
	n := 0
	for i := len(recent) - 1; i >= 0 && recent[i] == outcomeFail; i-- {
		n++
	}
	return n
}

// appendRecent appends outcomes to the ring, keeping the newest
// recentRingSize.
func appendRecent(ring, outcomes string) string {
	ring += outcomes
	if len(ring) > recentRingSize {
		ring = ring[len(ring)-recentRingSize:]
	}
	return ring
}

// outcomeOf maps one observation to its ring letter.
func outcomeOf(ok, healed bool) byte {
	switch {
	case !ok:
		return outcomeFail
	case healed:
		return outcomeHealed
	default:
		return outcomeOK
	}
}
