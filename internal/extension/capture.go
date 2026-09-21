package extension

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	neturl "net/url"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"

	"github.com/monoes/mono-agent/internal/capture"
)

// The page_capture wire contract (CLIP-01), implemented on the JS side in
// chrome-extension/:
//
//	Go → extension
//	  {"id": …, "type": "page_capture", "tabId": 12,
//	   "params": {"formats": ["mhtml","pdf","readable","screenshot"],
//	              "selection": false, "note": "", "tags": [], "collection": ""}}
//	  tabId is omitted to mean "the active tab".
//
//	extension → Go, one chunk of an artifact too big for one frame (zero or
//	more, all before that capture's closing message)
//	  {"id": …, "success": true, "data": {
//	     "chunk": {"index": 0, "total": 4, "of": "page.mhtml"}, "bytes": …}}
//
//	extension → Go, the closing message (exactly one, always last)
//	  {"id": …, "success": true, "data": {"meta": {…}, "warnings": […],
//	   "final": true,
//	   "artifacts": [{"name": "readable.md", "encoding": "base64", "bytes": …},
//	                 {"name": "page.mhtml", "chunked": true, "chunks": 4}]}}
//
//	extension → Go, failure
//	  {"id": …, "success": false, "error": "…"}
//
// An artifact that was streamed carries no inline bytes in the closing
// message, only the count it was sent in — which is what lets a dropped
// frame fail the capture instead of truncating a file. The frame budget on
// the JS side is 8MiB of base64, well under the 32MiB read limit that would
// close the connection, so ordinary captures are a single message.
//
// A capture the extension queued while this process was down and flushed on
// reconnect uses the same messages with an id of the form `ext-<uuid>` that
// nothing here is waiting for, and carries "type": "page_capture" so it is
// recognizable even when it failed and has no data. See
// dispatch/isCaptureResponse.

// DefaultCaptureFormats is what `capture page` asks for when the caller
// names no formats.
var DefaultCaptureFormats = []string{"mhtml", "pdf", "readable", "screenshot"}

// DefaultCaptureTimeout is generous on purpose: a capture runs lazy-load
// prep, an MHTML snapshot, a print-to-PDF and a full-page screenshot on a
// page the user is actually looking at.
const DefaultCaptureTimeout = 120 * time.Second

// captureStreamBuffer bounds how many capture messages may be queued for a
// waiting CapturePage before the read loop starts dropping them. The
// receiver is a tight select loop, so this only has to absorb bursts.
const captureStreamBuffer = 128

// CaptureRequest is one page_capture command.
type CaptureRequest struct {
	// TabID is the tab to capture; zero means the active tab.
	TabID      int
	Formats    []string
	Selection  bool
	Note       string
	Tags       []string
	Collection string
	Timeout    time.Duration
	// Inbox overrides where the envelope is written. It is deliberately
	// not part of the params the extension sees — the browser has no
	// business knowing about this machine's filesystem — so it rides the
	// relay hop as a query parameter instead (see
	// RemoteSender.CapturePage).
	Inbox string
}

// Capturer is implemented by both bridges — the one that owns the extension
// connection and the one relaying through another process — so a caller
// does not care which it got.
type Capturer interface {
	CapturePage(req CaptureRequest) (*capture.Result, error)
}

func (r CaptureRequest) formats() []string {
	if len(r.Formats) == 0 {
		return DefaultCaptureFormats
	}
	return r.Formats
}

func (r CaptureRequest) timeout() time.Duration {
	if r.Timeout > 0 {
		return r.Timeout
	}
	return DefaultCaptureTimeout
}

// command renders the request as the Command the extension receives.
func (r CaptureRequest) command() *Command {
	tags := r.Tags
	if tags == nil {
		tags = []string{}
	}
	return &Command{
		ID:    uuid.New().String(),
		Type:  CmdPageCapture,
		TabID: r.TabID,
		Params: map[string]interface{}{
			"formats":    r.formats(),
			"selection":  r.Selection,
			"note":       r.Note,
			"tags":       tags,
			"collection": r.Collection,
		},
	}
}

