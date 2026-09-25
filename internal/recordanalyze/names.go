// Package recordanalyze turns an activity recording (internal/recording)
// into a draft automation action: deterministic normalize → detect, then an
// AI draft through the monomind runner, then deterministic lint; verify
// replays the draft in safe mode and save installs it into the registry.
//
// Spec §8.4–§8.7: docs/mastermind/specs/2026-09-25-browser-automation-packages-design.md
package recordanalyze

import (
	"fmt"
	"regexp"
	"strings"
)

var (
	automationIDRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}$`)
	actionNameRe   = regexp.MustCompile(`^[a-z0-9][a-z0-9_]{0,62}$`)
	inputNameRe    = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_]{0,62}$`)
	nonWordRe      = regexp.MustCompile(`[^a-z0-9]+`)
)

// ValidAutomationID reports whether id is a valid package id ("acme-crm").
func ValidAutomationID(id string) bool { return automationIDRe.MatchString(id) }

// ValidActionName reports whether n is a valid action/fragment name
// ("create_contact").
func ValidActionName(n string) bool { return actionNameRe.MatchString(n) }

// ValidInputName reports whether n is a valid input/output variable name.
func ValidInputName(n string) bool { return inputNameRe.MatchString(n) }

// slugWith lowercases s and joins its alphanumeric runs with sep, capped
// at 63 chars. Empty input (or nothing alphanumeric) yields fallback.
func slugWith(s, sep, fallback string) string {
	out := strings.Trim(nonWordRe.ReplaceAllString(strings.ToLower(s), sep), sep)
	if len(out) > 63 {
		out = strings.TrimRight(out[:63], sep)
	}
	if out == "" {
		return fallback
	}
	return out
}

// AutomationSlug makes a package id from free text ("Acme CRM" → "acme-crm").
func AutomationSlug(s string) string { return slugWith(s, "-", "recorded") }

// ActionSlug makes an action/fragment name ("Create contact" → "create_contact").
func ActionSlug(s string) string { return slugWith(s, "_", "recorded_action") }

// InputSlug makes an input name; a leading digit gets a "v_" prefix.
func InputSlug(s string) string {
	out := slugWith(s, "_", "value")
	if out[0] >= '0' && out[0] <= '9' {
		out = "v_" + out
	}
	return out
}

// Unique returns name, or the first of name_2, name_3, … (joined with sep)
// that taken does not report as used.
func Unique(name, sep string, taken func(string) bool) string {
	if taken == nil || !taken(name) {
		return name
	}
	for n := 2; ; n++ {
		c := fmt.Sprintf("%s%s%d", name, sep, n)
		if !taken(c) {
			return c
		}
	}
}
