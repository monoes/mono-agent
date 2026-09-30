package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/monoes/mono-agent/internal/monomind"
)

// TestOrgCommandsNeverRunProjectPlantedMonomind is #301 end to end through
// the CLI: monomind is reached through a mise shim, and the project's
// .tool-versions points node at a planted .cache/n with its own node and
// monomind, codex and opencode. `org status` and `org run` run the
// installed monomind and node, and the codex the org's sessions start is
// the installed one; opencode, not installed, doesn't start at all.
func TestOrgCommandsNeverRunProjectPlantedMonomind(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell shims are unix-only")
	}
	testdata, err := filepath.Abs(filepath.Join("..", "..", "internal", "monomind", "testdata"))
	if err != nil {
		t.Fatal(err)
	}
	home, project := t.TempDir(), t.TempDir()
	data := filepath.Join(home, ".local", "share", "mise")
	node := filepath.Join(data, "installs", "node", "22.0.0", "bin")
	log := filepath.Join(t.TempDir(), "pin.log")
	for k, v := range map[string]string{
		"HOME": home, "MISE_DATA_DIR": data, "PIN_LOG": log, "MONOMIND_BIN": "",
		"XDG_DATA_HOME": "", "RTX_DATA_DIR": "", "ASDF_DATA_DIR": "", "VOLTA_HOME": "", "MISE_NODE_VERSION": "",
		"MONOAGENT_DAEMON_HEARTBEAT": filepath.Join(home, "hb.json"), "MONOAGENT_API_ADDR": "",
	} {
		t.Setenv(k, v)
	}
	handshake, _ := json.Marshal(monomind.VersionInfo{V: monomind.ProtocolVersion, Version: "9.0.0", MinCaller: "1.0.0", Capabilities: monomind.RequiredCapabilities})
	if out, err := exec.Command("sh", filepath.Join(testdata, "make-shim-fixture.sh"), node, node, project, string(handshake)).CombinedOutput(); err != nil {
		t.Fatalf("fixture: %v: %s", err, out)
	}
	src, err := os.ReadFile(filepath.Join(testdata, "fake-mise.sh"))
	if err != nil {
		t.Fatal(err)
	}
	tools, shims := t.TempDir(), filepath.Join(data, "shims")
	if err := os.WriteFile(filepath.Join(tools, "mise"), src, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(shims, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"monomind", "node", "codex", "opencode"} {
		if err := os.Symlink(filepath.Join(tools, "mise"), filepath.Join(shims, name)); err != nil {
			t.Fatal(err)
		}
	}
	codex := filepath.Join(data, "installs", "npm-openai-codex", "1.0.0", "bin", "codex")
	if err := os.MkdirAll(filepath.Dir(codex), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(codex, []byte("#!/bin/sh\necho \"codex $0\" >>\"$PIN_LOG\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FAKE_RUNTIMES", "codex opencode")
	t.Setenv("PATH", shims+string(os.PathListSeparator)+"/usr/bin"+string(os.PathListSeparator)+"/bin")
	monomind.ResetCapabilityCache()
	t.Cleanup(monomind.ResetCapabilityCache)
	t.Chdir(project)

	for _, args := range [][]string{{"status", "growth"}, {"run", "growth"}} {
		cmd := newOrgCmd(&globalConfig{})
		cmd.SetArgs(append(args, "--project", project))
		var runErr error
		captureStdout(t, func() { runErr = cmd.Execute() })
		if runErr != nil {
			t.Fatalf("org %v: %v", args, runErr)
		}
		b, _ := os.ReadFile(log)
		if strings.Contains(string(b), "PLANTED") || !strings.Contains(string(b), "monomind "+filepath.Join(node, "monomind")) ||
			!strings.Contains(string(b), "codex "+codex) {
			t.Fatalf("org %v ran:\n%s", args, b)
		}
		os.Remove(log)
	}

	// PATH entries that resolve in the project (relative ones, a direnv
	// `PATH_add bin`) and an override naming a project file supply
	// nothing: opencode, not installed, still doesn't start, and codex,
	// overridden to a project file, doesn't start at all (it is pointed at
	// the missing unpinned path, so it isn't looked up by name either).
	for _, dir := range []string{"node_modules/.bin", "bin"} {
		p := filepath.Join(project, dir, "opencode")
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("#!/bin/sh\necho \"PLANTED $0\" >>\"$PIN_LOG\"\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	sep := string(os.PathListSeparator)
	t.Setenv("PATH", strings.Join([]string{"node_modules/.bin", ".", filepath.Join(project, "bin"), shims, "/usr/bin", "/bin"}, sep))
	t.Setenv("CODEX_CLI_BIN", filepath.Join(project, ".cache", "n", "bin", "codex"))
	monomind.ResetCapabilityCache()
	cmd := newOrgCmd(&globalConfig{})
	cmd.SetArgs([]string{"status", "growth", "--project", project})
	var runErr error
	captureStdout(t, func() { runErr = cmd.Execute() })
	if runErr != nil {
		t.Fatal(runErr)
	}
	if b, _ := os.ReadFile(log); strings.Contains(string(b), "PLANTED") || strings.Contains(string(b), "codex ") {
		t.Fatalf("org status with project PATH entries ran:\n%s", b)
	}
}
