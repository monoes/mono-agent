//go:build !windows

package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/monoes/mono-agent/internal/monomind"
	"github.com/monoes/mono-agent/internal/openaiapi"
)

// fakeStuckMonomind speaks just enough of the Agent Exec Protocol for the
// gateway (handshake, scan, models), and its `agent exec` records its pid,
// starts, and then ignores everything: the cancel frame, stdin closing, SIGTERM.
// Only the group kill that monomind.Exec arms KillGrace after a cancel ends it,
// as with a real monomind under --tools none.
const fakeStuckMonomind = `#!/bin/sh
DIR="$(cd "$(dirname "$0")" && pwd)"
if [ "$1" = "--version" ] && [ "$2" = "--json" ]; then
  echo '{"v":1,"version":"2.22.0","min_caller":"1.0.0","capabilities":["agent-exec","agent-scan","org-json-v1","agent-models"]}'
  exit 0
fi
if [ "$1" = "agent" ] && [ "$2" = "scan" ]; then
  echo '{"v":1,"agents":[{"id":"claude","installed":true,"binary":"/usr/local/bin/claude","version":"2.1.0","install_hint":"","streams_incrementally":true,"native_sandbox":"monomind","sandbox_modes":["read-only","workspace-write","full"]}]}'
  exit 0
fi
if [ "$1" = "agent" ] && [ "$2" = "models" ]; then
  echo '{"v":1,"runtime":"claude","supported":true,"models":[{"id":"default","label":"Default"}]}'
  exit 0
fi
if [ "$1" = "agent" ] && [ "$2" = "exec" ]; then
  echo $$ > "$DIR/exec-pid.txt"
  echo '{"v":1,"type":"start","runtime":"claude","cwd":"/x","pid":1,"streams_incrementally":true,"native_sandbox":"monomind","access":"scoped"}'
  trap '' TERM INT HUP
  while true; do sleep 1; done
fi
echo "unsupported: $*" >&2
exit 2
`

// A second Ctrl+C ends the turns in flight and exits at once, so the agent
// CLIs must be dead by the time killTurns returns. monomind.Exec kills a turn
// that ignores its cancel only KillGrace later, so the wait has to follow that
// grace: a fixed wait as long as the grace returns just before the kill, the
// process exits, and on a system with no parent-death signal (macOS) the agent
// is left running.
func TestKillTurnsWaitsForTheKillTimerOfAStuckTurn(t *testing.T) {
	rt, err := newAPIRuntimeForTest(t, apiFlags{}) // a temporary HOME
	if err != nil {
		t.Fatal(err)
	}
	binDir := t.TempDir() // outside the turn's folder: monomind.Exec refuses a binary inside its cwd
	bin := filepath.Join(binDir, "monomind")
	if err := os.WriteFile(bin, []byte(fakeStuckMonomind), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv(monomind.EnvOverride, bin)
	monomind.ResetCapabilityCache()
	t.Cleanup(monomind.ResetCapabilityCache)
	oldGrace := monomind.KillGrace
	monomind.KillGrace = 5500 * time.Millisecond // longer than any fixed wait killTurns used to give
	t.Cleanup(func() { monomind.KillGrace = oldGrace })

	gw, err := rt.gateway()
	if err != nil {
		t.Fatal(err)
	}
	_, secret, err := rt.deps.Keys.Create(context.Background(), "default", "kill", false)
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	gw.Mount(mux, openaiapi.Policy{Max: openaiapi.Unconfined})
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions",
		strings.NewReader(`{"model":"claude","messages":[{"role":"user","content":"hi"}]}`))
	req.Header.Set("Authorization", "Bearer "+secret)
	ended := make(chan struct{})
	go func() {
		defer close(ended)
		mux.ServeHTTP(httptest.NewRecorder(), req)
	}()

	var pid int
	for i := 0; i < 100 && pid == 0; i++ {
		if b, err := os.ReadFile(filepath.Join(binDir, "exec-pid.txt")); err == nil {
			pid, _ = strconv.Atoi(strings.TrimSpace(string(b)))
		}
		time.Sleep(100 * time.Millisecond)
	}
	if pid == 0 {
		t.Fatal("the fake agent never started")
	}
	t.Cleanup(func() { _ = syscall.Kill(-pid, syscall.SIGKILL); _ = syscall.Kill(pid, syscall.SIGKILL) })

	rt.killTurns()
	if processAlive(pid) {
		t.Fatalf("killTurns returned while the agent process %d was still running: it would outlive a command that exits right after", pid)
	}
	select {
	case <-ended:
	case <-time.After(5 * time.Second):
		t.Error("the request is still being served after killTurns")
	}
}
