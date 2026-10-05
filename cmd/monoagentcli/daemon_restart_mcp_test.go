package main

import (
	"errors"
	"strings"
	"testing"
)

// `daemon restart --json` and the tool api_config_apply are one document and one set of rules, over
// the same service manager (autostart.RestartRegistered over autostart.Installer): what is
// restarted, what is said when nothing is registered and when the service manager fails. Each runs
// against a fake service manager; none runs launchctl, systemctl or schtasks.
func TestAPIConfigApplyIsTheDocumentOfDaemonRestartJSON(t *testing.T) {
	db := newAPITestDB(t)
	fake := &fakeAutostart{installed: true}
	useInstaller(t, fake)

	cli, stderr, err := execDaemonRestart(t, &globalConfig{JSONOutput: true})
	if err != nil {
		t.Fatal(err)
	}
	tool, isErr := mcpOnce(t, mcpOptions(t, db, "default", false), "api_config_apply", map[string]any{})
	if isErr {
		t.Fatalf("api_config_apply failed: %s", tool)
	}
	if want := strings.TrimSuffix(cli, "\n"); tool != want {
		t.Errorf("api_config_apply is not the document of daemon restart --json: %s", firstDifference(want, tool))
	}
	if fake.restarted != 2 {
		t.Errorf("the service was restarted %d times, want once by the command and once by the tool", fake.restarted)
	}
	// What the command says first, on stderr, is in the tool's description: a host cannot read stderr.
	if !strings.Contains(stderr, "interrupts") {
		t.Errorf("stderr of the command: %q", stderr)
	}
}

func TestAPIConfigApplyRefusesWhatDaemonRestartRefusesInTheSameWords(t *testing.T) {
	for _, c := range []struct {
		name       string
		registered bool
		failure    error
		exit       int
	}{
		{"nothing is registered", false, nil, 3},
		{"the service manager fails", true, errors.New("launchctl kickstart -k gui/501/com.monoagent.daemon: exit status 113: Could not find service"), 1},
	} {
		t.Run(c.name, func(t *testing.T) {
			db := newAPITestDB(t)
			fake := &fakeAutostart{installed: c.registered, restartErr: c.failure}
			useInstaller(t, fake)

			_, _, cliErr := execDaemonRestart(t, &globalConfig{JSONOutput: true})
			if cliErr == nil || exitCodeFor(cliErr) != c.exit {
				t.Fatalf("the command: exit %d, %v", exitCodeFor(cliErr), cliErr)
			}
			text, isErr := mcpOnce(t, mcpOptions(t, db, "default", false), "api_config_apply", map[string]any{})
			if !isErr || text != cliErr.Error() {
				t.Errorf("the tool answers %q (error %v), the command %q", text, isErr, cliErr.Error())
			}
		})
	}
}
