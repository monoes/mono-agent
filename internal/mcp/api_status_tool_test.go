package mcp

// api_status is the document of `monoagentcli api status --json`, built by the same function
// (apiconfig.BuildStatus) from the same inputs, for the MCP server's own profile and environment.

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/monoes/mono-agent/internal/apiconfig"
	"github.com/monoes/mono-agent/internal/apikeys"
	"github.com/monoes/mono-agent/internal/daemonhb"
	"github.com/monoes/mono-agent/internal/jev/jevconf"
)

const defaultMainBase = "http://127.0.0.1:9322" // the main listener when nothing names another

func TestAPIStatusIsTheDocumentOfBuildStatusForTheServersProfile(t *testing.T) {
	f := newConfigFixture(t, configSetup{})
	store := apikeys.NewStore(f.Side.DB)
	for _, name := range []string{"one", "two"} {
		if _, _, err := store.Create(context.Background(), "default", name, false); err != nil {
			t.Fatal(err)
		}
	}
	f.answers(defaultMainBase, true)

	text := f.mustCall("api_status", nil)
	st := decodeStatusReport(t, text)
	if st.V != 1 || st.Profile != "default" || st.Keys.Active != 2 || st.Daemon.Running {
		t.Errorf("status %+v", st)
	}
	if st.Auto.Available || !strings.Contains(st.Auto.Missing, "jev enable api_auto") {
		t.Errorf("the auto surface is off for the profile: %+v", st.Auto)
	}
	if len(st.Listeners) != 1 {
		t.Fatalf("listeners %+v", st.Listeners)
	}
	main := st.Listeners[0]
	if main.Name != "main" || main.Addr != "127.0.0.1:9322" || !main.Loopback || !main.V1 || !main.Reachable || !main.V1Answers || main.Scheme != "http" ||
		main.Confinement != "any" || main.ConfinementSource != "environment" || main.ContextConfinement != "chat-only" || main.AutoConfinement != "chat-only" {
		t.Errorf("main listener %+v", main)
	}

	// It is the document BuildStatus builds, byte for byte, and no other.
	want, err := apiconfig.BuildStatus(context.Background(), f.Side.DB, apiconfig.Env{Installer: f.Installer, Heartbeat: f.heartbeat, Probe: f.probe}, "default")
	if err != nil {
		t.Fatal(err)
	}
	wantText, _ := json.MarshalIndent(want, "", "  ")
	if text != string(wantText) {
		t.Errorf("the tool's document is not BuildStatus's:\n%s\nvs\n%s", text, wantText)
	}
	// What answered is what the server was told answers: nothing here reached the network.
	if len(f.probed) == 0 {
		t.Error("the listeners were not probed through the server's own probe")
	}
}

// What a server started now would listen on includes what was saved: the dedicated listener of a
// saved v1_addr is listed, and the saved confinement is the one assumed.
func TestAPIStatusReadsTheSavedSettings(t *testing.T) {
	f := newConfigFixture(t, configSetup{})
	f.save("v1_addr=127.0.0.1:9443", "confinement=sandboxed", "context_confinement=sandboxed", "auto_confinement=sandboxed")
	f.answers(defaultMainBase, true)
	f.answers("http://127.0.0.1:9443", true)

	st := decodeStatusReport(t, f.mustCall("api_status", nil))
	if len(st.Listeners) != 2 || st.Listeners[0].Name != "main" || st.Listeners[1].Name != "v1" || st.Listeners[1].Addr != "127.0.0.1:9443" {
		t.Fatalf("the saved dedicated listener should be listed: %+v", st.Listeners)
	}
	for _, l := range st.Listeners {
		if l.Confinement != "sandboxed" || l.ConfinementSource != "environment" || l.ContextConfinement != "sandboxed" || l.AutoConfinement != "sandboxed" {
			t.Errorf("%s listener: %+v", l.Name, l)
		}
	}
	// This server's environment is over what is saved.
	t.Setenv("MONOAGENT_API_CONFINEMENT", "chat-only")
	st = decodeStatusReport(t, f.mustCall("api_status", nil))
	if st.Listeners[0].Confinement != "chat-only" {
		t.Errorf("the environment over the saved settings: %+v", st.Listeners[0])
	}
}

