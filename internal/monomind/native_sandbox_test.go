package monomind

import (
	"encoding/json"
	"testing"
)

// The OpenAI-compatible API decides how much a runtime can do from
// native_sandbox: who confines its native tools. These tests pin that the
// field is decoded from both `agent scan` and the start event.

func TestScanEntryDecodesNativeSandbox(t *testing.T) {
	const payload = `{"v":1,"agents":[
	  {"id":"claude","installed":true,"native_sandbox":"monomind","sandbox_modes":["read-only","workspace-write","full"]},
	  {"id":"antigravity","installed":true,"native_sandbox":"none","sandbox_modes":["restricted","full"]},
	  {"id":"older","installed":true}
	]}`
	var res ScanResult
	if err := json.Unmarshal([]byte(payload), &res); err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"claude": "monomind", "antigravity": "none", "older": ""}
	for _, a := range res.Agents {
		if a.NativeSandbox != want[a.ID] {
			t.Errorf("%s: NativeSandbox = %q, want %q", a.ID, a.NativeSandbox, want[a.ID])
		}
	}
}

func TestStartEventDecodesNativeSandbox(t *testing.T) {
	var ev Event
	line := `{"v":1,"type":"start","runtime":"codex","access":"scoped","native_sandbox":"workspace-write","approvals":"off","sandbox_requested":"workspace-write","sandbox_applied":"workspace-write","streams_incrementally":false}`
	if err := json.Unmarshal([]byte(line), &ev); err != nil {
		t.Fatal(err)
	}
	if ev.NativeSandbox != "workspace-write" {
		t.Fatalf("NativeSandbox = %q, want workspace-write", ev.NativeSandbox)
	}

	// A monomind that predates the field never sends it.
	var older Event
	if err := json.Unmarshal([]byte(`{"v":1,"type":"start","runtime":"claude"}`), &older); err != nil {
		t.Fatal(err)
	}
	if older.NativeSandbox != "" {
		t.Fatalf("NativeSandbox = %q, want empty", older.NativeSandbox)
	}
}
