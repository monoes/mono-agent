package mcp

// api_config_apply restarts the daemon through the service manager it is registered with, so that it
// reads the settings saved with api_config_set: what `monoagentcli daemon restart` does, through the
// same function (autostart.RestartRegistered) over the same autostart.Installer. Every test gives
// the server a fake service manager: none runs launchctl, systemctl or schtasks.

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/monoes/mono-agent/internal/autostart"
)

func TestAPIConfigApplyRestartsTheRegisteredDaemon(t *testing.T) {
	f := newConfigFixture(t, configSetup{registered: true})
	text := f.mustCall("api_config_apply", nil)

	// The document of `daemon restart --json`: {restarted, via}, and nothing else.
	want := "{\n  \"restarted\": true,\n  \"via\": \"" + autostart.ServiceManager() + "\"\n}"
	if text != want {
		t.Errorf("the result is\n%s\nwant\n%s", text, want)
	}
	// It asks whether the daemon is registered before it restarts anything, and restarts once.
	if got := f.Installer.everything(); got != "status,restart" {
		t.Errorf("the service manager was asked %q, want status and then restart", got)
	}
}

// A daemon that is not registered for auto-start is restarted by nothing: the error says what the
// user can do, and the service manager is not asked to restart.
func TestAPIConfigApplyOfADaemonThatIsNotRegisteredSaysWhatTheUserCanDo(t *testing.T) {
	f := newConfigFixture(t, configSetup{registered: false})
	_, err := f.call("api_config_apply", nil)
	if err == nil {
		t.Fatal("nothing is registered, and the call did not fail")
	}
	var notRegistered *autostart.NotRegisteredError
	if !errors.As(err, &notRegistered) {
		t.Errorf("the error is %T, want the *autostart.NotRegisteredError of the command", err)
	}
	for _, want := range []string{"not registered for auto-start", "stop it and start `monoagentcli daemon` again", "monoagentcli daemon install"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the error does not say %q: %v", want, err)
		}
	}
	if f.Installer.count("restart") != 0 {
		t.Error("a restart was attempted for a daemon that is not registered")
	}
}

func TestAPIConfigApplyThatFailsPassesTheServiceManagersWordsOn(t *testing.T) {
	f := newConfigFixture(t, configSetup{registered: true})
	f.Installer.restartErr = errors.New("launchctl kickstart -k gui/501/com.monoagent.daemon: exit status 113: Could not find service")
	_, err := f.call("api_config_apply", nil)
	if err == nil || !strings.Contains(err.Error(), "Could not find service") {
		t.Errorf("a service manager that fails: %v", err)
	}
}

// It is a mutating tool, and destructive: it interrupts what the daemon is running. Omitted from
// tools/list and refused by name without --allow-mutations (which --allow-api-exposure does not
// replace), and nothing is restarted.
func TestAPIConfigApplyIsGatedAndAnnotatedDestructive(t *testing.T) {
	closed := newConfigFixture(t, configSetup{readOnly: true, registered: true})
	if toolsListNames(t, closed.Server)["api_config_apply"] {
		t.Error("api_config_apply restarts the daemon and must not be listed without --allow-mutations")
	}
	for _, setup := range []configSetup{{readOnly: true, registered: true}, {readOnly: true, allowExposure: true, registered: true}} {
		f := newConfigFixture(t, setup)
		if _, err := f.call("api_config_apply", nil); err == nil || !strings.Contains(err.Error(), "--allow-mutations") {
			t.Errorf("%+v: %v, want a refusal that names --allow-mutations", setup, err)
		}
		if f.Installer.everything() != "" {
			t.Errorf("%+v: a refused call asked the service manager: %q", setup, f.Installer.everything())
		}
	}
	open := newConfigFixture(t, configSetup{registered: true})
	if !toolsListNames(t, open.Server)["api_config_apply"] {
		t.Error("api_config_apply must be listed with --allow-mutations")
	}
	var got map[string]bool
	for _, def := range toolDefinitions(true) {
		if def["name"] == "api_config_apply" {
			got, _ = def["annotations"].(map[string]bool)
		}
	}
	if want := map[string]bool{"readOnlyHint": false, "destructiveHint": true}; !reflect.DeepEqual(got, want) {
		t.Errorf("annotations %v, want %v", got, want)
	}
}

// It takes no argument and changes no setting: it applies what is saved, and what a model sends it
// is not saved.
func TestAPIConfigApplyIgnoresWhatItIsSentAndSavesNothing(t *testing.T) {
	f := newConfigFixture(t, configSetup{registered: true})
	f.mustCall("api_config_apply", map[string]any{"set": map[string]any{"max_concurrent": "9"}, "unset": "all", "confirm": true})
	if !f.saved().IsEmpty() {
		t.Errorf("a restart saved something: %+v", f.saved())
	}
	if f.Installer.count("restart") != 1 {
		t.Errorf("restarted %d times, want once", f.Installer.count("restart"))
	}
}

// The description is what stands between a model and an interrupted workflow: it says so, says that
// it does not save anything or promise a graceful stop, and says when it cannot work.
func TestAPIConfigApplyDescriptionSaysWhatItInterrupts(t *testing.T) {
	var d string
	for _, tl := range apiConfigTools() {
		if tl.name == "api_config_apply" {
			d = tl.description
		}
	}
	for _, want := range []string{"daemon restart --json", "interrupts", "workflows", "org runs", "api_config_set", "api_config_get", "pending_restart", "not registered"} {
		if !strings.Contains(d, want) {
			t.Errorf("the description does not mention %q: %s", want, d)
		}
	}
	// How the daemon ends is the service manager's, and nothing here may promise more.
	if strings.Contains(strings.ToLower(d), "graceful") {
		t.Errorf("the description claims a graceful stop, which the service managers do not promise: %s", d)
	}
}
