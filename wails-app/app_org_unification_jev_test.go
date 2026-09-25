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

func TestAutonomySetArgsDeciderThreshold(t *testing.T) {
	a, err := autonomySetArgs("growth", `{"decider":{"kind":"jev","threshold":0.85}}`)
	eqArgs(t, a, err, []string{"autonomy", "set", "growth", "--by", "gui", "--decider", "jev", "--decider-threshold", "0.85"})
	a, err = autonomySetArgs("growth", `{"decider":{"threshold":1}}`)
	eqArgs(t, a, err, []string{"autonomy", "set", "growth", "--by", "gui", "--decider-threshold", "1"})
	for _, bad := range []string{"0", "-0.1", "1.05"} {
		if _, err := autonomySetArgs("growth", `{"decider":{"kind":"jev","threshold":`+bad+`}}`); err == nil || !strings.Contains(err.Error(), "threshold must be") {
			t.Errorf("threshold %s: err=%v", bad, err)
		}
	}
}
