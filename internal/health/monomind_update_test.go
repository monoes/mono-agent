package health

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/monoes/mono-agent/internal/monomind"
)

// An older monomind first on PATH shadowing the copy monomind.install put
// under the data folder: installing again changes nothing, so the fix names
// the one to remove instead of offering monomind.install forever.
func TestMonomindUpdateNamesTheShadowingCopy(t *testing.T) {
	ctx := context.Background()
	data := "/home/u/.monoagent"
	managed := filepath.Join(data, "npm-global", "bin", "monomind")
	env := &Env{DataDir: data,
		MonomindHandshake: func(context.Context) (*monomind.VersionInfo, error) {
			return nil, errors.New("monomind 1.0.0 is too old")
		},
		MonomindCandidates: func() []string { return []string{"/usr/bin/monomind", managed} },
	}
	res := checkMonomindHandshake(ctx, env)
	if res.FixID != FixMonomindShadowed || !strings.Contains(res.FixCommand, "/usr/bin/monomind") || !strings.Contains(res.FixCommand, managed) {
		t.Fatalf("shadowed: %+v", res)
	}
	if !strings.Contains(res.Detail, "comes first on PATH") {
		t.Errorf("detail should explain: %q", res.Detail)
	}

	// Capabilities missing, same machine: same fix.
	env.MonomindHandshake = func(context.Context) (*monomind.VersionInfo, error) {
		return &monomind.VersionInfo{Version: "2.0.0"}, nil
	}
	if res := checkMonomindCapabilities(ctx, env); res.FixID != FixMonomindShadowed {
		t.Errorf("capabilities: %+v", res)
	}

	// No copy under the data folder yet: installing is the fix.
	env.MonomindCandidates = func() []string { return []string{"/usr/bin/monomind", "/usr/local/bin/monomind"} }
	if res := checkMonomindCapabilities(ctx, env); res.FixID != FixMonomindInstall {
		t.Errorf("no managed copy: %+v", res)
	}
	// The managed copy is the one used: installing updates it.
	env.MonomindCandidates = func() []string { return []string{managed, "/usr/bin/monomind"} }
	if res := checkMonomindCapabilities(ctx, env); res.FixID != FixMonomindInstall {
		t.Errorf("managed first: %+v", res)
	}

	// Through the runner, the manual fix carries the check's command.
	env.MonomindCandidates = func() []string { return []string{"/usr/bin/monomind", managed} }
	c := Check{ID: CheckMonomindCapabilities, Group: GroupMonomind, Run: checkMonomindCapabilities}
	reg := NewRegistry([]Check{c}, Default().fixesList())
	r := reg.Run(ctx, env, Options{}).Results[0]
	if r.Fix == nil || r.Fix.Safety != SafetyManual || !strings.Contains(r.Fix.Command, "/usr/bin/monomind") {
		t.Errorf("resolved fix: %+v", r.Fix)
	}
}

func TestMonomindBinaryListsShadowedCopies(t *testing.T) {
	env := &Env{FindMonomind: func() (string, error) { return "/usr/bin/monomind", nil },
		MonomindCandidates: func() []string { return []string{"/usr/bin/monomind", "/home/u/.monoagent/npm-global/bin/monomind"} }}
	res := checkMonomindBinary(context.Background(), env)
	if res.Status != StatusOK || !strings.Contains(res.Detail, "npm-global/bin/monomind") {
		t.Fatalf("%+v", res)
	}
}

// The profile-init confirmation says it also runs a Claude turn.
func TestProfileInitFixSaysItContactsClaude(t *testing.T) {
	f, ok := Default().Fix(FixMonomindProfileInit)
	if !ok {
		t.Fatal("no profile-init fix")
	}
	if !strings.Contains(f.Label, "Claude account") || !strings.Contains(f.Command, "claude -p") {
		t.Fatalf("label %q / command %q should say it runs claude -p on the user's account", f.Label, f.Command)
	}
}

// EEXIST on bin/monomind from another package: the error names the owner.
func TestExplainNpmClash(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks")
	}
	prefix := t.TempDir()
	bin := filepath.Join(prefix, "bin", "monomind")
	os.MkdirAll(filepath.Dir(bin), 0o755)
	os.Symlink("../lib/node_modules/monomind/bin/cli.js", bin)
	out := []string{"npm error code EEXIST", "npm error path " + bin, "npm error EEXIST: file already exists",
		"npm error File exists: " + bin, "npm error Remove the existing file and try again"}
	err := ExplainNpmClash(errors.New("npm failed: exit status 1"), out, MonomindPackage)
	if err == nil || !strings.Contains(err.Error(), `npm package "monomind"`) || !strings.Contains(err.Error(), "npm uninstall -g monomind") {
		t.Fatalf("wrapper package: %v", err)
	}

	// A plain file: remove it.
	os.Remove(bin)
	os.WriteFile(bin, []byte("x"), 0o755)
	err = ExplainNpmClash(errors.New("npm failed"), out, MonomindPackage)
	if err == nil || !strings.Contains(err.Error(), "rm "+bin) {
		t.Fatalf("plain file: %v", err)
	}
	// Scoped owner, from npm < 10's output.
	os.Remove(bin)
	os.Symlink("../lib/node_modules/@other/tool/bin.js", bin)
	err = ExplainNpmClash(errors.New("npm failed"), []string{"npm ERR! EEXIST", "npm ERR! path " + bin}, MonomindPackage)
	if err == nil || !strings.Contains(err.Error(), `"@other/tool"`) {
		t.Fatalf("scoped owner: %v", err)
	}
	// Anything else is passed through.
	plain := errors.New("network down")
	if got := ExplainNpmClash(plain, nil, MonomindPackage); got != plain {
		t.Fatalf("unrelated error changed: %v", got)
	}
}