// captureRequestFromCommand recovers a CaptureRequest from a Command that
// arrived over the HTTP relay, where it has been through JSON and lost its
// Go types.
func captureRequestFromCommand(cmd *Command, timeout time.Duration, inbox string) CaptureRequest {
	req := CaptureRequest{TabID: cmd.TabID, Timeout: timeout, Inbox: inbox}
	p := cmd.Params
	if p == nil {
		return req
	}
	req.Formats = stringSlice(p["formats"])
	req.Tags = stringSlice(p["tags"])
	req.Selection, _ = p["selection"].(bool)
	req.Note, _ = p["note"].(string)
	req.Collection, _ = p["collection"].(string)
	return req
}

func stringSlice(v any) []string {
	switch t := v.(type) {
	case []string:
		return t
	case []any:
		out := make([]string, 0, len(t))
		for _, item := range t {
			if s, ok := item.(string); ok {
				out = append(out, s)
			}
		}
		return out
	default:
		return nil
	}
}

// ---------------------------------------------------------------------------
// Server side
// ---------------------------------------------------------------------------

// captureParts lazily builds the per-server capture machinery and hands it
// back, so neither NewServer nor a Server built by hand in a test has to
// know about it. Everything here is reached from both the read loop and a
// waiting CapturePage, so it lives under pendMu like the rest of the
// dispatch state.
func (s *Server) captureParts() (*capture.Assembler, *capture.Writer) {
	s.pendMu.Lock()
	defer s.pendMu.Unlock()
	if s.assembler == nil {
		s.assembler = capture.NewAssembler(s.captureSpoolOptions())
	}
	if s.captureWriter == nil {
		s.captureWriter = &capture.Writer{Inbox: s.captureInbox}
	}
	return s.assembler, s.captureWriter
}

// SetCaptureInbox overrides where captures land (default:
// capture.DefaultInbox, ~/.monomind/inbox).
func (s *Server) SetCaptureInbox(dir string) {
	s.pendMu.Lock()
	defer s.pendMu.Unlock()
	s.captureInbox = dir
	s.captureWriter = &capture.Writer{Inbox: dir}
	// Drop the assembler so it is rebuilt spooling into the new inbox
	// rather than the old one. Safe because this is documented as a
	// before-the-first-capture call.
	s.assembler = nil
}

// SetCaptureOptions overrides the assembler's size, chunk-timeout and
// spool settings. Call before the first capture: an assembler mid-flight is
// replaced, not reconfigured.
func (s *Server) SetCaptureOptions(opts capture.Options) {
	s.pendMu.Lock()
	defer s.pendMu.Unlock()
	s.captureOptions = opts
	s.assembler = capture.NewAssembler(s.captureSpoolOptions())
}

// captureSpoolOptions defaults the chunk spool to the configured inbox, so
// streamed artifacts stage on the same filesystem they are about to land
// on — and, on a machine whose /tmp is a shared tmpfs, nowhere near it.
// Must be called with pendMu held.
func (s *Server) captureSpoolOptions() capture.Options {
	opts := s.captureOptions
	if opts.SpoolDir == "" {
		opts.SpoolDir = s.captureInbox // empty in turn means capture.DefaultInbox
	}
	return opts
}

// OnCapture registers a callback invoked for every capture that arrives
// without a command asking for it (CLIP-08's flushed queue), with the
// written result or the error that stopped it. Used for logging, and by
// tests to observe the unsolicited path.
func (s *Server) OnCapture(fn func(*capture.Result, error)) {
	s.pendMu.Lock()
	s.onCapture = fn
	s.pendMu.Unlock()
}

