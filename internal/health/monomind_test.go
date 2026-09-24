package health

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/monoes/mono-agent/internal/monomind"
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

func strp(s string) *string { return &s }

func TestMonomindBinaryCheck(t *testing.T) {
	env := &Env{FindMonomind: func() (string, error) { return "", &monomind.ErrNotFound{Tried: []string{"/x"}} }}
	if res := checkMonomindBinary(context.Background(), env); res.Status != StatusFail || res.FixID != FixMonomindInstall {
		t.Errorf("missing: %+v", res)
	}
	env.FindMonomind = func() (string, error) { return "/usr/bin/monomind", nil }
	if res := checkMonomindBinary(context.Background(), env); res.Status != StatusOK {
		t.Errorf("found: %+v", res)
	}
}

func TestMonomindHandshakeAndCapabilities(t *testing.T) {
	ctx := context.Background()
	env := &Env{MonomindHandshake: func(context.Context) (*monomind.VersionInfo, error) {
		return nil, errors.New("monomind 1.0.0 is too old")
	}}
	if res := checkMonomindHandshake(ctx, env); res.Status != StatusFail || res.FixID != FixMonomindInstall {
		t.Errorf("old: %+v", res)
	}

	vi := &monomind.VersionInfo{V: 1, Version: "2.16.0", Capabilities: []string{monomind.CapOrgToolProviders}}
	env.MonomindHandshake = func(context.Context) (*monomind.VersionInfo, error) { return vi, nil }
	if res := checkMonomindHandshake(ctx, env); res.Status != StatusOK {
		t.Errorf("good: %+v", res)
	}
	res := checkMonomindCapabilities(ctx, env)
	if res.Status != StatusWarn || res.FixID != FixMonomindInstall || !strings.Contains(res.Detail, monomind.CapOrgFederation) {
		t.Errorf("missing caps: %+v", res)
	}
	for _, oc := range optionalCapabilities {
		vi.Capabilities = append(vi.Capabilities, oc.cap)
	}
	if res := checkMonomindCapabilities(ctx, env); res.Status != StatusOK {
		t.Errorf("all caps: %+v", res)
	}
}

func TestMonomindProfileInitCheckAndFix(t *testing.T) {
	root := t.TempDir()
	var initRoot string
	env := &Env{
		ProfileRoot: func(string) string { return root },
		InitMonomindProfile: func(_ context.Context, r string, _ func(string)) error {
			initRoot = r
			os.MkdirAll(filepath.Join(r, ".monomind"), 0o700)
			return os.WriteFile(filepath.Join(r, ".monomind", "config.yaml"), []byte("x"), 0o600)
		},
	}
	if res := checkMonomindProfileInit(context.Background(), env); res.Status != StatusWarn || res.FixID != FixMonomindProfileInit {
		t.Fatalf("uninitialized: %+v", res)
	}
	if err := fixMonomindProfileInit(context.Background(), env, noop); err != nil || initRoot != root {
		t.Fatalf("fix: %v (root %q)", err, initRoot)
	}
	if res := checkMonomindProfileInit(context.Background(), env); res.Status != StatusOK {
		t.Fatalf("initialized: %+v", res)
	}
}

func TestRuntimesCheckReportsChildren(t *testing.T) {
	scan := &monomind.ScanResult{V: 1, Agents: []monomind.ScanEntry{
		{ID: "claude", Installed: true, Binary: strp("/bin/claude"), Version: strp("2.1.0")},
		{ID: "codex", Installed: false, InstallHint: "npm install -g @openai/codex"},
	}}
	reg := NewRegistry([]Check{{ID: CheckRuntimes, Group: GroupRuntimes, Title: "AI agent runtimes", Run: checkRuntimes}}, runtimeFixes())
	env := &Env{ScanRuntimes: func(context.Context) (*monomind.ScanResult, error) { return scan, nil }}
	rep := reg.Run(context.Background(), env, Options{})
	if len(rep.Results) != 3 {
		t.Fatalf("want parent + 2 children, got %+v", rep.Results)
	}
	got := byID(rep)
	if got[CheckRuntimes].Status != StatusOK || got["runtimes.claude"].Status != StatusOK ||
		got["runtimes.codex"].Status != StatusInfo || got["runtimes.codex"].Group != GroupRuntimes {
		t.Fatalf("results: %+v", rep.Results)
	}
	if got["runtimes.claude"].Parent != CheckRuntimes || got[CheckRuntimes].Parent != "" {
		t.Errorf("parent links: %+v", got)
	}
	if rep.Summary[StatusOK] != 2 || rep.Summary[StatusInfo] != 1 {
		t.Errorf("summary counts children: %v", rep.Summary)
	}

	if f := got["runtimes.codex"].Fix; f == nil || f.ID != "runtimes.install:codex" || !f.Optional {
		t.Errorf("codex should offer an optional install fix: %+v", f)
	}

	scan.Agents = scan.Agents[1:]
	if res := checkRuntimes(context.Background(), env); res.Status != StatusFail {
		t.Errorf("none installed: %+v", res)
	}
}
