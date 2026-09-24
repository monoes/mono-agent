package health

import (
	"context"
	"testing"
)

func nodeEnv(sysVer string, managed string) *Env {
	return &Env{
		SystemNode: func(context.Context) (string, string, bool) {
			if sysVer == "" {
				return "", "", false
			}
			return "/usr/bin/node", sysVer, true
		},
		ManagedNode: func() (string, string, bool) {
			if managed == "" {
				return "", "", false
			}
			return managed, "/home/u/.monoagent/node/" + managed + "/bin/node", true
		},
	}
}

func TestNodeCheck(t *testing.T) {
	cases := []struct {
		name, sys, managed string
		want               Status
		fix                string
	}{
		{"suitable system", "24.1.0", "", StatusOK, ""},
		{"no node", "", "", StatusFail, FixNodeInstall},
		{"old system", "20.0.0", "", StatusFail, FixNodeInstall},
		{"old system + managed", "20.0.0", "24.1.0", StatusOK, ""},
		{"managed only", "", "24.1.0", StatusOK, ""},
	}
	for _, tc := range cases {
		res := checkNode(context.Background(), nodeEnv(tc.sys, tc.managed))
		if res.Status != tc.want || res.FixID != tc.fix {
			t.Errorf("%s: %q/%q (%s), want %q/%q", tc.name, res.Status, res.FixID, res.Summary, tc.want, tc.fix)
		}
	}
	if res := checkNode(context.Background(), &Env{}); res.Status != StatusSkip {
		t.Errorf("no hooks: %q", res.Status)
	}
}

func TestNodeFixCallsInstaller(t *testing.T) {
	called := false
	env := &Env{InstallNode: func(context.Context, func(string)) error { called = true; return nil }}
	if err := fixNodeInstall(context.Background(), env, noop); err != nil || !called {
		t.Fatalf("fix: %v, called %v", err, called)
	}
}
