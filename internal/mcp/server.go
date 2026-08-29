// Package mcp implements a stdio MCP (Model Context Protocol) server for
// AI agents: newline-delimited JSON-RPC 2.0 over os.Stdin/os.Stdout, with
// all logging on stderr so stdout stays a clean protocol channel.
package mcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"
)

const protocolVersion = "2024-11-05"

// maxRequestLineBytes caps a single request line. A longer line is
// discarded and answered with a -32700 "request too large" error, but the
// server keeps serving subsequent requests (the connection survives).
const maxRequestLineBytes = 17 * 1024 * 1024

// postEOFGrace bounds how long in-flight handlers may keep running after
// stdin reaches EOF before the serve context is cancelled: fast tools
// finish and get their responses flushed; long waits (a workflow_run
// poll) are cut off by the cancel instead of holding the server open.
const postEOFGrace = 3 * time.Second

// Options configures the server.
type Options struct {
	// DBPath is the SQLite database path ("~/..." expanded).
	// Empty defaults to ~/.monoagent/monoagent.db.
	DBPath string
	// Profile is the --profile flag value. When empty, MONOAGENT_PROFILE is
	// consulted, then the active_profile_id setting, then "default".
	Profile string
	// WorkflowsDir overrides the JSON workflow file store directory
	// (default ~/.monoagent/workflows; mainly for tests).
	WorkflowsDir string
	// Version is reported in initialize serverInfo.
	Version string
}

type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
	ID      json.RawMessage `json:"id,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  interface{}     `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type toolContent struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type toolResult struct {
	Content []toolContent `json:"content"`
	IsError bool          `json:"isError"`
}

// Server dispatches MCP JSON-RPC requests to tool handlers.
type Server struct {
	opts Options
	rt   *runtime

	// rtMu guards the lazy runtime bootstrap: requests are dispatched on
	// separate goroutines, so concurrent tools/call invocations may race
	// to build the runtime on first use.
	rtMu sync.Mutex
}

// NewServer creates a Server with the given options.
func NewServer(opts Options) *Server {
	return &Server{opts: opts}
}

// Run serves MCP over stdin/stdout until stdin closes. It is the entry
// point used by the `monoagentcli mcp` command.
func Run(opts Options) error {
	return NewServer(opts).Serve(context.Background(), os.Stdin, os.Stdout)
}

// Serve reads newline-delimited JSON-RPC requests from in and writes one
// JSON response line per request to out. Notifications (requests with no
// raw "id" member) produce no output. Each request is dispatched on its
// own goroutine so a slow tool call cannot block other requests
// (head-of-line removal); responses are serialized through wmu, but their
// ORDER is no longer guaranteed under concurrency (acceptable per spec).
// When the read loop ends (EOF), the serve context is cancelled so any
// context-aware wait (e.g. a pending workflow_run polling loop) observes
// the closed client instead of blocking out its full timeout. It returns
// nil when in reaches EOF.
func (s *Server) Serve(ctx context.Context, in io.Reader, out io.Writer) error {
	defer s.closeRuntime()

	// serveCtx is cancelled as soon as the read loop ends (client closed
	// stdin) — handlers below receive it, so waitForExecution-style waits
	// return promptly instead of blocking for the full request timeout.
	serveCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	reader := bufio.NewReaderSize(in, 64*1024)
	w := bufio.NewWriter(out)
	// wmu serializes response writes (and the shared bufio.Writer) across
	// handler goroutines; writeErr remembers the first failure so Serve
	// can surface it after all in-flight handlers settle.
	var (
		wmu      sync.Mutex
		writeErr error
	)
	var wg sync.WaitGroup
	dispatch := func(line []byte) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			resp := s.handleLine(serveCtx, line)
			if resp == nil {
				return
			}
			wmu.Lock()
			defer wmu.Unlock()
			if werr := writeResponse(w, resp); werr != nil && writeErr == nil {
				writeErr = werr
			}
		}()
	}
	// settle stops reading and releases the handlers: in-flight work gets
	// postEOFGrace to complete on its own, then serveCtx is cancelled so
	// context-aware waits return instead of blocking out their timeouts.
	settle := func() {
		graceDone := make(chan struct{})
		go func() {
			wg.Wait()
			close(graceDone)
		}()
		select {
		case <-graceDone:
		case <-time.After(postEOFGrace):
		}
		cancel()
		<-graceDone
	}

	for {
		if err := serveCtx.Err(); err != nil {
			settle()
			return err
		}
		line, tooLong, rerr := readLine(reader, maxRequestLineBytes)
		if tooLong {
			wmu.Lock()
			werr := writeResponse(w, &rpcResponse{
				JSONRPC: "2.0",
				ID:      json.RawMessage("null"),
				Error:   &rpcError{Code: -32700, Message: "request too large"},
			})
			wmu.Unlock()
			if werr != nil {
				settle()
				return werr
			}
		} else if trimmed := bytes.TrimSpace(line); len(trimmed) > 0 {
			dispatch(trimmed)
		}
		if rerr != nil {
			if errors.Is(rerr, io.EOF) {
				break
			}
			settle()
			return fmt.Errorf("mcp: read stdin: %w", rerr)
		}
	}
	// stdin is gone: let in-flight handlers settle (bounded), then flush.
	settle()
	if writeErr != nil {
		return writeErr
	}
	return w.Flush()
}

