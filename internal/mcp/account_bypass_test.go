package mcp

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/monoes/mono-agent/internal/account/accounttest"
)

// Account gate B3b, Task 9 (issue #368): no shape of a JSON-RPC line starts a
// tool while the account is locked. A line the server answers as a call gets the
// refusal; a line it takes for a notification, a batch, a different method
// spelling or garbage runs nothing and answers nothing but a protocol error.
func TestNoShapeOfALineRunsAToolWhileLocked(t *testing.T) {
	s := newTestServerAllowMutations(t, true)
	accounttest.Install(t, accounttest.LockedNoLogin)

	call := func(id string) string {
		return `{"jsonrpc":"2.0","id":` + id + `,"method":"tools/call","params":{"name":"workflow_run","arguments":{"id":"x"}}}`
	}
	// Taken for calls: every id the server accepts, each refused with the tool error.
	for _, id := range []string{`1`, `0`, `"a"`, `""`, `1.5`, `[]`, `{}`, `false`, `true`} {
		resp := s.handleLine(context.Background(), []byte(call(id)))
		if resp == nil {
			t.Errorf("id %s: a call got no answer", id)
			continue
		}
		raw, _ := json.Marshal(resp)
		if !strings.Contains(string(raw), `"isError":true`) || !strings.Contains(string(raw), loginRequiredText) {
			t.Errorf("id %s: answered %s, want the login-required tool error", id, raw)
		}
	}
	// Taken for notifications (no answer, no run), or for no method of the server.
	for name, line := range map[string]string{
		"no id":                   `{"jsonrpc":"2.0","method":"tools/call","params":{"name":"workflow_run","arguments":{"id":"x"}}}`,
		"null id":                 strings.Replace(call("null"), `"id":null`, `"id":null`, 1),
		"upper-case method":       strings.Replace(call("1"), "tools/call", "Tools/Call", 1),
		"padded method":           strings.Replace(call("1"), "tools/call", " tools/call", 1),
		"method with a suffix":    strings.Replace(call("1"), "tools/call", "tools/call/", 1),
		"notification prefix":     strings.Replace(call("null"), "tools/call", "notifications/tools/call", 1),
		"a batch":                 "[" + call("1") + "," + call("2") + "]",
		"a wrong version":         strings.Replace(call("1"), `"2.0"`, `"1.0"`, 1),
		"a second method (last)":  `{"jsonrpc":"2.0","id":1,"method":"tools/call","method":"ping","params":{"name":"workflow_run","arguments":{"id":"x"}}}`,
		"a second method (first)": `{"jsonrpc":"2.0","id":1,"method":"ping","method":"tools/call","params":{"name":"workflow_run","arguments":{"id":"x"}}}`,
	} {
		resp := s.handleLine(context.Background(), []byte(line))
		if resp == nil {
			continue // a notification: nothing ran, nothing answered
		}
		raw, _ := json.Marshal(resp)
		// Whatever it answered, it is not the result of a tool: the refusal, a
		// protocol error or the result of the method the line really named.
		if strings.Contains(string(raw), `"isError":false`) || strings.Contains(string(raw), "workflow_run") {
			t.Errorf("%s: answered %s", name, raw)
		}
	}
}
