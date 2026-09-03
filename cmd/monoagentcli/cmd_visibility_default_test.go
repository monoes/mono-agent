//go:build !social

package main

import "testing"

// TestLegacySocialCommandsHiddenDefaultBuild asserts the default build
// hides the legacy social verbs from help while they stay invokable.
func TestLegacySocialCommandsHiddenDefaultBuild(t *testing.T) {
	root := newRootCmd()
	hidden := map[string]bool{}
	for _, name := range hideLegacySocialCommands() {
		hidden[name] = true
	}
	for _, name := range []string{"list", "template"} {
		if !hidden[name] {
			t.Errorf("hideLegacySocialCommands() must include %q in the default build", name)
		}
		sub, _, err := root.Find([]string{name})
		if err != nil || sub == nil || sub.Name() != name {
			t.Fatalf("command %q not found on root: %v", name, err)
		}
		if !sub.Hidden {
			t.Errorf("command %q must be Hidden in the default build", name)
		}
	}
}

// TestLegacySocialCommandsKeptVisibleDefaultBuild pins the do-NOT-hide list.
func TestLegacySocialCommandsKeptVisibleDefaultBuild(t *testing.T) {
	root := newRootCmd()
	for _, name := range []string{"login", "crawl", "connect", "people"} {
		sub, _, err := root.Find([]string{name})
		if err != nil || sub == nil || sub.Name() != name {
			t.Fatalf("command %q not found on root: %v", name, err)
		}
		if sub.Hidden {
			t.Errorf("command %q must NOT be hidden", name)
		}
	}
}