// writeResponse marshals and writes one response line, flushing the writer
// so responses are not reordered or delayed by buffering.
func writeResponse(w *bufio.Writer, resp *rpcResponse) error {
	b, err := json.Marshal(resp)
	if err != nil {
		fmt.Fprintf(os.Stderr, "mcp: marshal response: %v\n", err)
		return nil
	}
	if _, err := w.Write(append(b, '\n')); err != nil {
		return err
	}
	return w.Flush()
}

// readLine reads one '\n'-terminated line, accumulating at most max bytes
// of line CONTENT (the '\n' delimiter itself is neither returned nor
// counted toward the limit). When the content exceeds max the remainder is
// still consumed (up to the delimiter) but discarded: it reports
// tooLong=true so the caller can answer with an error and continue
// serving. err is nil when the delimiter was found; io.EOF when input
// ended (possibly with a final unterminated line); any other error is a
// genuine read failure.
func readLine(r *bufio.Reader, max int) (line []byte, tooLong bool, err error) {
	for {
		chunk, cerr := r.ReadSlice('\n')
		if cerr == nil {
			chunk = chunk[:len(chunk)-1]
		}
		if !tooLong {
			if len(line)+len(chunk) > max {
				tooLong, line = true, nil
			} else {
				line = append(line, chunk...)
			}
		}
		if cerr != bufio.ErrBufferFull {
			return line, tooLong, cerr
		}
	}
}

// hasRequestID reports whether the request carries a usable (present,
// non-null) raw "id" member. Per JSON-RPC 2.0 a request without one is a
// notification: the server must not respond.
func hasRequestID(req rpcRequest) bool {
	id := bytes.TrimSpace(req.ID)
	return len(id) > 0 && !bytes.Equal(id, []byte("null"))
}

func (s *Server) handleLine(ctx context.Context, line []byte) *rpcResponse {
	var req rpcRequest
	if err := json.Unmarshal(line, &req); err != nil {
		return &rpcResponse{
			JSONRPC: "2.0",
			ID:      json.RawMessage("null"),
			Error:   &rpcError{Code: -32700, Message: "parse error: " + err.Error()},
		}
	}
	// The jsonrpc member is mandatory and must be exactly "2.0" — a
	// missing or wrong version is an Invalid Request, not a parse error.
	if req.JSONRPC != "2.0" {
		id := json.RawMessage("null")
		if hasRequestID(req) {
			id = req.ID
		}
		return &rpcResponse{
			JSONRPC: "2.0",
			ID:      id,
			Error:   &rpcError{Code: -32600, Message: `invalid request: jsonrpc member must be "2.0"`},
		}
	}
	isNotificationMethod := strings.HasPrefix(req.Method, "notifications/")

	// A notifications/* method with a non-null id is a protocol violation.
	if isNotificationMethod && hasRequestID(req) {
		return &rpcResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Error:   &rpcError{Code: -32600, Message: "invalid request: " + req.Method + " is a notification and must not carry an id"},
		}
	}
	// Notifications (id-less requests) never get a response — for any
	// method, per JSON-RPC 2.0.
	if isNotificationMethod || !hasRequestID(req) {
		return nil
	}

	switch req.Method {
	case "initialize":
		return s.result(req.ID, map[string]interface{}{
			"protocolVersion": protocolVersion,
			"capabilities": map[string]interface{}{
				"tools": map[string]interface{}{"listChanged": false},
			},
			"serverInfo": map[string]interface{}{
				"name":    "monoagentcli",
				"version": s.version(),
			},
			"instructions": "Start with docs(topic) or workflow_list; validate before run; hil_list for pending approvals.",
		})

	case "ping":
		return s.result(req.ID, map[string]interface{}{})

	case "tools/list":
		return s.result(req.ID, map[string]interface{}{"tools": toolDefinitions()})

	case "tools/call":
		return s.handleToolsCall(ctx, req)

	default:
		return &rpcResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Error:   &rpcError{Code: -32601, Message: "method not found: " + req.Method},
		}
	}
}

func (s *Server) handleToolsCall(ctx context.Context, req rpcRequest) *rpcResponse {
	var params struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments,omitempty"`
	}
	if len(req.Params) > 0 {
		if err := json.Unmarshal(req.Params, &params); err != nil {
			return &rpcResponse{
				JSONRPC: "2.0",
				ID:      req.ID,
				Error:   &rpcError{Code: -32602, Message: "invalid tools/call params: " + err.Error()},
			}
		}
	}
	result, err := callTool(ctx, s, params.Name, params.Arguments)
	text := result
	if err != nil {
		text = err.Error()
	}
	return s.result(req.ID, toolResult{
		Content: []toolContent{{Type: "text", Text: text}},
		IsError: err != nil,
	})
}

// closeRuntime releases the lazily-bootstrapped runtime, if any. (A plain
// `defer s.rt.Close()` would capture s.rt at defer time — before lazy
// bootstrap — and no-op.) Safe when the runtime was never built.
func (s *Server) closeRuntime() {
	s.rtMu.Lock()
	rt := s.rt
	s.rtMu.Unlock()
	rt.Close()
}

func (s *Server) result(id json.RawMessage, result interface{}) *rpcResponse {
	if len(id) == 0 {
		id = json.RawMessage("null")
	}
	return &rpcResponse{JSONRPC: "2.0", ID: id, Result: result}
}

func (s *Server) version() string {
	if s.opts.Version == "" {
		return "dev"
	}
	return s.opts.Version
}
