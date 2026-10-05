package main

// The daemon's heartbeat says, for each setting of the API's server, the value it started with
// and where it came from.

import (
	"testing"

	"github.com/monoes/mono-agent/internal/apiconfig"
	"github.com/monoes/mono-agent/internal/daemonhb"
)

func TestTheDaemonsHeartbeatCarriesEverySettingAndItsSource(t *testing.T) {
	rt := savedAPIRuntime(t,
		apiFlags{v1Addr: "127.0.0.1:1", confinement: "chat-only", contextConfinement: "any", autoConfinement: "sandboxed", maxConcurrent: 8},
		// Saved under every flag and under the environment, and for two settings only saved.
		map[string]string{
			"v1_addr": "127.0.0.1:0", "confinement": "any", "context_confinement": "sandboxed", "auto_confinement": "any", "max_concurrent": "2",
			"tls_cert_file": "/s/c.pem", "tls_key_file": "/s/k.pem", "turn_timeout": "30m", "image_runtimes": "none", "tool_runtimes": "none",
		},
		map[string]string{"MONOAGENT_API_TURN_TIMEOUT": "900s", "MONOAGENT_API_TOOL_RUNTIMES": "agy, Codex", "MONOAGENT_API_TLS_CERT": "/e/c.pem"},
	)
	hb := rt.heartbeat("127.0.0.1:9322", "127.0.0.1:9999", "127.0.0.1:55555")

	want := map[string]daemonhb.APISetting{
		"v1_addr":             {Value: "127.0.0.1:1", Source: "flag"},
		"tls_cert_file":       {Value: "/e/c.pem", Source: "env"}, // one variable takes the whole pair from the environment...
		"tls_key_file":        {Value: "", Source: "env"},         // ...whatever is saved for the other file
		"confinement":         {Value: "chat-only", Source: "flag"},
		"context_confinement": {Value: "any", Source: "flag"},
		"auto_confinement":    {Value: "sandboxed", Source: "flag"},
		"max_concurrent":      {Value: "8", Source: "flag"},
		"turn_timeout":        {Value: "15m", Source: "env"}, // the canonical spelling, whoever wrote it
		"image_runtimes":      {Value: "none", Source: "saved"},
		"tool_runtimes":       {Value: "antigravity,codex", Source: "env"},
	}
	if len(hb.APISettings) != len(want) {
		t.Fatalf("%d settings, want %d: %+v", len(hb.APISettings), len(want), hb.APISettings)
	}
	for _, key := range apiconfig.Keys() {
		if got := hb.APISettings[key]; got != want[key] {
			t.Errorf("%s = %+v, want %+v", key, got, want[key])
		}
	}

	// What the heartbeat said before is where it was.
	if hb.APIAddr != "127.0.0.1:9322" || hb.BridgeAddr != "127.0.0.1:9999" || hb.V1Addr != "127.0.0.1:55555" || hb.Version != getVersion() {
		t.Errorf("addresses and version: %+v", hb)
	}
	if hb.APIConfinement != "chat-only" || hb.V1Confinement != "chat-only" || hb.ContextConfinement != "any" || hb.AutoConfinement != "sandboxed" {
		t.Errorf("policies: %+v", hb)
	}
}

// Settings that are only saved are what the daemon started with, and say so.
func TestTheHeartbeatSaysWhenASettingIsOnlySaved(t *testing.T) {
	rt := savedAPIRuntime(t, apiFlags{}, map[string]string{"v1_addr": "127.0.0.1:0", "confinement": "sandboxed", "max_concurrent": "3", "turn_timeout": "20m"}, nil)
	hb := rt.heartbeat("127.0.0.1:9322", "", "127.0.0.1:55555")
	for key, want := range map[string]daemonhb.APISetting{
		"v1_addr": {Value: "127.0.0.1:0", Source: "saved"}, "confinement": {Value: "sandboxed", Source: "saved"},
		"max_concurrent": {Value: "3", Source: "saved"}, "turn_timeout": {Value: "20m", Source: "saved"},
		"auto_confinement": {Value: "chat-only", Source: "default"},
	} {
		if got := hb.APISettings[key]; got != want {
			t.Errorf("%s = %+v, want %+v", key, got, want)
		}
	}
}

// With nothing set anywhere every setting is at its default, and says so.
func TestTheHeartbeatOfADaemonWithNothingSetIsAllDefaults(t *testing.T) {
	rt := savedAPIRuntime(t, apiFlags{}, nil, nil)
	hb := rt.heartbeat("127.0.0.1:9322", "", "")
	for _, key := range apiconfig.Keys() {
		got, ok := hb.APISettings[key]
		if !ok || got.Source != "default" {
			t.Errorf("%s = %+v (present %v), want the default", key, got, ok)
		}
	}
	if got := hb.APISettings["max_concurrent"].Value; got != "4" {
		t.Errorf("max_concurrent default: %q", got)
	}
	if got := hb.APISettings["turn_timeout"].Value; got != "10m" {
		t.Errorf("turn_timeout default: %q", got)
	}
	if hb.BridgeAddr != "" || hb.V1Addr != "" || hb.V1Confinement != "" {
		t.Errorf("no bridge and no dedicated listener: %+v", hb)
	}
}
