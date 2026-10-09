package health

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/monoes/mono-agent/internal/monomind"
)

func upkeepEnv(t *testing.T, version, pin string) *Env {
	t.Helper()
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, ".monomind"), 0o755)
	os.WriteFile(filepath.Join(root, ".monomind", "config.yaml"), []byte("x"), 0o644)
	os.WriteFile(filepath.Join(root, ".mcp.json"), []byte(`{"mcpServers":{"monomind":{"args":["-y","--package=@monoes/monomindcli@`+pin+`","monomind","mcp","start"]}}}`), 0o644)
	return &Env{
		Home:        t.TempDir(),
		ProfileRoot: func(string) string { return root },
		MonomindHandshake: func(context.Context) (*monomind.VersionInfo, error) {
			return &monomind.VersionInfo{V: 1, Version: version}, nil
		},
	}
}

func TestMCPPinCheck(t *testing.T) {
	res := checkMonomindMCPPin(context.Background(), upkeepEnv(t, "2.24.4", "2.20.0"))
	if res.Status != StatusWarn || res.FixID != FixMonomindRepin || !strings.Contains(res.Summary, "2.20.0") || !strings.Contains(res.Summary, "2.24.4") {
		t.Fatalf("stale pin: %+v", res)
	}
	if res := checkMonomindMCPPin(context.Background(), upkeepEnv(t, "2.24.4", "2.24.4")); res.Status != StatusOK || res.FixID != "" {
		t.Errorf("current pin: %+v", res)
	}
	env := upkeepEnv(t, "2.24.4", "2.20.0")
	env.ProfileRoot = func(string) string { return t.TempDir() }
	if res := checkMonomindMCPPin(context.Background(), env); res.Status != StatusSkip {
		t.Errorf("folder not set up: %+v", res)
	}
}

func TestRepinFixIsConfirmLevelAndReinitsTheProfile(t *testing.T) {
	f, ok := Default().Fix(FixMonomindRepin)
	if !ok || f.Safety != SafetyConfirm {
		t.Fatalf("repin fix: %+v ok=%v, want a confirm fix", f.FixInfo, ok)
	}
	env := upkeepEnv(t, "2.24.4", "2.20.0")
	var got string
	env.RepinMonomindProfile = func(_ context.Context, root string, _ func(string)) error { got = root; return nil }
	if err := f.Apply(context.Background(), env, func(string) {}); err != nil || got != env.ProfileRoot("") {
		t.Fatalf("repin ran in %q, err %v", got, err)
	}
	env.ProfileRoot = func(string) string { return t.TempDir() }
	if err := f.Apply(context.Background(), env, func(string) {}); err == nil {
		t.Error("repin must refuse a folder that is not set up")
	}
}

func TestDepsCheck(t *testing.T) {
	env := upkeepEnv(t, "2.24.4", "2.24.4")
	res := checkMonomindDeps(context.Background(), env)
	if res.Status != StatusInfo || res.FixID != FixMonomindDeps {
		t.Fatalf("not installed: %+v", res)
	}
	os.MkdirAll(filepath.Join(env.Home, ".monomind", "deps", "@anthropic-ai+claude-agent-sdk@0.3.289"), 0o755)
	if res := checkMonomindDeps(context.Background(), env); res.Status != StatusOK || res.FixID != "" {
		t.Errorf("installed: %+v", res)
	}
	old := upkeepEnv(t, "2.21.0", "2.21.0")
	if res := checkMonomindDeps(context.Background(), old); res.Status != StatusSkip || res.FixID != "" {
		t.Errorf("monomind without deps install: %+v", res)
	}
}

func TestDepsFixIsOptionalAndConfirmLevel(t *testing.T) {
	f, ok := Default().Fix(FixMonomindDeps)
	if !ok || f.Safety != SafetyConfirm || !f.Optional {
		t.Fatalf("deps fix: %+v ok=%v", f.FixInfo, ok)
	}
}
