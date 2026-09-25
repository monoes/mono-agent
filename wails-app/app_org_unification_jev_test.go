package main

import (
	"strings"
	"testing"
)

// The GUI's decider-kind list matches the CLI's (jev plan WS4).
func TestAutonomySetArgsDeciderKinds(t *testing.T) {
	for _, c := range []struct {
		kind string
		ok   bool
	}{{"model", true}, {"boss", true}, {"parent", true}, {"jev", true}, {"bogus", false}} {
		args, err := autonomySetArgs("growth", `{"decider":{"kind":"`+c.kind+`"}}`)
		if (err == nil) != c.ok {
			t.Errorf("kind %s: args=%v err=%v", c.kind, args, err)
		}
		if c.ok && !strings.Contains(strings.Join(args, " "), "--decider "+c.kind) {
			t.Errorf("kind %s: args %v", c.kind, args)
		}
	}
}
