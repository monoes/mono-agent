package main

// The daemon runs workflows, schedules and org runs, and the OpenAI-compatible API is one part of it. When the
// settings saved with `api config` cannot be used (the row cannot be read, or a value fails its rule) the daemon
// must not stop with them: a login service starts it again and again, and nothing it runs would ever run
// (S4 of the security review of phase 6). It serves no /v1 instead and says why. A saved setting is never ignored
// to make it start: ignoring a row would serve with the defaults a server that someone had limited.

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"testing"

	"github.com/monoes/mono-agent/internal/apiconfig"
)

func TestTheDaemonStartsWithoutTheAPIWhenTheSavedSettingsCannotBeUsed(t *testing.T) {
	for name, row := range map[string]string{
		"a row that is not JSON":                        `not json`,
		"a row in a newer format":                       `{"v":2,"confinement":"any"}`,
		"a saved value that fails its rule":             `{"v":1,"max_concurrent":99}`,
		"a saved value that is not even the right type": `{"v":1,"max_concurrent":"abc"}`,
	} {
		t.Run(name, func(t *testing.T) {
			db := savedAPIDB(t, nil, nil)
			plantRow(t, db, row)
			var logs []string
			logf := func(format string, args ...any) { logs = append(logs, fmt.Sprintf(format, args...)) }

			rt, err := daemonAPIRuntime(db, apiFlags{}, logf)
			if err != nil || rt == nil {
				t.Fatalf("the daemon stopped on the saved settings: %v", err)
			}
			if rt.disabled == nil {
				t.Error("the runtime does not say that it serves nothing, and why")
			}
			// It serves nothing: no /v1 on the HTTP API's listener, no dedicated listener, nothing in the heartbeat.
			if mount := rt.mainMount("127.0.0.1:9322"); mount != nil {
				t.Error("/v1 is mounted on the HTTP API's listener")
			}
			if addr, err := rt.startV1(context.Background()); addr != "" || err != nil {
				t.Errorf("startV1 = %q, %v: a dedicated listener started", addr, err)
			}
			hb := rt.heartbeat("127.0.0.1:9322", "", "")
			if hb.APIConfinement != "" || hb.V1Confinement != "" || hb.V1Addr != "" || hb.APISettings != nil {
				t.Errorf("the heartbeat claims an API: %+v", hb)
			}
			rt.drain() // nothing to stop, and it must not hang or panic
			rt.killTurns()
			// It said why where the daemon logs, with the way out.
			joined := strings.Join(logs, "\n")
			if !strings.Contains(joined, "does not serve the OpenAI-compatible API") || !strings.Contains(joined, "api config") {
				t.Errorf("the log does not say that the API is not served and how to fix it:\n%s", joined)
			}
		})
	}
}

// A bad flag, or a bad variable, of this start is the operator's mistake of this start: the daemon still stops, as
// every other command does. Only what was saved, and may have been saved by someone else, is survived.
func TestTheDaemonStillStopsOnABadFlagOrVariable(t *testing.T) {
	db := savedAPIDB(t, nil, nil)
	for name, f := range map[string]apiFlags{
		"confinement": {confinement: "everything"}, "maximum": {maxConcurrent: -1}, "address": {v1Addr: "9443"},
	} {
		if rt, err := daemonAPIRuntime(db, f, func(string, ...any) {}); err == nil || exitCode(err) != 3 || rt != nil {
			t.Errorf("%s: runtime %v, exit %d, %v, want the error of a bad flag", name, rt, exitCode(err), err)
		}
	}
	db = savedAPIDB(t, nil, map[string]string{"MONOAGENT_API_MAX_CONCURRENT": "abc"})
	if rt, err := daemonAPIRuntime(db, apiFlags{}, func(string, ...any) {}); err == nil || exitCode(err) != 3 || rt != nil {
		t.Errorf("a bad variable: runtime %v, exit %d, %v", rt, exitCode(err), err)
	}
}

// Usable settings give the runtime they always gave: this changes nothing for anyone whose saved settings are fine.
func TestTheDaemonRuntimeIsTheUsualOneForUsableSettings(t *testing.T) {
	db := savedAPIDB(t, map[string]string{"max_concurrent": "2", "confinement": "sandboxed"}, nil)
	rt, err := daemonAPIRuntime(db, apiFlags{}, func(string, ...any) {})
	if err != nil || rt == nil || rt.disabled != nil {
		t.Fatalf("runtime %v, error %v", rt, err)
	}
	if rt.conf.MaxConcurrent != 2 || rt.override != "sandboxed" {
		t.Errorf("the saved settings were not read: %+v", rt)
	}
	if hb := rt.heartbeat("", "", ""); len(hb.APISettings) != len(apiconfig.Keys()) {
		t.Errorf("the heartbeat reports %d settings", len(hb.APISettings))
	}
}

func plantRow(t *testing.T, db *sql.DB, value string) {
	t.Helper()
	if _, err := db.Exec(`INSERT INTO settings (key, value) VALUES (?, ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value`, apiconfig.Row, value); err != nil {
		t.Fatal(err)
	}
}
