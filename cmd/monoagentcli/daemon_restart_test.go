package main

// `daemon restart` goes through internal/autostart: these tests give it a fake service manager,
// and none of them may run launchctl, systemctl or schtasks.

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/monoes/mono-agent/internal/autostart"
)

func execDaemonRestart(t *testing.T, cfg *globalConfig) (stdout, stderr string, err error) {
	t.Helper()
	cmd := newDaemonRestartCmd(cfg)
	var out, errb bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errb)
	cmd.SetArgs(nil)
	cmd.SilenceErrors, cmd.SilenceUsage = true, true
	err = cmd.Execute()
	return out.String(), errb.String(), err
}

func TestDaemonRestartJSON(t *testing.T) {
	fake := &fakeAutostart{installed: true}
	useInstaller(t, fake)
	out, stderr, err := execDaemonRestart(t, &globalConfig{JSONOutput: true})
	if err != nil {
		t.Fatal(err)
	}
	if fake.restarted != 1 {
		t.Errorf("the service was restarted %d times", fake.restarted)
	}
	var doc map[string]any
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("not a document: %v\n%s", err, out)
	}
	if len(doc) != 2 || doc["restarted"] != true || doc["via"] != autostart.ServiceManager() {
		t.Errorf("document: %v, want exactly {restarted: true, via: %s}", doc, autostart.ServiceManager())
	}
	// It says what a restart does, before it does it, on stderr: stdout stays one document.
	if !strings.Contains(stderr, "interrupts") || !strings.Contains(stderr, "workflows") {
		t.Errorf("stderr should warn that a restart interrupts what the daemon is running: %q", stderr)
	}
}

func TestDaemonRestartText(t *testing.T) {
	useInstaller(t, &fakeAutostart{installed: true})
	out, _, err := execDaemonRestart(t, &globalConfig{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "Restarted the daemon") || !strings.Contains(out, autostart.ServiceManager()) || !strings.Contains(out, "interrupted") {
		t.Errorf("the text should say what was done, through what, and what it cost:\n%s", out)
	}
}

func TestDaemonRestartWithNothingRegisteredIsInvalidInput(t *testing.T) {
	fake := &fakeAutostart{installed: false}
	useInstaller(t, fake)
	for _, jsonOut := range []bool{true, false} {
		out, _, err := execDaemonRestart(t, &globalConfig{JSONOutput: jsonOut})
		if exitCodeFor(err) != 3 || err == nil {
			t.Fatalf("json=%v: exit %d, %v", jsonOut, exitCodeFor(err), err)
		}
		for _, want := range []string{"not registered for auto-start", "daemon install"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("the message %q must say %q", err.Error(), want)
			}
		}
		if out != "" {
			t.Errorf("json=%v: stdout %q: a failed command prints no document", jsonOut, out)
		}
	}
	if fake.restarted != 0 {
		t.Error("nothing registered, and a restart was attempted")
	}
}

func TestDaemonRestartThatFailsIsAGeneralError(t *testing.T) {
	useInstaller(t, &fakeAutostart{installed: true, restartErr: errors.New("launchctl kickstart -k gui/501/com.monoagent.daemon: exit status 113: Could not find service")})
	out, _, err := execDaemonRestart(t, &globalConfig{JSONOutput: true})
	if exitCodeFor(err) != 1 || err == nil || !strings.Contains(err.Error(), "Could not find service") {
		t.Errorf("exit %d, %v", exitCodeFor(err), err)
	}
	if out != "" {
		t.Errorf("stdout %q", out)
	}
}

// It restarts a service, and has nothing to do with the database.
func TestDaemonRestartOpensNoDatabase(t *testing.T) {
	useInstaller(t, &fakeAutostart{installed: true})
	file := filepath.Join(t.TempDir(), "not-a-folder")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	// A database path whose parent is a file cannot be opened.
	if _, _, err := execDaemonRestart(t, &globalConfig{DBPath: filepath.Join(file, "x.db")}); err != nil {
		t.Errorf("daemon restart needed the database: %v", err)
	}
}

func TestDaemonRestartIsASubcommandOfDaemon(t *testing.T) {
	found := false
	for _, c := range newDaemonCmd(&globalConfig{}).Commands() {
		if c.Name() == "restart" {
			found = true
		}
	}
	if !found {
		t.Error("`monoagentcli daemon restart` does not exist")
	}
}
