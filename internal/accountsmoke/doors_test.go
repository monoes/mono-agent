//go:build devaccount && !windows

package accountsmoke

import (
	"strings"
	"testing"
	"time"
)

// Acceptance 1, door by door, in the real processes (the wire shapes are B3b's). The daemon is a
// serving command: started with no session after the date it stays up, logs the command to run,
// reports account: locked in /health and its heartbeat, runs nothing, and every door refuses.
// Signing in opens every door without a restart.
func TestEveryDoorRefusesWhenLockedAndOpensWhenSignedIn(t *testing.T) {
	r := newRig(t, rigOptions{enforce: past})
	d := r.startDaemon()
	r.start("extension", "serve")
	ask := r.bridge()
	mcp := r.mcp(nil, "mcp")
	mcp.call("initialize", map[string]any{})
	api, hook := "http://"+r.addr("api"), "http://"+r.addr("webhook")

	// Locked.
	if !d.alive() || !strings.Contains(r.read("daemon.log"), "monoagentcli account login") {
		t.Fatalf("a locked daemon stays up and logs the command to run:\n%s", r.read("daemon.log"))
	}
	_, hb := r.heartbeatAccount()
	hb.is(t, "locked", true, false)
	r.health(api).is(t, "locked", true, false)
	if a := probe(t, "GET", api+"/workflows", ""); a.code != 401 || !strings.Contains(a.body, `"login_required"`) || a.header.Get("WWW-Authenticate") != "" {
		t.Errorf("the HTTP API door, locked: %d %s", a.code, a.body)
	}
	if a := probe(t, "GET", api+"/v1/models", ""); a.code != 401 || !strings.Contains(a.body, `"code":"login_required"`) {
		t.Errorf("the /v1 door, locked: %d %s", a.code, a.body)
	}
	if a := probe(t, "POST", api+"/org-endpoint/ep_x", "{}"); a.code != 401 || !strings.Contains(a.body, "login_required") {
		t.Errorf("the org receiver door, locked: %d %s", a.code, a.body)
	}
	if a := probe(t, "POST", hook+"/webhook/nope", "{}"); a.code != 503 || a.header.Get("Retry-After") != "60" || !strings.Contains(a.body, "login_required") {
		t.Errorf("the webhook door, locked: %d %v %s", a.code, a.header, a.body)
	}
	if raw, reply := ask("doc.lookup"); reply.Code != "account_locked" || strings.Contains(raw, testEmail) {
		t.Errorf("the bridge door, locked: %s", raw)
	}
	if raw, reply := ask("ping"); !reply.OK || strings.Contains(raw, testEmail) {
		t.Errorf("ping answers a locked bridge, so that the side panel can say so: %s", raw)
	} else {
		reply.Data.Account.is(t, "locked", true, false)
	}
	if text, isErr := mcp.try("workflow_list", map[string]any{}); !isErr || text != loginLine {
		t.Errorf("the MCP door, locked: %v %q", isErr, text)
	}
	if res := mcp.call("tools/list", map[string]any{}); res["tools"] == nil {
		t.Error("tools/list still answers when locked")
	}

	// Signed in: no restart, each door sees the session within its poll interval.
	r.signIn()
	open := func(what string, ok func() bool) { r.waitFor(what+" to open", 60*time.Second, ok) }
	open("the HTTP API", func() bool { return probe(t, "GET", api+"/workflows", "").header.Get("WWW-Authenticate") != "" })
	if a := probe(t, "GET", api+"/workflows", ""); a.code != 401 || !strings.Contains(a.body, "missing or invalid bearer credential") {
		t.Errorf("the HTTP API door, signed in: %d %s", a.code, a.body)
	}
	if a := probe(t, "GET", api+"/v1/models", ""); a.code != 401 || !strings.Contains(a.body, "invalid_api_key") {
		t.Errorf("the /v1 door, signed in: %d %s", a.code, a.body)
	}
	if a := probe(t, "POST", api+"/org-endpoint/ep_x", "{}"); a.code != 404 || !strings.Contains(a.body, "unknown endpoint") {
		t.Errorf("the org receiver door, signed in: %d %s", a.code, a.body)
	}
	if a := probe(t, "POST", hook+"/webhook/nope", "{}"); a.code != 404 || !strings.Contains(a.body, "webhook not found") {
		t.Errorf("the webhook door, signed in: %d %s", a.code, a.body)
	}
	open("the bridge", func() bool { _, reply := ask("doc.lookup"); return reply.Code != "account_locked" })
	if raw, reply := ask("ping"); !reply.OK {
		t.Errorf("ping, signed in: %s", raw)
	} else {
		reply.Data.Account.is(t, "ok", true, true)
	}
	open("MCP", func() bool { _, isErr := mcp.try("workflow_list", map[string]any{}); return !isErr })
	open("the heartbeat", func() bool { return r.accountState() == "ok" })
	_, hb = r.heartbeatAccount()
	hb.is(t, "ok", true, true)
	r.health(api).is(t, "ok", true, true)
}
