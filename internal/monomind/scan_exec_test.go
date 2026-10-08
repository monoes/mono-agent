package monomind

import (
	"encoding/json"
	"testing"
)

// `agent scan` (monomind 2.24.1) says per runtime whether it can be run.
func TestScanEntryExecutionSupport(t *testing.T) {
	var res ScanResult
	raw := `{"v":1,"agents":[
	 {"id":"freebuff","installed":false,"execution_supported":false,"execution_unsupported_reason":"interactive-only"},
	 {"id":"kilo","installed":false,"execution_supported":true,"execution_unsupported_reason":null},
	 {"id":"old","installed":true}]}`
	if err := json.Unmarshal([]byte(raw), &res); err != nil {
		t.Fatal(err)
	}
	if r, ok := res.Find("freebuff").UnsupportedReason(); !ok || r != "interactive-only" {
		t.Errorf("freebuff = %q %v", r, ok)
	}
	for _, id := range []string{"kilo", "old"} {
		if _, ok := res.Find(id).UnsupportedReason(); ok {
			t.Errorf("%s reported unsupported", id)
		}
	}
	out, _ := json.Marshal(res.Find("freebuff"))
	var back map[string]any
	_ = json.Unmarshal(out, &back)
	if back["execution_supported"] != false || back["execution_unsupported_reason"] != "interactive-only" {
		t.Errorf("re-encoded = %s", out)
	}
}
