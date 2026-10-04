//go:build !windows

package main

import (
	"context"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/monoes/mono-agent/internal/monomind"
)

// fakeSlowMonomind speaks just enough of the Agent Exec Protocol for the gateway
// (handshake, scan, models); its `agent exec` starts, tells the test it has, and
// finishes a normal turn once the test creates a file.
const fakeSlowMonomind = `#!/bin/sh
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
  echo '{"v":1,"type":"start","runtime":"claude","cwd":"/x","pid":1,"streams_incrementally":true,"native_sandbox":"monomind","access":"scoped"}'
  touch "$DIR/started"
  while [ ! -f "$DIR/release" ]; do sleep 0.05; done
  echo '{"v":1,"type":"assistant","text":"all done"}'
  echo '{"v":1,"type":"result","subtype":"success","is_error":false,"stop_reason":"end_turn","input_tokens":3,"output_tokens":2}'
  echo '{"v":1,"type":"done","exit_code":0}'
  exit 0
fi
echo "unsupported: $*" >&2
exit 2
`

// Stopping the dedicated listener gives a request that is running its grace
// before its turn is ended: drain stops the listener first, and only then ends
// the turns it still has. Ending the turns first would cut every request in
// flight short at once.
func TestDrainLetsARunningRequestFinishBeforeItEndsTurns(t *testing.T) {
	rt, err := newAPIRuntimeForTest(t, apiFlags{v1Addr: "127.0.0.1:0"}) // a temporary HOME
	if err != nil {
		t.Fatal(err)
	}
	binDir := t.TempDir() // outside the turn's folder: monomind.Exec refuses a binary inside its cwd
	bin := filepath.Join(binDir, "monomind")
	if err := os.WriteFile(bin, []byte(fakeSlowMonomind), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv(monomind.EnvOverride, bin)
	monomind.ResetCapabilityCache()
	t.Cleanup(monomind.ResetCapabilityCache)

	addr, err := rt.startV1(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	_, secret, err := rt.deps.Keys.Create(context.Background(), "default", "drain", false)
	if err != nil {
		t.Fatal(err)
	}

	type result struct {
		code int
		body string
	}
	got := make(chan result, 1)
	go func() {
		req, _ := http.NewRequest(http.MethodPost, "http://"+addr+"/v1/chat/completions",
			strings.NewReader(`{"model":"claude","messages":[{"role":"user","content":"hi"}]}`))
		req.Header.Set("Authorization", "Bearer "+secret)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			got <- result{0, err.Error()}
			return
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		got <- result{resp.StatusCode, string(b)}
	}()
	for i := 0; i < 200; i++ { // the turn is running
		if _, err := os.Stat(filepath.Join(binDir, "started")); err == nil {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if _, err := os.Stat(filepath.Join(binDir, "started")); err != nil {
		t.Fatal("the turn never started")
	}

	drained := make(chan struct{})
	go func() { rt.drain(); close(drained) }()
	time.Sleep(500 * time.Millisecond) // drain is under way: a drain that ended the turn would have by now
	if err := os.WriteFile(filepath.Join(binDir, "release"), []byte("go"), 0o600); err != nil {
		t.Fatal(err)
	}

	select {
	case r := <-got:
		if r.code != http.StatusOK || !strings.Contains(r.body, "all done") {
			t.Errorf("a request that was running when the listener was told to stop must finish: %d %s", r.code, r.body)
		}
	case <-time.After(20 * time.Second):
		t.Fatal("the request never finished")
	}
	select {
	case <-drained:
	case <-time.After(20 * time.Second):
		t.Fatal("drain never returned")
	}
}
