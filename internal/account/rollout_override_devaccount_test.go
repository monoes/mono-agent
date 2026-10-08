//go:build devaccount

package account

import (
	"strings"
	"testing"
)

// A development build reads the variable, and only to set a date: a value that is not an RFC 3339
// time, or the zero time, which would switch the gate off, stops it at start.
func TestDevBuildReadsTheEnforceOverride(t *testing.T) {
	if out, ok := startsWith(t, "2020-01-01T00:00:00Z"); !ok || date(out) != "2020-01-01T00:00:00Z" {
		t.Errorf("a past date forces enforcement: %q (started: %v)", date(out), ok)
	}
	if out, ok := startsWith(t, "2999-01-01T00:00:00Z"); !ok || date(out) != "2999-01-01T00:00:00Z" {
		t.Errorf("a future date forces the warn period: %q (started: %v)", date(out), ok)
	}
	for value, why := range map[string]string{"tomorrow": "must be an RFC 3339 time", "0001-01-01T00:00:00Z": "would switch the gate off"} {
		if out, ok := startsWith(t, value); ok || !strings.Contains(out, why) {
			t.Errorf("%q should stop the process with %q: started %v: %s", value, why, ok, out)
		}
	}
}