// A running daemon is believed about what it serves and applies, saved settings or not.
func TestAPIStatusBelievesTheRunningDaemon(t *testing.T) {
	f := newConfigFixture(t, configSetup{})
	f.save("confinement=any")
	f.daemon(daemonhb.Heartbeat{APIAddr: "127.0.0.1:9322", APIConfinement: "chat-only", V1Addr: "0.0.0.0:9443", V1Confinement: "chat-only", ContextConfinement: "chat-only", AutoConfinement: "chat-only"})

	st := decodeStatusReport(t, f.mustCall("api_status", nil))
	if !st.Daemon.Running || st.Daemon.APIAddr != "127.0.0.1:9322" || st.Daemon.V1Addr != "0.0.0.0:9443" || len(st.Listeners) != 2 {
		t.Fatalf("daemon %+v, listeners %+v", st.Daemon, st.Listeners)
	}
	for _, l := range st.Listeners {
		if l.Confinement != "chat-only" || l.ConfinementSource != "daemon" {
			t.Errorf("the daemon applies chat-only: %+v", l)
		}
	}
}

// The counts and the auto surface are the server's profile's, by its id: the profile of a server
// opened by name, whose id is neither "default" nor that name.
func TestAPIStatusIsOfTheServersProfileByItsID(t *testing.T) {
	f := newConfigFixture(t, configSetup{work: true})
	t.Setenv("TYPESAFE_API_KEY", "test-key")
	store := apikeys.NewStore(f.Side.DB)
	for profile, name := range map[string]string{"default": "theirs", workProfileID: "mine"} {
		if _, _, err := store.Create(context.Background(), profile, name, false); err != nil {
			t.Fatal(err)
		}
	}
	if err := jevconf.SetEnabled(f.Side.DB, "default", jevconf.APIAuto, true); err != nil {
		t.Fatal(err)
	}

	st := decodeStatusReport(t, f.mustCall("api_status", nil))
	if st.Profile != workProfileID || st.Keys.Active != 1 {
		t.Errorf("profile %q with %d active keys, want %s with 1: only this profile's keys count", st.Profile, st.Keys.Active, workProfileID)
	}
	if st.Auto.Available || !strings.Contains(st.Auto.Missing, "jev enable api_auto") {
		t.Errorf("auto is on for the default profile only: %+v", st.Auto)
	}
	if err := jevconf.SetEnabled(f.Side.DB, workProfileID, jevconf.APIAuto, true); err != nil {
		t.Fatal(err)
	}
	if st = decodeStatusReport(t, f.mustCall("api_status", nil)); !st.Auto.Available || st.Auto.KeySource != "env" {
		t.Errorf("auto is on for this profile and a key is in the environment: %+v", st.Auto)
	}
}

// A setting that fails its rule stops a server, so it stops this report, naming the setting, and a
// bad value in this server's environment is not repeated.
func TestAPIStatusRefusesSettingsThatFailTheirRules(t *testing.T) {
	f := newConfigFixture(t, configSetup{})
	f.save("max_concurrent=0")
	if _, err := f.call("api_status", nil); err == nil || !strings.Contains(err.Error(), "max_concurrent must be") {
		t.Errorf("a saved max_concurrent of 0: %v", err)
	}

	f = newConfigFixture(t, configSetup{})
	t.Setenv("MONOAGENT_API_CONFINEMENT", "everything-supersecret")
	_, err := f.call("api_status", nil)
	if err == nil || strings.Contains(err.Error(), "supersecret") {
		t.Fatalf("a bad MONOAGENT_API_CONFINEMENT: %v, want an error that does not repeat the value", err)
	}
	if !strings.Contains(err.Error(), "MONOAGENT_API_CONFINEMENT") {
		t.Errorf("the error must say where to look: %v", err)
	}
}
