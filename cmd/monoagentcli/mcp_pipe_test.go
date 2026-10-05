package main

// The MCP tools that have a command for a twin return the document of that command's --json. Both are
// built by the same function in internal/apiconfig, but each gathers its own inputs (this shell's
// flags and environment, or the MCP server's; the profile; the service manager), and that is where
// they could come apart. These tests drive the tools as a host does, over a pipe to a server of its
// own, and compare what comes back with the command's output byte for byte.

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/monoes/mono-agent/internal/apiconfig"
	"github.com/monoes/mono-agent/internal/mcp"
)

// mcpOptions are the options of a server over the database of the CLI's tests, with mutations
// allowed. Its service manager is the one the CLI gets (newInstaller), so that both see the same
// daemon registration; its heartbeat and environment are the process's, as the CLI's are.
func mcpOptions(t *testing.T, dbPath, profile string, allowExposure bool) mcp.Options {
	t.Helper()
	t.Setenv("MONOAGENT_MCP_ALLOW_MUTATIONS", "")
	t.Setenv("MONOAGENT_MCP_ALLOW_API_EXPOSURE", "")
	return mcp.Options{
		DBPath: dbPath, Profile: profile, WorkflowsDir: filepath.Join(t.TempDir(), "workflows"), Version: "test",
		AllowMutations: true, AllowAPIExposure: allowExposure,
		APIEnv: apiconfig.Env{Installer: newInstaller()},
	}
}

// mcpSession is a server of its own, served over a pipe.
type mcpSession struct {
	t      *testing.T
	in     *io.PipeWriter
	lines  chan []byte
	served chan error
	next   int
	closed bool
}

func newMCPSession(t *testing.T, o mcp.Options) *mcpSession {
	t.Helper()
	srv := mcp.NewServer(o)
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	m := &mcpSession{t: t, in: inW, lines: make(chan []byte, 64), served: make(chan error, 1), next: 1}
	go func() {
		m.served <- srv.Serve(context.Background(), inR, outW)
		outW.Close()
	}()
	go func() {
		r := bufio.NewReader(outR)
		for {
			line, err := r.ReadBytes('\n')
			if len(line) > 0 {
				m.lines <- line
			}
			if err != nil {
				close(m.lines)
				return
			}
		}
	}()
	t.Cleanup(m.close)
	return m
}

// call makes one tools/call and returns the text of its answer and whether it is an error. Calls are
// made one after the other: each waits for its answer.
func (m *mcpSession) call(name string, args any) (string, bool) {
	m.t.Helper()
	id := m.next
	m.next++
	request, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0", "id": id, "method": "tools/call",
		"params": map[string]any{"name": name, "arguments": args},
	})
	if err != nil {
		m.t.Fatal(err)
	}
	if _, err := m.in.Write(append(request, '\n')); err != nil {
		m.t.Fatal(err)
	}
	var line []byte
	select {
	case l, ok := <-m.lines:
		if !ok {
			m.t.Fatalf("the MCP server closed its output before it answered %s", name)
		}
		line = l
	case <-time.After(30 * time.Second):
		m.t.Fatalf("the MCP server did not answer %s within 30 s", name)
	}
	var resp struct {
		Result struct {
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
			IsError bool `json:"isError"`
		} `json:"result"`
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(line, &resp); err != nil {
		m.t.Fatalf("not a JSON-RPC answer: %v\n%s", err, line)
	}
	if resp.Error != nil || len(resp.Result.Content) != 1 {
		m.t.Fatalf("%s: a protocol error or no content: %s", name, line)
	}
	return resp.Result.Content[0].Text, resp.Result.IsError
}

// toolNames asks the server for its tool list, as a host does, and returns the names.
func (m *mcpSession) toolNames() []string {
	m.t.Helper()
	id := m.next
	m.next++
	if _, err := m.in.Write([]byte(`{"jsonrpc":"2.0","id":` + strconv.Itoa(id) + `,"method":"tools/list"}` + "\n")); err != nil {
		m.t.Fatal(err)
	}
	var line []byte
	select {
	case l, ok := <-m.lines:
		if !ok {
			m.t.Fatal("the MCP server closed its output before it listed its tools")
		}
		line = l
	case <-time.After(30 * time.Second):
		m.t.Fatal("the MCP server did not list its tools within 30 s")
	}
	var resp struct {
		Result struct {
			Tools []struct {
				Name string `json:"name"`
			} `json:"tools"`
		} `json:"result"`
	}
	if err := json.Unmarshal(line, &resp); err != nil {
		m.t.Fatalf("not a JSON-RPC answer: %v\n%s", err, line)
	}
	names := make([]string, len(resp.Result.Tools))
	for i, tl := range resp.Result.Tools {
		names[i] = tl.Name
	}
	return names
}

// mustCall is call for a tool that must succeed.
func (m *mcpSession) mustCall(name string, args any) string {
	m.t.Helper()
	text, isErr := m.call(name, args)
	if isErr {
		m.t.Fatalf("%s failed: %s", name, text)
	}
	return text
}

// close ends the server's input and waits for it to stop.
func (m *mcpSession) close() {
	if m.closed {
		return
	}
	m.closed = true
	m.in.Close()
	select {
	case err := <-m.served:
		if err != nil {
			m.t.Errorf("the MCP server stopped with %v", err)
		}
	case <-time.After(30 * time.Second):
		m.t.Error("the MCP server did not stop when its input ended")
	}
}

// mcpOnce makes one call to a server of its own and stops it.
func mcpOnce(t *testing.T, o mcp.Options, name string, args any) (string, bool) {
	t.Helper()
	m := newMCPSession(t, o)
	text, isErr := m.call(name, args)
	m.close()
	return text, isErr
}

// asMCPDoc is a document of a command as the tool of an MCP server says it: the command's output
// without its last newline, and with the environment it names its own.
func asMCPDoc(cli string) string {
	return strings.Replace(strings.TrimSuffix(cli, "\n"), `"environment": "shell"`, `"environment": "mcp"`, 1)
}
