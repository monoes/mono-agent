package main

import (
	"strings"
	"testing"
)

// `org autonomy set --decider jev` is the decider's opt-in (jev plan D2):
// it needs a TypeSafe key for the profile, stores the threshold in the
// decider settings, and shows both.
func TestOrgAutonomyJevDecider(t *testing.T) {
	f := newOrgCLIFixture(t)
	stubRuntimes(t, nil, nil, nil) // no runtime catalog: the model name is left as typed

	t.Setenv("TYPESAFE_API_KEY", "")
	if _, err := f.run(t, "autonomy", "set", "growth", "--decider", "jev"); err == nil || !strings.Contains(err.Error(), "TypeSafe API key") {
		t.Fatalf("jev without a key: %v", err)
	}

	t.Setenv("TYPESAFE_API_KEY", "test-key")
	set := f.mustRun(t, "autonomy", "set", "growth", "--decider", "jev")
	d := set["decider"].(map[string]interface{})
	if d["kind"] != "jev" || d["threshold"] != 0.8 || d["fallback"] != "model" {
		t.Fatalf("decider = %v", d)
	}
	set = f.mustRun(t, "autonomy", "set", "growth", "--decider-threshold", "0.65")
	if got := set["decider"].(map[string]interface{})["threshold"]; got != 0.65 {
		t.Fatalf("threshold = %v", got)
	}
	for _, bad := range []string{"0", "1.5", "-1"} {
		if _, err := f.run(t, "autonomy", "set", "growth", "--decider-threshold", bad); err == nil {
			t.Fatalf("threshold %s accepted", bad)
		}
	}
	if got := f.mustRun(t, "autonomy", "show", "growth")["decider"].(map[string]interface{}); got["kind"] != "jev" || got["threshold"] != 0.65 {
		t.Fatalf("show = %v", got)
	}

	// The CLI's kind list: model and jev pass here (boss and parent need
	// monomind and a holding org, covered elsewhere); anything else fails.
	for _, c := range []struct {
		kind string
		ok   bool
	}{{"bogus", false}, {"jev", true}, {"model", true}} {
		_, err := f.run(t, "autonomy", "set", "growth", "--decider", c.kind)
		if (err == nil) != c.ok {
			t.Errorf("--decider %s: err=%v", c.kind, err)
		}
	}
	if got := f.mustRun(t, "autonomy", "show", "growth")["decider"].(map[string]interface{}); got["threshold"] != nil {
		t.Fatalf("model decider shows a threshold: %v", got)
	}
}
