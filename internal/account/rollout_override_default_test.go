//go:build !devaccount

package account

import "testing"

// A default build, the build every release is, ignores the variable whatever it holds: it cannot
// move the date, and it cannot stop the process either.
func TestDefaultBuildIgnoresTheEnforceOverride(t *testing.T) {
	compiled, ok := startsWith(t, "")
	if !ok || date(compiled) == "" {
		t.Fatalf("the helper did not report a date: %s", compiled)
	}
	for _, value := range []string{"2020-01-01T00:00:00Z", "2999-01-01T00:00:00Z", "0001-01-01T00:00:00Z", "tomorrow"} {
		out, ok := startsWith(t, value)
		if !ok || date(out) != date(compiled) {
			t.Errorf("with the variable set to %q a default build reports %q (started: %v), want the compiled date %q", value, date(out), ok, date(compiled))
		}
	}
}
