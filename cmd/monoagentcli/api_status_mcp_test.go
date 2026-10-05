package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/monoes/mono-agent/internal/daemonhb"
)

// `api status --json` and the tool api_status are one document: the listeners they probe, the keys
// they count and the auto model they report are the profile's and this process's, the same for both.
func TestAPIStatusToolIsTheDocumentOfAPIStatusJSON(t *testing.T) {
	cases := []struct {
		name    string
		daemon  bool   // a live daemon reports the main listener
		saved   bool   // a dedicated listener is saved
		surface bool   // the api_auto surface is on for the profile
		key     bool   // TYPESAFE_API_KEY is set
		want    int    // listeners in the document
		auto    string // what the document says of the auto model
	}{
		{name: "nothing saved, no daemon", want: 1, auto: "surface off"},
		{name: "a saved dedicated listener", saved: true, want: 2, auto: "surface off"},
		{name: "a daemon reports the main listener", daemon: true, want: 1, auto: "surface off"},
		{name: "auto on without a key", surface: true, want: 1, auto: "no key"},
		{name: "auto on with a key", surface: true, key: true, want: 1, auto: "available"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			db := newAPITestDB(t)
			clearAPIEnv(t)
			t.Setenv("TYPESAFE_API_KEY", "")
			main := apiServer(t, true)
			t.Setenv("MONOAGENT_HTTPAPI_ADDR", main)
			for _, name := range []string{"one", "two"} {
				if _, _, err := runAPI(t, db, "default", true, "key", "create", "--name", name); err != nil {
					t.Fatal(err)
				}
			}
			if c.saved {
				saveAt(t, db, "v1_addr="+apiServer(t, true), "confinement=sandboxed")
			}
			if c.daemon {
				t.Setenv("MONOAGENT_DAEMON_HEARTBEAT", filepath.Join(t.TempDir(), "hb.json"))
				if err := daemonhb.Write(daemonhb.Heartbeat{PID: os.Getpid(), APIAddr: main, APIConfinement: "chat-only", ContextConfinement: "chat-only", AutoConfinement: "chat-only"}); err != nil {
					t.Fatal(err)
				}
			}
			if c.surface {
				enableAPIAuto(t, db, "default")
			}
			if c.key {
				t.Setenv("TYPESAFE_API_KEY", "test-key")
			}

			cli, _, err := runAPI(t, db, "default", true, "status")
			if err != nil {
				t.Fatal(err)
			}
			tool, isErr := mcpOnce(t, mcpOptions(t, db, "default", false), "api_status", map[string]any{})
			if isErr {
				t.Fatalf("api_status failed: %s", tool)
			}

			// The case exercises what it names.
			doc := decodeStatus(t, cli)
			if len(doc.Listeners) != c.want || autoState(doc.Auto) != c.auto || doc.Keys.Active != 2 {
				t.Errorf("the case does not exercise what it names: %d listeners (want %d), auto %q (want %q), %d keys", len(doc.Listeners), c.want, autoState(doc.Auto), c.auto, doc.Keys.Active)
			}
			for _, l := range doc.Listeners {
				if !l.Reachable {
					t.Errorf("listener %+v does not answer: the probes were not exercised", l)
				}
			}
			if want := asMCPDoc(cli); tool != want {
				t.Errorf("api_status is not the document of api status --json: %s", firstDifference(want, tool))
			}
		})
	}
}
