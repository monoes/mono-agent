package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/monoes/mono-agent/internal/daemonhb"
)

// A daemon that took the saved address and the saved certificate and could not bring the dedicated listener up
// (a file that cannot be read, a port in use) logs it and reports no address for it. `show` must not say that
// the settings are applied (C1 of the correctness review of phase 6): it says that the listener is not up, in
// the JSON as a state and in the text as words and a note.
func TestAPIConfigShowSaysWhenTheDedicatedListenerIsNotUp(t *testing.T) {
	db := configTest(t)
	useInstaller(t, &fakeAutostart{installed: true})
	t.Setenv("MONOAGENT_DAEMON_HEARTBEAT", filepath.Join(t.TempDir(), "hb.json"))
	cert, key := filepath.Join(t.TempDir(), "c.pem"), filepath.Join(t.TempDir(), "k.pem")
	saveAt(t, db, "v1_addr=0.0.0.0:9443", "tls_cert_file="+cert, "tls_key_file="+key)
	running := map[string]daemonhb.APISetting{
		"v1_addr": {Value: "0.0.0.0:9443", Source: "saved"}, "tls_cert_file": {Value: cert, Source: "saved"},
		"tls_key_file": {Value: key, Source: "saved"}, "max_concurrent": {Value: "4", Source: "default"},
	}
	if err := daemonhb.Write(daemonhb.Heartbeat{PID: os.Getpid(), APISettings: running}); err != nil { // no V1Addr: it did not come up
		t.Fatal(err)
	}

	out, _, err := runAPI(t, db, "default", true, "config", "show")
	if err != nil {
		t.Fatal(err)
	}
	r := decodeConfig(t, out)
	states := map[string]string{}
	for _, row := range r.Settings {
		states[row.Key] = row.State
	}
	for _, key := range []string{"v1_addr", "tls_cert_file", "tls_key_file"} {
		if states[key] != "not_serving" {
			t.Errorf("%s: state %q, want not_serving", key, states[key])
		}
	}
	if states["max_concurrent"] != "applied" || r.RestartNeeded {
		t.Errorf("max_concurrent %q, restart needed %v: only the listener's settings are not serving, and a restart is not what cures it", states["max_concurrent"], r.RestartNeeded)
	}

	text, _, err := runAPI(t, db, "default", false, "config", "show")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"listener not up", "dedicated /v1 listener is not up (v1_addr, tls_cert_file, tls_key_file)", "log says why", "restart"} {
		if !strings.Contains(text, want) {
			t.Errorf("the text must say %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "Restart needed for") {
		t.Errorf("nothing is pending, so the text must not say a restart is needed:\n%s", text)
	}

	// The same daemon with its listener up: applied, and no note.
	if err := daemonhb.Write(daemonhb.Heartbeat{PID: os.Getpid(), APISettings: running, V1Addr: "[::]:9443"}); err != nil {
		t.Fatal(err)
	}
	out, _, err = runAPI(t, db, "default", true, "config", "show")
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range decodeConfig(t, out).Settings {
		if _, reported := running[row.Key]; reported && row.State != "applied" {
			t.Errorf("%s with the listener up: %q, want applied", row.Key, row.State)
		}
	}
	if text, _, _ := runAPI(t, db, "default", false, "config", "show"); strings.Contains(text, "is not up") {
		t.Errorf("the note is for a listener that is not up:\n%s", text)
	}
}
