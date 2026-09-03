//go:build social

package main

import "testing"

// TestLegacySocialCommandsVisibleSocialBuild asserts the social build
// keeps every legacy social verb visible in help.
func TestLegacySocialCommandsVisibleSocialBuild(t *testing.T) {
	if got := hideLegacySocialCommands(); got != nil {
		t.Errorf("hideLegacySocialCommands() = %v in social build, want nil", got)
	}
	root := newRootCmd()
	for _, name := range []string{"list", "template"} {
		sub, _, err := root.Find([]string{name})
		if err != nil || sub == nil || sub.Name() != name {
			t.Fatalf("command %q not found on root: %v", name, err)
		}
		if sub.Hidden {
			t.Errorf("command %q must NOT be hidden in the social build", name)
		}
	}
}
