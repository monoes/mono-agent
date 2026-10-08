package extension

import (
	"bufio"
	"fmt"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/monoes/mono-agent/internal/account/accounttest"
)

// Account gate B3b, Task 9 (issue #368): no spelling of a request to the bridge's
// HTTP surface gets a command to the browser while the account is locked. Each
// request carries the right pairing token, so the account is all that stands in
// the way; a path or method the door misjudges but the mux routes would put a
// command on the extension's socket, which the fake extension would read.
func TestNoSpellingOfABridgeRequestReachesTheBrowserWhileLocked(t *testing.T) {
	srv, ext, _ := startCaptureServer(t)
	addr, tok := bridgeAddr(t, srv)
	accounttest.Install(t, accounttest.LockedNoLogin)

	body := `{"id":"c1","type":"create_tab"}`
	req := func(method, target, extra string) string {
		return fmt.Sprintf("%s %s HTTP/1.1\r\nHost: %s\r\n%s: %s\r\nContent-Length: %d\r\nConnection: close\r\n%s\r\n%s",
			method, target, addr, tokenHeader, tok, len(body), extra, body)
	}
	raw := []struct{ name, request string }{
		{"plain", req("POST", "/monoagent/relay?timeout_ms=200", "")},
		{"trailing slash", req("POST", "/monoagent/relay/?timeout_ms=200", "")},
		{"double slash", req("POST", "//monoagent/relay?timeout_ms=200", "")},
		{"dot segment", req("POST", "/monoagent/./relay?timeout_ms=200", "")},
		{"dot-dot", req("POST", "/monoagent/pair/../relay?timeout_ms=200", "")},
		{"encoded letter", req("POST", "/monoagent/rel%61y?timeout_ms=200", "")},
		{"encoded slash", req("POST", "/monoagent%2Frelay?timeout_ms=200", "")},
		{"upper case", req("POST", "/MONOAGENT/RELAY?timeout_ms=200", "")},
		{"absolute-URI", req("POST", "http://"+addr+"/monoagent/relay?timeout_ms=200", "")},
		{"HEAD", req("HEAD", "/monoagent/relay", "")},
		{"OPTIONS", req("OPTIONS", "/monoagent/relay", "")},
		{"PUT", req("PUT", "/monoagent/relay", "")},
		{"method override header", req("GET", "/monoagent/relay", "X-HTTP-Method-Override: POST\r\n")},
		{"h2c upgrade", req("POST", "/monoagent/relay?timeout_ms=200", "Connection: Upgrade, HTTP2-Settings\r\nUpgrade: h2c\r\nHTTP2-Settings: AAMAAABkAAQCAAAAAAIAAAAA\r\n")},
		{"websocket upgrade on the relay", req("GET", "/monoagent/relay", "Connection: Upgrade\r\nUpgrade: websocket\r\nSec-WebSocket-Version: 13\r\nSec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==\r\n")},
		{"cdp upgrade", req("GET", "/monoagent/cdp", "Connection: Upgrade\r\nUpgrade: websocket\r\nSec-WebSocket-Version: 13\r\nSec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==\r\n")},
		{"cdp trailing slash", req("GET", "/monoagent/cdp/", "Connection: Upgrade\r\nUpgrade: websocket\r\nSec-WebSocket-Version: 13\r\nSec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==\r\n")},
		{"HTTP/1.0", "POST /monoagent/relay?timeout_ms=200 HTTP/1.0\r\nHost: " + addr + "\r\n" + tokenHeader + ": " + tok + "\r\nContent-Length: " + fmt.Sprint(len(body)) + "\r\n\r\n" + body},
	}
	for _, a := range raw {
		conn, err := net.Dial("tcp", addr)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = conn.Write([]byte(a.request))
		_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
		resp, err := http.ReadResponse(bufio.NewReader(conn), nil)
		if err == nil {
			// 101 would be an open socket, 2xx an executed command.
			if resp.StatusCode < 300 || resp.StatusCode == http.StatusSwitchingProtocols {
				t.Errorf("%s: answered %d while locked", a.name, resp.StatusCode)
			}
			resp.Body.Close()
		}
		conn.Close()
	}

	// Nothing reached the extension: it reads no command, and none is pending.
	_ = ext.conn.SetReadDeadline(time.Now().Add(300 * time.Millisecond))
	if _, msg, err := ext.conn.ReadMessage(); err == nil {
		t.Fatalf("the extension was sent %s while the account was locked", msg)
	}
	if n := srv.inFlightCommands(); n != 0 {
		t.Fatalf("%d commands are in flight for a locked bridge", n)
	}
}
