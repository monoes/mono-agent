//go:build devaccount && !windows

package accountsmoke

import (
	"strings"
	"testing"
	"time"
)

// The signed-in path through every process type and entry point (spec §11). Each subtest runs one
// operation of one entry point and expects it to succeed: in a test binary Require fails open
// without a guard, so a path that forgot to install one passes the unit tests and would fail closed
// for signed-in users in production. Logging out closes the machine again.
func TestEveryEntryPointWorksSignedIn(t *testing.T) {
	r := newRig(t, rigOptions{enforce: past})
	r.signIn()
	fast := r.workflow(waitWorkflow("smoke-fast", 1), true)

	t.Run("workflow run", func(t *testing.T) {
		res := r.sub(t).run("--json", "workflow", "run", fast, "--timeout", "60s")
		mustExit(t, res, 0)
		var rec struct{ Status string }
		mustJSON(t, res.stdout, &rec)
		if rec.Status != "SUCCESS" {
			t.Fatalf("the run ended %s", rec.Status)
		}
	})

	d := r.startDaemon()
	t.Run("daemon", func(t *testing.T) {
		r := r.sub(t)
		r.waitFor("the heartbeat to report an ok account", 30*time.Second, func() bool { return r.accountState() == "ok" })
		pid, report := r.heartbeatAccount()
		report.is(t, "ok", true, true)
		id := r.enqueue(fast) // adopted by the daemon, which runs it through handleExecution
		r.waitFor("the daemon to run the queued execution", 60*time.Second, func() bool {
			e := r.execution(id)
			return e.Status == "SUCCESS" && e.PID == pid
		})
	})

	t.Run("httpapi", func(t *testing.T) {
		r := r.sub(t)
		p := r.start("httpapi", "--addr", r.addr("httpapi"))
		base := "http://" + r.addr("httpapi")
		r.waitFor("the HTTP API", 30*time.Second, func() bool { code, _ := get(t, base+"/health", ""); return code == 200 })
		var health struct{ Account *accountReport }
		_, body := get(t, base+"/health", "") // open, with no credential, and it says what the account is
		mustJSON(t, body, &health)
		health.Account.is(t, "ok", true, true)
		get(t, base+"/workflows", "") // the first request creates the bearer token in the vault
		token := strings.TrimSpace(r.run("secret", "reveal", "httpapi-token", "--reveal").stdout)
		if code, body := get(t, base+"/workflows", token); code != 200 || !strings.Contains(body, fast) {
			t.Fatalf("GET /workflows with the bearer: %d", code)
		}
		p.stop()
	})

	t.Run("mcp", func(t *testing.T) {
		c := r.sub(t).mcp(nil, "mcp")
		c.call("initialize", map[string]any{})
		if text := c.tool("workflow_list", map[string]any{}, nil); !strings.Contains(text, fast) {
			t.Fatalf("workflow_list: %s", text)
		}
	})

	t.Run("mcp --grant child", func(t *testing.T) {
		r := r.sub(t)
		wf := r.workflow(`{"name":"Publish post","version":1,"is_active":false,
 "nodes":[{"id":"t","type":"trigger.manual","name":"Start","config":{}},
  {"id":"s","type":"core.set","name":"Publish","config":{"assignments":"[{\"field\":\"result\",\"value\":\"published {{ $json.input.text }}\"}]","include_input":false}}],
 "connections":[{"id":"c","source":"t","source_handle":"main","target":"s","target_handle":"main"}]}`, false)
		mustExit(t, r.run("org", "create-json", "growth", "--json", `{"name":"growth","goal":"Publish posts.","status":"stopped","schedule":null,"roles":[{"id":"lead","title":"Lead","type":"boss","reports_to":null,"responsibilities":["Decide."]},{"id":"writer","title":"Writer","type":"specialist","reports_to":"lead","responsibilities":["Write posts."]}]}`), 0)
		mustExit(t, r.run("org", "automation", "add", "growth", "--workflow", wf, "--alias", "publish_post"), 0)
		res := r.run("org", "grant", "add", "growth", "--role", "writer", "--automation", "publish_post", "--approval", "none")
		mustExit(t, res, 0)
		var grant struct{ Grant struct{ ID string } }
		mustJSON(t, res.stdout, &grant)

		// monomind spawns this for an org role: its own process, running the work in the daemon.
		c := r.mcp([]string{"MONOMIND_ORG_NAME=growth", "MONOMIND_ORG_ROLE=writer", "MONOMIND_ORG_RUN=run-smoke"}, "mcp", "--grant", grant.Grant.ID, "--profile", "default")
		c.call("initialize", map[string]any{})
		meta := map[string]any{"trace": map[string]any{"org": "growth", "run": "run-smoke", "role": "writer", "chain_id": "chn_smoketest", "hop": 1}}
		if text := c.tool("automation_publish_post", map[string]any{"text": "hello"}, meta); !strings.Contains(text, "published hello") {
			t.Fatalf("the grant call did not run the workflow in the daemon: %s", text)
		}
	})

	t.Run("extension serve", func(t *testing.T) {
		r := r.sub(t)
		p := r.start("extension", "serve")
		ask := r.bridge()
		raw, reply := ask("ping")
		if !reply.OK || !reply.Data.Pong || strings.Contains(raw, testEmail) {
			t.Fatalf("ping should answer, and never name the user: %s", raw)
		}
		reply.Data.Account.is(t, "ok", true, true)
		if raw, reply := ask("doc.lookup"); reply.Code == "account_locked" {
			t.Fatalf("a signed-in bridge refused a request: %s", raw)
		}
		p.stop()
	})

	t.Run("org serve", func(t *testing.T) {
		r := r.sub(t)
		p := r.start("org", "serve", "--foreground")
		r.waitFor("monomind org serve to be started", 30*time.Second, func() bool { return strings.Contains(r.read("monomind.log"), "org serve") })
		p.stop()
		if p.err != nil {
			t.Fatalf("org serve did not end cleanly: %v\n%s", p.err, r.read("org.log"))
		}
	})

	t.Run("chat", func(t *testing.T) {
		r := r.sub(t)
		res := r.run("--json", "chat", "history", "create", "--runtime", "claude")
		mustExit(t, res, 0)
		var conv struct{ ID string }
		mustJSON(t, res.stdout, &conv)
		res = r.run("--json", "chat", "--conversation", conv.ID, "--turn", "t1", "--", "hello")
		mustExit(t, res, 0)
		lines := strings.Split(strings.TrimSpace(res.stdout), "\n")
		var last struct {
			Type    string
			Payload struct{ Status string }
		}
		mustJSON(t, lines[len(lines)-1], &last)
		if last.Type != "turn.finished" || last.Payload.Status != "completed" || !strings.Contains(r.read("monomind.log"), "agent exec") {
			t.Fatalf("the turn did not reach monomind.Exec and complete: %s", lines[len(lines)-1])
		}
	})

	if !d.alive() {
		t.Fatalf("the daemon died during the smoke:\n%s", r.read("daemon.log"))
	}
	mustExit(t, r.run("account", "logout"), 0)
	r.assertLocked(r.run("--json", "workflow", "list"), "not_logged_in", true)
}
