package main

import (
	"errors"
	"strings"
	"testing"

	"github.com/monoes/mono-agent/internal/storage"
)

// `daemon restart --json` and the tool api_config_apply are one document and one set of rules, over
// the same service manager (autostart.RestartRegistered over autostart.Installer): what is
// restarted, what is said when nothing is registered and when the service manager fails. Each runs
// against a fake service manager; none runs launchctl, systemctl or schtasks.
func TestAPIConfigApplyIsTheDocumentOfDaemonRestartJSON(t *testing.T) {
	db := newAPITestDB(t)
	fake := &fakeAutostart{installed: true}
	useInstaller(t, fake)

	cli, stderr, err := execDaemonRestart(t, &globalConfig{DBPath: db, JSONOutput: true})
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

			_, _, cliErr := execDaemonRestart(t, &globalConfig{DBPath: db, JSONOutput: true})
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

// Both read the saved settings before they restart anything (C2 of the correctness review of phase 6), and
// refuse the settings they cannot use in the same words, with the daemon not asked to restart.
func TestAPIConfigApplyRefusesUnusableSavedSettingsInTheWordsOfDaemonRestart(t *testing.T) {
	for name, c := range map[string]struct {
		row  string
		exit int
	}{
		"a row that is not JSON":            {`not json`, 3},
		"a saved value that fails its rule": {`{"v":1,"max_concurrent":99}`, 3},
		"a row in a newer format":           {`{"v":2}`, 1},
	} {
		t.Run(name, func(t *testing.T) {
			db := newAPITestDB(t)
			fake := &fakeAutostart{installed: true}
			useInstaller(t, fake)
			st, err := storage.NewDatabase(db)
			if err != nil {
				t.Fatal(err)
			}
			if err := st.ApplyMigrations(); err != nil {
				t.Fatal(err)
			}
			plantRow(t, st.DB, c.row)
			st.Close()

			_, _, cliErr := execDaemonRestart(t, &globalConfig{DBPath: db, JSONOutput: true})
			if cliErr == nil || exitCodeFor(cliErr) != c.exit {
				t.Fatalf("the command: exit %d, %v", exitCodeFor(cliErr), cliErr)
			}
			text, isErr := mcpOnce(t, mcpOptions(t, db, "default", false), "api_config_apply", map[string]any{})
			if !isErr || text != cliErr.Error() {
				t.Errorf("the tool answers %q (error %v), the command %q", text, isErr, cliErr.Error())
			}
			if fake.restarted != 0 {
				t.Errorf("the service was restarted %d times: a refused call restarts nothing", fake.restarted)
			}
		})
	}
}
