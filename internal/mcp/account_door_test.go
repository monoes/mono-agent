package mcp

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/monoes/mono-agent/internal/account/accounttest"
	"github.com/monoes/mono-agent/internal/accountdoor/doortest"
	"github.com/monoes/mono-agent/internal/orggrant"
)

const loginRequiredText = "Log in to monoes.me first: monoagentcli account login"

// byID indexes responses by JSON-RPC id: requests run on goroutines, so the
// order of the lines is not the order of the requests.
func byID(t *testing.T, resps []map[string]json.RawMessage) map[string]map[string]json.RawMessage {
	t.Helper()
	out := map[string]map[string]json.RawMessage{}
	for _, r := range resps {
		out[string(r["id"])] = r
	}
	return out
}

// The handshake and the tool list still answer while locked, so a client can
// connect and tell its user what to do; every tools/call is refused with a tool
// error (isError) whatever it names, malformed or not, mutating or not.
func TestToolsCallIsRefusedWhileLocked(t *testing.T) {
	for _, c := range doortest.Modes {
		t.Run(c.Name, func(t *testing.T) {
			s := newTestServerAllowMutations(t, true)
			accounttest.Install(t, c.Mode)

			lines := []string{
				request(1, "initialize", map[string]interface{}{"protocolVersion": "2024-11-05"}),
				request(2, "tools/list", nil),
				callToolReq(3, "workflow_list", map[string]interface{}{}),
			}
			if c.Refused { // allowed, these would start an engine (it binds the webhook port) or fail to parse: sent only where they are refused at once
				lines = append(lines,
					callToolReq(4, "workflow_run", map[string]interface{}{"id": "x"}), // mutating
					callToolReq(5, "no_such_tool", map[string]interface{}{}),
					request(6, "tools/call", "not an object")) // malformed params
			}
			resps := byID(t, serveLines(t, s, lines...))
			if len(resps) != len(lines) {
				t.Fatalf("got %d responses, want %d", len(resps), len(lines))
			}

			var list struct {
				Tools []struct{} `json:"tools"`
			}
			if _, ok := resps["1"]["result"]; !ok || json.Unmarshal(resps["2"]["result"], &list) != nil || len(list.Tools) < 8 {
				t.Errorf("the handshake and the full tool list must answer in every state: %s %s", resps["1"]["error"], resps["2"]["error"])
			}
			for id := range resps {
				if id == "1" || id == "2" {
					continue
				}
				// A protocol error carries no result for toolText to read: report it first.
				if _, isProtocolError := resps[id]["error"]; isProtocolError {
					t.Errorf("call %s: a protocol error %s, want a tool result", id, resps[id]["error"])
					continue
				}
				text, isErr := toolText(t, resps[id])
				if refused := isErr && text == loginRequiredText; refused != c.Refused {
					t.Errorf("call %s = %q (isError %v), want refused = %v", id, text, isErr, c.Refused)
				}
			}
		})
	}
}

// Grant mode (monomind's role tool provider) is the same door: it lists the
// granted tools, and a call is refused before any run is created.
func TestGrantModeToolsCallIsRefusedWhileLocked(t *testing.T) {
	for _, c := range doortest.Modes {
		t.Run(c.Name, func(t *testing.T) {
			f := newGrantFixture(t, orggrant.Tool{Wait: true})
			accounttest.Install(t, c.Mode)

			resps := byID(t, serveLines(t, f.server,
				request(1, "tools/list", map[string]interface{}{}),
				callToolReq(2, "automation_publish", map[string]interface{}{"text": "hi"}),
			))
			var list struct {
				Tools []struct {
					Name string `json:"name"`
				} `json:"tools"`
			}
			if err := json.Unmarshal(resps["1"]["result"], &list); err != nil || len(list.Tools) != 3 {
				t.Errorf("grant tools/list: %d tools, err %v, want 3", len(list.Tools), err)
			}
			text, isErr := toolText(t, resps["2"])
			if c.Refused {
				if !isErr || text != loginRequiredText {
					t.Errorf("automation_publish = %q (isError %v), want the login-required tool error", text, isErr)
				}
			} else if !strings.HasPrefix(text, codeDaemonRequired) {
				// No daemon runs here: the call got as far as the grant's own check.
				t.Errorf("automation_publish = %q, want it to reach the grant (daemon_required)", text)
			}
			var runs int
			if err := f.db.DB.QueryRow(`SELECT COUNT(*) FROM workflow_executions`).Scan(&runs); err != nil {
				t.Fatal(err)
			}
			if runs != 0 {
				t.Errorf("%d runs were created", runs)
			}
		})
	}
}
