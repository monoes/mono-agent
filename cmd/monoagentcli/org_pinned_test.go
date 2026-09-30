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
// monomind. `org status` and `org run` run the installed ones.
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
	for _, name := range []string{"monomind", "node"} {
		if err := os.Symlink(filepath.Join(tools, "mise"), filepath.Join(shims, name)); err != nil {
			t.Fatal(err)
		}
	}
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
		if strings.Contains(string(b), "PLANTED") || !strings.Contains(string(b), "monomind "+filepath.Join(node, "monomind")) {
			t.Fatalf("org %v ran:\n%s", args, b)
		}
		os.Remove(log)
	}
}