// CapturePage asks the extension to capture a tab and writes the resulting
// envelope into the inbox, returning where it landed.
func (s *Server) CapturePage(req CaptureRequest) (*capture.Result, error) {
	assembler, writer := s.captureParts()
	if req.Inbox != "" {
		writer = &capture.Writer{Inbox: req.Inbox}
	}
	cmd := req.command()

	ch := make(chan *Response, captureStreamBuffer)
	s.pendMu.Lock()
	if s.streams == nil {
		s.streams = make(map[string]chan *Response)
	}
	s.streams[cmd.ID] = ch
	s.pendMu.Unlock()
	defer func() {
		s.pendMu.Lock()
		delete(s.streams, cmd.ID)
		s.pendMu.Unlock()
		assembler.Drop(cmd.ID)
	}()

	if err := s.writeCommand(cmd); err != nil {
		return nil, err
	}
	s.logger.Debug().Str("id", cmd.ID).Str("type", cmd.Type).Msg("command sent")

	deadline := time.NewTimer(req.timeout())
	defer deadline.Stop()
	for {
		select {
		case resp := <-ch:
			if !resp.Success {
				return nil, fmt.Errorf("extension error: %s", resp.Error)
			}
			env, err := assembler.Accept(cmd.ID, resp.dataMap())
			if err != nil {
				return nil, fmt.Errorf("page_capture: %w", err)
			}
			if env == nil {
				continue // more chunks to come
			}
			return writer.Write(env)
		case <-deadline.C:
			return nil, fmt.Errorf("page_capture timed out after %s", req.timeout())
		}
	}
}

// writeCommand marshals and writes one command to the extension socket.
func (s *Server) writeCommand(cmd *Command) error {
	data, err := json.Marshal(cmd)
	if err != nil {
		return fmt.Errorf("marshal command: %w", err)
	}

	s.connMu.Lock()
	conn := s.conn
	s.connMu.Unlock()
	if conn == nil {
		return fmt.Errorf("no extension connected")
	}

	s.writeMu.Lock()
	err = conn.WriteMessage(websocket.TextMessage, data)
	s.writeMu.Unlock()
	if err != nil {
		return fmt.Errorf("write command: %w", err)
	}
	return nil
}

// dispatch routes one decoded response from the extension. A capture is
// streamed (many messages share one id), an ordinary command gets its
// single reply, and a capture nobody asked for is written to the inbox
// anyway — that is the extension flushing the queue it filled while this
// process was down (CLIP-08). Every send here is non-blocking: the read
// loop must never be parked by a slow or vanished receiver, because that
// would stall the whole extension connection.
func (s *Server) dispatch(resp *Response) {
	s.pendMu.Lock()
	stream, streaming := s.streams[resp.ID]
	pending, waiting := s.pending[resp.ID]
	s.pendMu.Unlock()

	switch {
	case streaming:
		select {
		case stream <- resp:
		default:
			s.logger.Error().Str("id", resp.ID).Msg("capture stream buffer full, dropped a message")
		}
	case waiting:
		select {
		case pending <- resp:
		default:
			s.logger.Warn().Str("id", resp.ID).Msg("duplicate response for an already-answered command")
		}
	case isCaptureResponse(resp):
		s.acceptUnsolicitedCapture(resp)
	default:
		s.logger.Warn().Str("id", resp.ID).Msg("no pending request for response")
	}
}

// isCaptureResponse reports whether a response that matches no pending
// command is a page_capture message. The type field settles it outright;
// the shape check is the fallback for a sender that omits it.
func isCaptureResponse(resp *Response) bool {
	if resp.Type == CmdPageCapture {
		return true
	}
	data := resp.dataMap()
	if data == nil {
		return false
	}
	if _, chunked := data["chunk"]; chunked {
		return true
	}
	_, hasMeta := data["meta"]
	_, hasArtifacts := data["artifacts"]
	return hasMeta && hasArtifacts
}

// acceptUnsolicitedCapture feeds a flushed-queue capture through the same
// assembler and writer as a requested one. Called from the read loop, so
// the disk write is handed to a goroutine — a 60MB envelope must not hold
// up the extension connection.
func (s *Server) acceptUnsolicitedCapture(resp *Response) {
	assembler, writer := s.captureParts()
	if !resp.Success {
		s.logger.Warn().Str("id", resp.ID).Str("error", resp.Error).Msg("queued capture failed in the extension")
		s.reportCapture(nil, fmt.Errorf("extension error: %s", resp.Error))
		return
	}
	env, err := assembler.Accept(resp.ID, resp.dataMap())
	if err != nil {
		s.logger.Error().Err(err).Str("id", resp.ID).Msg("queued capture could not be assembled")
		s.reportCapture(nil, err)
		return
	}
	if env == nil {
		return // more chunks to come
	}
	go func() {
		res, err := writer.Write(env)
		if err != nil {
			s.logger.Error().Err(err).Str("id", resp.ID).Msg("queued capture could not be written")
		} else {
			s.logger.Info().Str("path", res.Path).Msg("wrote a capture the extension had queued")
		}
		s.reportCapture(res, err)
	}()
}

