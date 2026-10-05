package daemonhb

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

// The daemon says, for each setting of the OpenAI-compatible API's server, the value it started
// with and where that came from, so that a shell, the app or an MCP host can tell what a
// restart would change.
func TestHeartbeatCarriesTheEffectiveSettingsAndTheirSources(t *testing.T) {
	t.Setenv("MONOAGENT_DAEMON_HEARTBEAT", filepath.Join(t.TempDir(), "hb.json"))
	settings := map[string]APISetting{
		"v1_addr":        {Value: "0.0.0.0:9443", Source: "saved"},
		"confinement":    {Value: "", Source: "default"},
		"max_concurrent": {Value: "8", Source: "flag"},
		"image_runtimes": {Value: "codex,antigravity", Source: "env"},
	}
	if err := Write(Heartbeat{PID: os.Getpid(), APIAddr: "127.0.0.1:9322", APISettings: settings}); err != nil {
		t.Fatal(err)
	}
	hb, live := Read()
	if !live || !reflect.DeepEqual(hb.APISettings, settings) {
		t.Fatalf("Read = %+v, live=%v", hb.APISettings, live)
	}
	raw, err := os.ReadFile(Path())
	if err != nil {
		t.Fatal(err)
	}
	// The wire form is part of the contract: one object, keyed by setting, each {value, source}.
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	const want = `{"confinement":{"value":"","source":"default"},"image_runtimes":{"value":"codex,antigravity","source":"env"},` +
		`"max_concurrent":{"value":"8","source":"flag"},"v1_addr":{"value":"0.0.0.0:9443","source":"saved"}}`
	if string(doc["api_settings"]) != want {
		t.Errorf("api_settings is %s, want %s", doc["api_settings"], want)
	}
}

// A heartbeat that predates the field has none, and reads as a live one all the same: the
// states are then unknown, not an error.
func TestAnOlderHeartbeatHasNoSettings(t *testing.T) {
	t.Setenv("MONOAGENT_DAEMON_HEARTBEAT", filepath.Join(t.TempDir(), "hb.json"))
	old := `{"v":1,"pid":` + itoa(os.Getpid()) + `,"ts":"` + time.Now().Format(time.RFC3339Nano) + `","api_addr":"127.0.0.1:9322","auto_confinement":"any"}`
	if err := os.WriteFile(Path(), []byte(old), 0o600); err != nil {
		t.Fatal(err)
	}
	hb, live := Read()
	if !live || hb.APISettings != nil || hb.AutoConfinement != "any" {
		t.Errorf("Read = %+v, live=%v", hb, live)
	}
}

// A heartbeat that has none writes no key: the file of a daemon that reports nothing is what it was.
func TestAHeartbeatWithoutSettingsWritesNoKey(t *testing.T) {
	t.Setenv("MONOAGENT_DAEMON_HEARTBEAT", filepath.Join(t.TempDir(), "hb.json"))
	if err := Write(Heartbeat{PID: os.Getpid(), APIAddr: "127.0.0.1:9322"}); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(Path())
	if strings.Contains(string(raw), "api_settings") {
		t.Errorf("api_settings must be omitted when there is none: %s", raw)
	}
}

func itoa(n int) string {
	b, _ := json.Marshal(n)
	return string(b)
}
