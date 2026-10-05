//go:build !windows

package mcp

// api_models_list asks every installed runtime for its models: processes. These
// tests count them, and make them hang, with a fake monomind that records each
// call in files next to itself and is told what to do by files too.

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/monoes/mono-agent/internal/monomind"
)

// countingMonomind is the fake of fakeAPIMonomind (claude, codex, antigravity)
// that appends a line to <dir>/scans for each `agent scan` and to <dir>/models for
// each `agent models`, and misbehaves when a control file exists:
//
//	scan-delay   its content is the seconds a scan sleeps (in a child) before it answers
//	scan-hang    a scan records its pid in scan.pid and becomes `sleep 8` (it can be killed)
//	models-hang  a listing sleeps 8 seconds in a child process that holds its pipe
type countingMonomind struct{ dir string }

func newCountingMonomind(t *testing.T) countingMonomind {
	t.Helper()
	dir := t.TempDir()
	script := `#!/bin/sh
DIR="$(cd "$(dirname "$0")" && pwd)"
if [ "$1" = "--version" ] && [ "$2" = "--json" ]; then
  echo '{"v":1,"version":"2.22.0","min_caller":"1.0.0","capabilities":["agent-exec","agent-scan","org-json-v1","agent-models","agent-exec-sandbox"]}'
  exit 0
fi
if [ "$1" = "agent" ] && [ "$2" = "scan" ]; then
  echo "$$" >> "$DIR/scans"
  if [ -f "$DIR/scan-hang" ]; then echo "$$" > "$DIR/scan.pid"; exec sleep 8; fi
  if [ -f "$DIR/scan-delay" ]; then sleep "$(cat "$DIR/scan-delay")"; fi
  echo '{"v":1,"agents":[
    {"id":"claude","installed":true,"binary":"/usr/local/bin/claude","version":"2.1.0","install_hint":"","native_sandbox":"monomind","sandbox_modes":["read-only","workspace-write","full"]},
    {"id":"codex","installed":true,"binary":"/usr/local/bin/codex","version":null,"install_hint":"","native_sandbox":"full","sandbox_modes":["read-only","workspace-write","full"]},
    {"id":"antigravity","installed":true,"binary":"/usr/local/bin/agy","version":"1.2.14","install_hint":"","native_sandbox":"none","sandbox_modes":["restricted","full"]}]}'
  exit 0
fi
if [ "$1" = "agent" ] && [ "$2" = "models" ]; then
  echo "$4" >> "$DIR/models"
  if [ -f "$DIR/models-hang" ]; then sleep 8; fi
  case "$4" in
    claude) echo '{"v":1,"runtime":"claude","supported":true,"models":[{"id":"default","label":"Default"}]}' ;;
    codex) echo '{"v":1,"runtime":"codex","supported":true,"models":[{"id":"gpt-6-astra","label":"GPT-6-Astra"}]}' ;;
    antigravity) echo '{"v":1,"runtime":"antigravity","supported":true,"models":[{"id":"gemini-3.8-flash-high","label":"Gemini 3.8 Flash"}]}' ;;
  esac
  exit 0
fi
exit 2
`
	bin := filepath.Join(dir, "monomind")
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv(monomind.EnvOverride, bin)
	monomind.ResetCapabilityCache()
	t.Cleanup(monomind.ResetCapabilityCache)
	return countingMonomind{dir}
}

// set creates a control file with the given content.
func (m countingMonomind) set(t *testing.T, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(m.dir, name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// count is how many lines the fake has recorded in a file: how many times it ran.
func (m countingMonomind) count(name string) int {
	b, err := os.ReadFile(filepath.Join(m.dir, name))
	if err != nil {
		return 0
	}
	return len(strings.Fields(string(b)))
}

// waitFor waits until the fake has recorded at least n lines in a file.
func (m countingMonomind) waitFor(t *testing.T, name string, n int) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for m.count(name) < n {
		if time.Now().After(deadline) {
			t.Fatalf("the fake monomind never recorded %d lines in %s", n, name)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// Calls at once, as a host sends them, share one load: one scan and one listing per
// runtime, whatever the number of calls, and calls after it within the TTL start
// nothing. Each call used to build a catalog of its own, so twenty calls started a
// hundred processes.
func TestAPIModelsListCallsAtOnceShareOneLoad(t *testing.T) {
	pinAPIEnv(t)
	fake := newCountingMonomind(t)
	fake.set(t, "scan-delay", "0.4") // long enough for every call to be in flight
	s, _ := newAPIKeyServer(t, false)
	w := newWireSession(t, s)

	const calls = 20
	ids := make([]string, calls)
	for i := range ids {
		ids[i] = w.send("api_models_list", nil)
	}
	var first string
	for _, id := range ids {
		text, isErr := w.await(id)
		if isErr {
			t.Fatalf("a call failed: %s", scrubbed(text))
		}
		if first == "" {
			first = text
		} else if text != first {
			t.Error("calls at once must get the same answer")
		}
	}
	for range 3 { // one after the other, within the TTL
		w.call("api_models_list", nil)
	}
	time.Sleep(300 * time.Millisecond) // a reload started by mistake would show by now
	w.finish()

	if got := fake.count("scans"); got != 1 {
		t.Errorf("%d calls started %d scans, want 1", calls+3, got)
	}
	if got := fake.count("models"); got != 3 {
		t.Errorf("%d calls started %d runtime listings, want 3 (one per installed runtime)", calls+3, got)
	}
}

// The server stops when its input ends, giving its calls three seconds (Serve's
// postEOFGrace) before it cancels them. A call that is waiting for monomind must
// not hold it past that, whether monomind's own command watches its context (a scan
// is killed) or leaves a child holding its pipe (a shell killed under a `sleep`
// leaves it reading until the child ends, the way a node tool with children can).
func TestAPIModelsListEndsWithItsServer(t *testing.T) {
	for _, c := range []struct {
		name, control, runs string
		killed              bool
	}{
		{"a scan that can be killed", "scan-hang", "scans", true},
		{"a listing whose child holds the pipe", "models-hang", "models", false},
	} {
		t.Run(c.name, func(t *testing.T) {
			pinAPIEnv(t)
			fake := newCountingMonomind(t)
			fake.set(t, c.control, "")
			s, _ := newAPIKeyServer(t, false)
			w := newWireSession(t, s)
			w.send("api_models_list", nil)
			fake.waitFor(t, c.runs, 1)

			closedAt := time.Now()
			w.finish()
			if took := time.Since(closedAt); took > 5*time.Second {
				t.Errorf("the server took %v to stop after its input ended, which it gives its calls 3 s for", took)
			}
			if c.killed {
				pid := readPid(t, filepath.Join(fake.dir, "scan.pid"))
				deadline := time.Now().Add(3 * time.Second)
				for syscall.Kill(pid, 0) == nil && time.Now().Before(deadline) {
					time.Sleep(10 * time.Millisecond)
				}
				if syscall.Kill(pid, 0) == nil {
					t.Error("the scan is still running after the server stopped: its load did not end with it")
				}
			}
		})
	}
}

func readPid(t *testing.T, path string) int {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(b)))
	if err != nil {
		t.Fatal(err)
	}
	return pid
}