// reportCapture invokes the OnCapture callback, if one is registered.
func (s *Server) reportCapture(res *capture.Result, err error) {
	s.pendMu.Lock()
	fn := s.onCapture
	s.pendMu.Unlock()
	if fn != nil {
		fn(res, err)
	}
}

// sweepCaptures drops captures whose remaining chunks never arrived. Driven
// by the ping loop: a requested capture has its own deadline, but a flushed
// one has nobody waiting on it, so without this its buffered chunks would
// be held until the process exits.
func (s *Server) sweepCaptures() {
	s.pendMu.Lock()
	assembler := s.assembler
	s.pendMu.Unlock()
	if assembler == nil {
		return // nothing has captured yet
	}
	for _, id := range assembler.Expire(time.Now()) {
		s.logger.Warn().Str("id", id).Msg("dropped a capture whose chunks stopped arriving")
		s.reportCapture(nil, capture.ErrExpired)
	}
}

// serveRelayCapture answers a relayed page_capture (see handleRelay). The
// process that owns the extension connection is the one that assembles and
// writes the envelope; the caller gets back only the small Result, so a
// 60MB archive never crosses the loopback HTTP hop.
func (s *Server) serveRelayCapture(w http.ResponseWriter, cmd *Command, timeout time.Duration, inbox string) {
	res, err := s.CapturePage(captureRequestFromCommand(cmd, timeout, inbox))
	resp := &Response{ID: cmd.ID, Type: CmdPageCapture}
	if err != nil {
		resp.Error = err.Error()
	} else {
		resp.Success = true
		resp.Data = res
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

// ---------------------------------------------------------------------------
// Relay side
// ---------------------------------------------------------------------------

// CapturePage relays a capture through the process that owns the extension
// connection. That process writes the envelope; this one only learns where
// it landed.
func (r *RemoteSender) CapturePage(req CaptureRequest) (*capture.Result, error) {
	cmd := req.command()
	body, err := json.Marshal(cmd)
	if err != nil {
		return nil, fmt.Errorf("marshal command: %w", err)
	}
	timeout := req.timeout()
	url := fmt.Sprintf("%s/monoagent/relay?timeout_ms=%d", r.baseURL, timeout.Milliseconds())
	if req.Inbox != "" {
		url += "&inbox=" + neturl.QueryEscape(req.Inbox)
	}
	httpReq, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set(tokenHeader, r.token)

	// A capture can legitimately outlast the shared client's fixed
	// timeout, so this one request gets a client sized to the capture.
	client := &http.Client{Timeout: timeout + 30*time.Second}
	httpResp, err := client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("relay request: %w", err)
	}
	defer httpResp.Body.Close()

	var resp Response
	if err := json.NewDecoder(httpResp.Body).Decode(&resp); err != nil {
		return nil, fmt.Errorf("decode relay response: %w", err)
	}
	if !resp.Success {
		if resp.Error == "" {
			resp.Error = httpResp.Status
		}
		return nil, fmt.Errorf("page_capture: %s", resp.Error)
	}
	blob, err := json.Marshal(resp.Data)
	if err != nil {
		return nil, fmt.Errorf("re-encode capture result: %w", err)
	}
	var res capture.Result
	if err := json.Unmarshal(blob, &res); err != nil {
		return nil, fmt.Errorf("decode capture result: %w", err)
	}
	if res.Path == "" {
		return nil, fmt.Errorf("relayed capture returned no envelope path")
	}
	return &res, nil
}
