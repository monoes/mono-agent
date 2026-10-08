//go:build devaccount && !windows

package accountsmoke

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// mcpConn is a stdio MCP server run by the rig: one JSON-RPC request per line, one answer per line.
type mcpConn struct {
	t   *testing.T
	cmd *exec.Cmd
	in  io.WriteCloser
	out *bufio.Reader
	id  int
}

func (r *rig) mcp(extraEnv []string, args ...string) *mcpConn {
	r.t.Helper()
	cmd := exec.Command(r.bin, args...)
	cmd.Env, cmd.Dir = r.env(extraEnv...), r.dir
	in, err := cmd.StdinPipe()
	must(r.t, err)
	out, err := cmd.StdoutPipe()
	must(r.t, err)
	must(r.t, cmd.Start())
	r.t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
	return &mcpConn{t: r.t, cmd: cmd, in: in, out: bufio.NewReader(out)}
}

// call sends one request and returns its result object; a JSON-RPC error fails the test.
func (c *mcpConn) call(method string, params any) map[string]any {
	c.t.Helper()
	c.id++
	req, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": c.id, "method": method, "params": params})
	_, err := c.in.Write(append(req, '\n'))
	must(c.t, err)
	watchdog := time.AfterFunc(60*time.Second, func() { _ = c.cmd.Process.Kill() })
	defer watchdog.Stop()
	line, err := c.out.ReadString('\n')
	if err != nil {
		c.t.Fatalf("%s: no answer: %v", method, err)
	}
	var resp struct {
		Result map[string]any
		Error  *struct{ Message string }
	}
	mustJSON(c.t, line, &resp)
	if resp.Error != nil {
		c.t.Fatalf("%s: %s", method, resp.Error.Message)
	}
	return resp.Result
}

// try calls a tool and returns its text and whether it answered an error.
func (c *mcpConn) try(name string, args map[string]any) (text string, isErr bool) {
	return c.tryWith(name, args, nil)
}

func (c *mcpConn) tryWith(name string, args, meta map[string]any) (text string, isErr bool) {
	c.t.Helper()
	params := map[string]any{"name": name, "arguments": args}
	if meta != nil {
		params["_meta"] = meta
	}
	res := c.call("tools/call", params)
	if content, _ := res["content"].([]any); len(content) > 0 {
		text, _ = content[0].(map[string]any)["text"].(string)
	}
	isErr, _ = res["isError"].(bool)
	return text, isErr
}

// tool calls a tool and returns its text; an error result fails the test.
func (c *mcpConn) tool(name string, args, meta map[string]any) string {
	c.t.Helper()
	text, isErr := c.tryWith(name, args, meta)
	if isErr {
		c.t.Fatalf("%s answered an error: %s", name, text)
	}
	return text
}

// bridgeReply is the settling frame of the extension bridge's request channel; ping's data carries
// the account (index section 3.4 item 7, and B3b's plan for its shape).
type bridgeReply struct {
	OK   bool
	Code string
	Data struct {
		Pong    bool
		Account *accountReport
	}
}

// bridge connects to the extension bridge the way the browser's extension does (the pairing
// token, then request frames) and returns a function that sends one request and returns the frame.
func (r *rig) bridge() func(method string) (string, bridgeReply) {
	r.t.Helper()
	addr := r.addr("bridge")
	r.waitFor("the bridge", 30*time.Second, func() bool { code, _ := get(r.t, "http://"+addr+"/monoagent/health", ""); return code == 200 })
	token, err := os.ReadFile(filepath.Join(r.home, ".monoagent", "extension.token"))
	must(r.t, err)
	ws, _, err := websocket.DefaultDialer.Dial("ws://"+addr+"/monoagent", http.Header{"Origin": {"chrome-extension://smoke"}})
	must(r.t, err)
	r.t.Cleanup(func() { ws.Close() })
	must(r.t, ws.WriteJSON(map[string]string{"type": "auth", "token": strings.TrimSpace(string(token))}))
	n := 0
	return func(method string) (string, bridgeReply) {
		n++
		id := fmt.Sprintf("r%d", n)
		must(r.t, ws.WriteJSON(map[string]any{"kind": "request", "id": id, "method": method, "params": map[string]any{"url": "https://example.com/"}}))
		_ = ws.SetReadDeadline(time.Now().Add(30 * time.Second))
		for {
			_, msg, err := ws.ReadMessage()
			must(r.t, err)
			var head struct{ Kind, ID string }
			if json.Unmarshal(msg, &head) == nil && head.Kind == "reply" && head.ID == id {
				var reply bridgeReply
				mustJSON(r.t, string(msg), &reply)
				return string(msg), reply
			}
		}
	}
}

func get(t testing.TB, url, bearer string) (int, string) {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, url, nil)
	must(t, err)
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, err.Error()
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

// answer is what one request got back.
type answer struct {
	code   int
	header http.Header
	body   string
}

// probe sends one request with no credential. Signed in, the door's own check answers it (no bearer,
// no key, no endpoint); locked, the account gate answers first. So the same request tells the two
// states apart, and nothing here needs a token or the vault.
func probe(t testing.TB, method, url, body string) answer {
	t.Helper()
	req, err := http.NewRequest(method, url, strings.NewReader(body))
	must(t, err)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return answer{body: err.Error()}
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return answer{resp.StatusCode, resp.Header, string(b)}
}

// health is the account object of GET /health, which is open in every state and never names the user.
func (r *rig) health(api string) *accountReport {
	r.t.Helper()
	a := probe(r.t, "GET", api+"/health", "")
	if a.code != 200 || strings.Contains(a.body, testEmail) {
		r.t.Fatalf("GET /health: %d %s", a.code, a.body)
	}
	var doc struct{ Account *accountReport }
	mustJSON(r.t, a.body, &doc)
	return doc.Account
}
