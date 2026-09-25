package chat

import (
	"context"
	"strings"
	"testing"
)

// set_org_autonomy's decider-kind list matches orgdesign's (jev plan WS4).
func TestSetOrgAutonomy_DeciderKinds(t *testing.T) {
	db := newMonoagentTestDB(t)
	mt := NewMonoagentTools(db.DB, "/bin/monoagentcli")
	stubRunSelfExec(t, func(ctx context.Context, bin string, args ...string) ([]byte, error) {
		t.Fatal("exec must not run for a preview")
		return nil, nil
	})
	for _, c := range []struct {
		kind string
		ok   bool
	}{{"model", true}, {"boss", true}, {"parent", true}, {"jev", true}, {"bogus", false}} {
		out, err := mt.Execute("set_org_autonomy", `{"org_name":"growth","decider":"`+c.kind+`"}`)
		if (err == nil) != c.ok {
			t.Errorf("decider %s: out=%s err=%v", c.kind, out, err)
		}
		if c.ok && !strings.Contains(out, "--decider "+c.kind) {
			t.Errorf("decider %s: preview %s", c.kind, out)
		}
	}
}

func TestSetOrgAutonomy_DeciderThreshold(t *testing.T) {
	db := newMonoagentTestDB(t)
	mt := NewMonoagentTools(db.DB, "/bin/monoagentcli")
	stubRunSelfExec(t, func(ctx context.Context, bin string, args ...string) ([]byte, error) {
		t.Fatal("exec must not run for a preview")
		return nil, nil
	})
	out, err := mt.Execute("set_org_autonomy", `{"org_name":"growth","decider":"jev","decider_threshold":0.85}`)
	if err != nil || !strings.Contains(out, "--decider jev --decider-threshold 0.85") {
		t.Fatalf("preview %s err=%v", out, err)
	}
	for _, bad := range []string{"0", "1.2"} {
		if out, err := mt.Execute("set_org_autonomy", `{"org_name":"growth","decider":"jev","decider_threshold":`+bad+`}`); err == nil {
			t.Errorf("threshold %s accepted: %s", bad, out)
		}
	}
}
