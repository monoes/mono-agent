//go:build !windows

package extension

import (
	"encoding/json"
	"net"
	"net/http"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// One relay client must never be able to stall the extension bridge.
//
// The read loop that serves the extension connection is the only thing
// draining that socket: captures, command replies and CDP events all arrive
// on it. If fanning an event out to a relay client can park that loop, a
// single monobrowse that is SIGSTOPped — or merely slow to drain during a
// trace — stops the whole extension, and loopback TCP will never time it
// out on its own.

// dialStuckCdp opens a relay socket with a tiny kernel receive buffer that
// nobody ever reads from: a client that has stopped consuming, which is
// what a stopped or wedged monobrowse looks like from this side.
func (r *cdpTestRig) dialStuckCdp(t *testing.T) *websocket.Conn {
	t.Helper()
	dialer := websocket.Dialer{
		NetDial: func(network, addr string) (net.Conn, error) {
			d := &net.Dialer{Control: func(_, _ string, c syscall.RawConn) error {
				var serr error
				if err := c.Control(func(fd uintptr) {
					serr = syscall.SetsockoptInt(int(fd), syscall.SOL_SOCKET, syscall.SO_RCVBUF, 2048)
				}); err != nil {
					return err
				}
				return serr
			}}
			return d.Dial(network, addr)
		},
	}
	h := http.Header{}
	h.Set(tokenHeader, r.token(t))
	conn, _, err := dialer.Dial("ws://127.0.0.1:"+strconv.Itoa(r.port)+"/monoagent/cdp", h)
	if err != nil {
		t.Fatalf("dial stuck cdp client: %v", err)
	}
	t.Cleanup(func() { conn.Close() })
	return conn
}

// drainCdp reads a client's socket forever, forwarding everything that is
// not a fanned-out event — i.e. the command replies a test asserts on.
func drainCdp(conn *websocket.Conn) <-chan []byte {
	replies := make(chan []byte, 16)
	go func() {
		defer close(replies)
		for {
			_, msg, err := conn.ReadMessage()
			if err != nil {
				return
			}
			var probe struct {
				Type string `json:"type"`
			}
			if json.Unmarshal(msg, &probe) == nil && probe.Type == CdpEventType {
				continue
			}
			select {
			case replies <- msg:
			default:
			}
		}
	}()
	return replies
}

func TestCdpRelay_StuckClientDoesNotStallTheBridge(t *testing.T) {
	rig := startCdpRig(t)
	_ = rig.dialStuckCdp(t) // never read from again
	healthy := rig.dialCdp(t, rig.token(t))
	replies := drainCdp(healthy)
	// Both sockets must be registered before the flood starts.
	time.Sleep(100 * time.Millisecond)

	event, err := json.Marshal(Response{
		Type: CdpEventType, Success: true,
		Data: map[string]any{
			"tabId":  42,
			"method": "Network.dataReceived",
			"params": map[string]any{"data": strings.Repeat("y", 64<<10)},
		},
	})
	if err != nil {
		t.Fatalf("marshal event: %v", err)
	}

	// The extension pushes events the way chrome.debugger does. Every write
	// must land: a write that times out here means the server has stopped
	// reading the extension connection altogether.
	for i := 0; i < 300; i++ {
		_ = rig.ext.SetWriteDeadline(time.Now().Add(2 * time.Second))
		if err := rig.ext.WriteMessage(websocket.TextMessage, event); err != nil {
			t.Fatalf("the server stopped reading the extension after %d events: %v", i, err)
		}
	}
	_ = rig.ext.SetWriteDeadline(time.Time{})

	// And the bridge still serves everyone else: a command round trip over
	// the extension connection, which only works if the read loop is alive.
	if err := healthy.WriteJSON(map[string]any{
		"id": "live-1", "type": CmdCdp, "tabId": 42,
		"params": map[string]any{"method": "Page.enable"},
	}); err != nil {
		t.Fatalf("write command: %v", err)
	}
	rig.answerExtension(t, map[string]any{"result": map[string]any{}}, "")

	select {
	case msg, ok := <-replies:
		if !ok {
			t.Fatal("the healthy client's socket closed instead of answering")
		}
		var reply struct {
			ID      string `json:"id"`
			Success bool   `json:"success"`
		}
		if err := json.Unmarshal(msg, &reply); err != nil {
			t.Fatalf("decode reply %q: %v", msg, err)
		}
		if reply.ID != "live-1" || !reply.Success {
			t.Fatalf("reply = %+v, want the live-1 round trip to succeed", reply)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the bridge never answered a command while one client was stuck")
	}
}

// A client whose backlog overflows is disconnected, not kept on with a hole
// in its event stream: an instrument that silently misses events reports
// confidently wrong results.
func TestCdpRelay_DropsAClientThatStopsDraining(t *testing.T) {
	rig := startCdpRig(t)
	stuck := rig.dialStuckCdp(t)
	time.Sleep(100 * time.Millisecond)

	event, _ := json.Marshal(Response{
		Type: CdpEventType, Success: true,
		Data: map[string]any{"tabId": 42, "method": "Network.dataReceived",
			"params": map[string]any{"data": strings.Repeat("y", 64<<10)}},
	})
	for i := 0; i < 300; i++ {
		_ = rig.ext.SetWriteDeadline(time.Now().Add(2 * time.Second))
		if err := rig.ext.WriteMessage(websocket.TextMessage, event); err != nil {
			t.Fatalf("extension write blocked after %d events: %v", i, err)
		}
	}
	_ = rig.ext.SetWriteDeadline(time.Time{})

	_ = stuck.SetReadDeadline(time.Now().Add(10 * time.Second))
	for {
		if _, _, err := stuck.ReadMessage(); err != nil {
			if strings.Contains(err.Error(), "i/o timeout") {
				t.Fatal("the wedged client was still attached after overflowing its backlog")
			}
			return // closed, which is the policy
		}
	}
}
