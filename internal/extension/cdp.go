package extension

import (
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// The CDP relay: raw Chrome DevTools Protocol over the extension bridge.
//
// The extension already holds Chrome's `debugger` permission and already
// speaks CDP to the user's real, logged-in tabs (see the eval_cdp/type_cdp
// commands in chrome-extension/background.js, and cdp_proxy.js which
// generalises them). What was missing was a way for something *outside* this
// process to drive that — specifically monobrowse, whose every instrument
// (console, network, HAR, vitals, trace, CPU profiler, snapshot, screenshot,
// emulation) is written against a CDP client and none of which should have
// to be ported per backend.
//
// So: one WebSocket endpoint that carries CDP envelopes to the extension and
// fans debugger events back. It deliberately knows nothing about CDP itself —
// framing, ids, sessions and timeouts all live in monobrowse's bridge
// transport (packages/@monoes/monobrowse/src/browser/bridge.ts). This side is
// a relay, and a relay that parsed CDP would be a second implementation to
// keep in sync.
//
// Two things it does enforce, because nothing downstream can:
//
//   - the same token as /monoagent/relay. This socket reaches a browser the
//     user is signed into; it is not a public port.
//   - only the three cdp_* command types. The general relay already exists
//     for everything else, with its own audit trail; this endpoint must not
//     quietly become a second, unaudited path to get_cookies and eval.
const (
	// CmdCdp carries one CDP command through chrome.debugger.sendCommand.
	CmdCdp = "cdp"
	// CmdCdpAttach attaches the debugger to a tab and starts relaying its
	// events. With no tabId the extension resolves the active tab and
	// reports back which one it took.
	CmdCdpAttach = "cdp_attach"
	// CmdCdpDetach stops the relay and detaches, which is what takes
	// Chrome's "started debugging this browser" banner off the tab.
	CmdCdpDetach = "cdp_detach"

	// CdpEventType marks an unsolicited frame carrying a debugger event.
	// Like a flushed capture (see isCaptureResponse) it matches no pending
	// command, so the read loop needs the marker to tell it apart from a
	// stray response.
	CdpEventType = "cdp_event"
)

// cdpCommandTimeout bounds one relayed CDP command. Generous next to a local
// socket's round trip because the path is longer — HTTP/WS to this process,
// WS to an MV3 service worker that may have to be woken, then
// chrome.debugger — and because a screenshot or a large DOM snapshot is a
// multi-megabyte frame at the far end.
const cdpCommandTimeout = 60 * time.Second

// maxInflightCdpCommands bounds the goroutines one relay socket can have
// waiting on the extension at once. Commands are relayed concurrently
// (monobrowse pipelines several domain enables at a time), so this is what
// keeps a chatty or hostile client from fanning out without limit.
const maxInflightCdpCommands = 64

// cdpClient is one connected consumer of the relay — in practice a
// monobrowse CdpClient behind its bridge transport.
type cdpClient struct {
	conn    *websocket.Conn
	writeMu sync.Mutex
	// tabs this client attached, so they can be detached if it vanishes.
	tabsMu sync.Mutex
	tabs   map[int]struct{}
}

func (c *cdpClient) write(v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	return c.conn.WriteMessage(websocket.TextMessage, data)
}

func (c *cdpClient) rememberTab(tabID int) {
	if tabID == 0 {
		return
	}
	c.tabsMu.Lock()
	defer c.tabsMu.Unlock()
	c.tabs[tabID] = struct{}{}
}

func (c *cdpClient) forgetTab(tabID int) {
	c.tabsMu.Lock()
	defer c.tabsMu.Unlock()
	delete(c.tabs, tabID)
}

func (c *cdpClient) attachedTabs() []int {
	c.tabsMu.Lock()
	defer c.tabsMu.Unlock()
	out := make([]int, 0, len(c.tabs))
	for id := range c.tabs {
		out = append(out, id)
	}
	return out
}

func (s *Server) addCdpClient(c *cdpClient) {
	s.cdpMu.Lock()
	defer s.cdpMu.Unlock()
	if s.cdpClients == nil {
		s.cdpClients = make(map[*cdpClient]struct{})
	}
	s.cdpClients[c] = struct{}{}
}

func (s *Server) removeCdpClient(c *cdpClient) {
	s.cdpMu.Lock()
	defer s.cdpMu.Unlock()
	delete(s.cdpClients, c)
}

// isCdpEvent reports whether a response that matches no pending command is a
// relayed debugger event.
func isCdpEvent(resp *Response) bool {
	return resp.Type == CdpEventType
}

// fanoutCdpEvent pushes one debugger event to every listening relay client.
// Events are the whole reason this endpoint is a socket rather than another
// HTTP relay call: console messages, network responses and trace chunks all
// arrive unasked-for, and an instrument that only saw command replies would
// see nothing at all.
func (s *Server) fanoutCdpEvent(resp *Response) {
	s.cdpMu.Lock()
	clients := make([]*cdpClient, 0, len(s.cdpClients))
	for c := range s.cdpClients {
		clients = append(clients, c)
	}
	s.cdpMu.Unlock()

	if len(clients) == 0 {
		return
	}
	frame := map[string]any{"type": CdpEventType, "data": resp.Data}
	for _, c := range clients {
		if err := c.write(frame); err != nil {
			// A client that cannot be written to is going away; its read
			// loop will clean it up.
			s.logger.Debug().Err(err).Msg("cdp event write failed")
		}
	}
}

// handleCdpSocket serves /monoagent/cdp.
func (s *Server) handleCdpSocket(w http.ResponseWriter, r *http.Request) {
	// Compared before the upgrade so a rejected client gets a plain 401 it
	// can read, rather than a WebSocket that closes for no stated reason.
	if s.token == "" ||
		subtle.ConstantTimeCompare([]byte(r.Header.Get(tokenHeader)), []byte(s.token)) != 1 {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		s.logger.Error().Err(err).Msg("cdp relay upgrade failed")
		return
	}
	conn.SetReadLimit(maxMessageSize)

	client := &cdpClient{conn: conn, tabs: make(map[int]struct{})}
	s.addCdpClient(client)
	s.logger.Debug().Msg("cdp relay client connected")

	defer func() {
		s.removeCdpClient(client)
		_ = conn.Close()
		// Detach whatever this client left attached. Chrome shows the user
		// a banner for every attached debuggee; a CLI that crashed mid-run
		// must not leave one on their tab until the extension's idle sweep
		// notices 30 seconds later.
		for _, tabID := range client.attachedTabs() {
			go func(id int) {
				_, err := s.SendCommand(&Command{Type: CmdCdpDetach, TabID: id}, cdpCommandTimeout)
				if err != nil {
					s.logger.Debug().Err(err).Int("tabId", id).Msg("cdp detach on disconnect failed")
				}
			}(tabID)
		}
		s.logger.Debug().Msg("cdp relay client disconnected")
	}()

	inflight := make(chan struct{}, maxInflightCdpCommands)
	for {
		_, msg, err := conn.ReadMessage()
		if err != nil {
			return
		}
		var cmd Command
		if err := json.Unmarshal(msg, &cmd); err != nil {
			// Never log the payload: a CDP command can carry page content.
			s.logger.Debug().Int("len", len(msg)).Msg("invalid cdp envelope")
			continue
		}
		if !isCdpCommand(cmd.Type) {
			_ = client.write(&Response{
				ID:    cmd.ID,
				Error: fmt.Sprintf("the CDP relay carries %s/%s/%s only, not %q", CmdCdp, CmdCdpAttach, CmdCdpDetach, cmd.Type),
			})
			continue
		}

		select {
		case inflight <- struct{}{}:
		default:
			_ = client.write(&Response{ID: cmd.ID, Error: "cdp relay busy: too many commands in flight"})
			continue
		}
		go func(cmd Command) {
			defer func() { <-inflight }()
			s.relayCdpCommand(client, &cmd)
		}(cmd)
	}
}

func isCdpCommand(t string) bool {
	return t == CmdCdp || t == CmdCdpAttach || t == CmdCdpDetach
}

// relayCdpCommand sends one envelope to the extension and writes the answer
// back to the client that asked, keeping the client's id so the bridge
// transport can match it.
func (s *Server) relayCdpCommand(client *cdpClient, cmd *Command) {
	clientID := cmd.ID
	// The extension's own ids must be unique across this process, and a
	// client's are only unique to itself. SendCommand mints one when empty.
	cmd.ID = ""

	resp, err := s.SendCommand(cmd, cdpCommandTimeout)
	if resp == nil {
		resp = &Response{}
	}
	if err != nil && resp.Error == "" {
		resp.Error = err.Error()
	}
	resp.ID = clientID
	resp.Type = cmd.Type

	if cmd.Type == CmdCdpAttach && resp.Success {
		// The extension reports which tab it actually attached to, which is
		// the only way the client learns the id when it asked for "whatever
		// is active" — and the only record this side has for detaching it.
		client.rememberTab(attachedTabID(cmd, resp))
	}
	if cmd.Type == CmdCdpDetach {
		client.forgetTab(cmd.TabID)
	}

	if err := client.write(resp); err != nil {
		s.logger.Debug().Err(err).Msg("cdp reply write failed")
	}
}

// attachedTabID prefers the tab the extension reports over the one asked
// for, since an attach with no tabId resolves to the active tab.
func attachedTabID(cmd *Command, resp *Response) int {
	if data, ok := resp.Data.(map[string]interface{}); ok {
		if raw, ok := data["tabId"].(float64); ok && raw != 0 {
			return int(raw)
		}
	}
	return cmd.TabID
}
