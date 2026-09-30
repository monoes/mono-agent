//go:build !windows

package main

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/monoes/mono-agent/internal/ai"
	"github.com/monoes/mono-agent/internal/ai/chatevents"
	"github.com/monoes/mono-agent/internal/monomind"
)

// writeTwoWorkerMonomind is a fake monomind whose lead spawns two workers
// without waiting, then waits for both and reports what org_wait said. The
// "slow" worker runs a 30s sleep child (its pid in dir/slow.pid) until it
// is stopped; the "quick" one answers once dir/release exists, so the test
// decides when it finishes instead of racing a timer.
func writeTwoWorkerMonomind(t *testing.T) (bin, dir string) {
	t.Helper()
	dir = t.TempDir()
	bin = filepath.Join(dir, "monomind")
	script := `#!/bin/sh
if [ "$1" = "--version" ]; then echo '{"v":1,"version":"9.0.0","min_caller":"1.0.0","capabilities":["agent-exec","agent-scan","org-json-v1"]}'; exit 0; fi
if [ "$1" = "pick" ]; then echo '{"agents":{"confident":false,"ranked":[]},"skills":{"confident":false,"ranked":[]}}'; exit 0; fi
if [ "$1" = "agent" ] && [ "$2" = "exec" ]; then
  sys=""; prompt=""; prev=""
  for a in "$@"; do
    if [ "$prev" = "--system-file" ]; then sys="$a"; fi
    if [ "$prev" = "--prompt-file" ]; then prompt="$a"; fi
    prev="$a"
  done
  if [ -n "$sys" ] && grep -q "a worker in a team" "$sys"; then
    echo '{"v":1,"type":"start","runtime":"claude","cwd":"/w","pid":2,"access":"read"}'
    if grep -q SLOW "$prompt"; then
      sleep 30 &
      echo $! > '` + filepath.Join(dir, "slow.pid") + `'
      wait
      echo '{"v":1,"type":"result","subtype":"success","is_error":false,"stop_reason":"end_turn","text":"slow finished anyway"}'
    else
      i=0
      while [ ! -f '` + filepath.Join(dir, "release") + `' ] && [ $i -lt 600 ]; do sleep 0.1; i=$((i+1)); done
      echo '{"v":1,"type":"result","subtype":"success","is_error":false,"stop_reason":"end_turn","text":"quick report"}'
    fi
    echo '{"v":1,"type":"done","exit_code":0}'
    exit 0
  fi
  echo '{"v":1,"type":"start","runtime":"claude","cwd":"/w","pid":1,"access":"full"}'
  echo '{"v":1,"type":"tool_call","id":"c1","name":"org_spawn","args":{"brief":"investigate the SLOW path"}}'
  read -r r1
  echo '{"v":1,"type":"tool_call","id":"c2","name":"org_spawn","args":{"brief":"investigate the quick path"}}'
  read -r r2
  echo '{"v":1,"type":"tool_call","id":"c3","name":"org_wait","args":{"agent_ids":["w1","w2"],"timeout_s":60}}'
  read -r r3
  printf '%s\n' "$r3" > '` + filepath.Join(dir, "wait.json") + `'
  echo '{"v":1,"type":"assistant","text":"The lead is done."}'
  echo '{"v":1,"type":"result","subtype":"success","is_error":false,"stop_reason":"end_turn","text":"The lead is done."}'
  echo '{"v":1,"type":"done","exit_code":0}'
  exit 0
fi
echo "unsupported: $*" >&2
exit 2
`
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv(monomind.EnvOverride, bin)
	return bin, dir
}

// Stopping one of two workers cancels only that one: the other finishes
// its work, the lead gets both results and completes the turn, and the
// stopped worker's process tree is gone. A second stop is a no-op.
func TestChatTurnStopStopsOneWorker(t *testing.T) {
	t.Setenv("TYPESAFE_API_KEY", "")
	dbPath := newChatCLITestDB(t)
	bin, dir := writeTwoWorkerMonomind(t)
	withCoderCaps(t, append(monomind.CoderCapabilities, monomind.CapAgentExecFullAccessTools, monomind.CapAgentExecAccessRead)...)
	withCoderScan(t, orgScan(true))
	setCoderSettings(t, dbPath, coderSettings{Enabled: true})
	store := openTestChatStore(t, dbPath)
	conv, err := store.CreateConversationMode("default", "agent", "general", "claude", "", "opus", ai.ModeCoder, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if out, code := runChatHistory(t, dbPath, "default", "set-org", conv.ID, "dynamic"); code != 0 {
		t.Fatalf("set-org: %s", out)
	}
	mailbox := agentControlDir(&globalConfig{DBPath: dbPath}, "turn-1")

	type stopped struct {
		res      chatAgentStopResult
		err      error
		w2Status string
	}
	got := make(chan stopped, 1)
	go func() {
		var s stopped
		defer func() {
			// Let the quick worker finish, whatever happened here.
			os.WriteFile(filepath.Join(dir, "release"), nil, 0o600)
			got <- s
		}()
		pidFile := filepath.Join(dir, "slow.pid")
		for deadline := time.Now().Add(20 * time.Second); ; time.Sleep(50 * time.Millisecond) {
			if _, err := os.Stat(pidFile); err == nil {
				break
			}
			if time.Now().After(deadline) {
				s.err = os.ErrDeadlineExceeded
				return
			}
		}
		s.res, s.err = stopChatAgent(context.Background(), store, "default", mailbox, conv.ID, "turn-1", "w1", 10*time.Second)
		_, s.w2Status, _ = agentStatusInTurn(store, "default", conv.ID, "turn-1", "w2")
	}()

	out, err := runChatCmd(t, dbPath, bin, "--conversation", conv.ID, "--turn", "turn-1", "--", "look at both paths")
	if err != nil {
		t.Fatalf("turn: %v\n%s", err, out)
	}
	s := <-got
	if s.err != nil {
		t.Fatalf("stop: %v", s.err)
	}
	if !s.res.Requested || s.res.Status != chatevents.AgentCancelled || s.res.TurnStatus != "active" {
		t.Errorf("stop result = %+v, want a requested stop that cancelled w1 while the turn ran", s.res)
	}
	if s.w2Status == chatevents.AgentCancelled || s.w2Status == "" {
		t.Errorf("w2 right after w1 was stopped: %q, want it still running", s.w2Status)
	}

	j := parseJournaledTurn(t, out)
	outcomes := map[string]string{}
	for _, e := range j.byType(chatevents.EventAgentFinished) {
		var p chatevents.AgentFinishedPayload
		json.Unmarshal(e.Payload, &p)
		outcomes[p.AgentID] = p.Outcome
	}
	if outcomes["w1"] != chatevents.AgentCancelled || outcomes["w2"] != chatevents.AgentDone {
		t.Errorf("agent.finished outcomes = %v, want w1 cancelled and w2 done", outcomes)
	}
	cancelledStatus := false
	for _, e := range j.byType(chatevents.EventAgentStatus) {
		var p chatevents.AgentStatusPayload
		json.Unmarshal(e.Payload, &p)
		if p.AgentID == "w1" && p.To == chatevents.AgentCancelled {
			cancelledStatus = true
		}
	}
	if !cancelledStatus {
		t.Error("no agent.status to cancelled for w1")
	}
	if p := j.finished(t); p.Status != chatevents.StatusCompleted {
		t.Errorf("the lead's turn = %+v, want completed", p)
	}
	wait, _ := os.ReadFile(filepath.Join(dir, "wait.json"))
	if !strings.Contains(string(wait), "quick report") {
		t.Errorf("the lead's org_wait result = %s", wait)
	}

	// Nothing orphaned: the stopped worker's sleep child is gone, and so
	// is the turn's mailbox.
	pidText, _ := os.ReadFile(filepath.Join(dir, "slow.pid"))
	if pid, err := strconv.Atoi(strings.TrimSpace(string(pidText))); err != nil {
		t.Errorf("slow.pid = %q", pidText)
	} else if syscall.Kill(pid, 0) == nil {
		syscall.Kill(pid, syscall.SIGKILL)
		t.Errorf("the stopped worker's child %d is still running", pid)
	}
	if _, err := os.Stat(mailbox); !os.IsNotExist(err) {
		t.Errorf("mailbox %s left behind: %v", mailbox, err)
	}

	// Idempotent: w1 again, a finished worker, and an unknown one.
	for agent, want := range map[string]string{"w1": chatevents.AgentCancelled, "w2": chatevents.AgentDone, "w9": agentStatusUnknown} {
		// The app's argv: ids after "--", the agent as --agent=.
		out, code := runChatHistoryRoot(t, dbPath, "turn", "stop", "--agent="+agent, "--", conv.ID, "turn-1")
		var res chatAgentStopResult
		decodeChatJSON(t, out, &res)
		if code != 0 || res.Requested || res.Status != want {
			t.Errorf("stop %s after the turn: exit %d %+v, want a no-op reporting %s", agent, code, res, want)
		}
	}
}

// runChatHistoryRoot runs `chat <args>` with --json, like runChatHistory
// without the implied `history`.
func runChatHistoryRoot(t *testing.T, dbPath string, args ...string) (string, int) {
	t.Helper()
	cfg := &globalConfig{DBPath: dbPath, ProfileID: "default", JSONOutput: true}
	var err error
	out := captureStdout(t, func() {
		cmd := newChatCmd(cfg)
		cmd.SetArgs(args)
		cmd.SilenceErrors, cmd.SilenceUsage = true, true
		err = cmd.Execute()
	})
	return out, exitCodeFor(err)
}

// A running turn with no such worker answers at once instead of waiting
// out --wait: the turn takes the request (removes the file) and the journal
// never names the agent.
func TestChatTurnStopUnknownAgentWhileRunning(t *testing.T) {
	dbPath := newChatCLITestDB(t)
	store := openTestChatStore(t, dbPath)
	conv, err := store.CreateConversationMode("default", "agent", "general", "claude", "", "opus", ai.ModeCoder, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.CreateTurn(conv.ID, "default", "turn-2", "inst", "hi"); err != nil {
		t.Fatal(err)
	}
	mailbox := agentControlDir(&globalConfig{DBPath: dbPath}, "turn-2")
	var calls []string
	stopWatch := watchAgentStops(mailbox, func(id string) { calls = append(calls, id) })
	defer stopWatch()
	start := time.Now()
	res, err := stopChatAgent(context.Background(), store, "default", mailbox, conv.ID, "turn-2", "w7", 10*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Requested || res.Status != agentStatusUnknown || time.Since(start) > 5*time.Second {
		t.Errorf("unknown agent: %+v after %s", res, time.Since(start))
	}
	stopWatch()
	if len(calls) != 1 || calls[0] != "w7" {
		t.Errorf("the turn got stop calls %v, want [w7]", calls)
	}
}

// deadPID is the pid of a process that has exited.
func deadPID(t *testing.T) int {
	t.Helper()
	cmd := exec.Command("true")
	if err := cmd.Run(); err != nil {
		t.Fatal(err)
	}
	return cmd.Process.Pid
}

// A turn killed with -9 stays active in the database and leaves its
// folder: a stop sees the dead pid and answers at once instead of waiting
// out --wait, and removes the folder.
func TestChatTurnStopCrashedTurn(t *testing.T) {
	dbPath := newChatCLITestDB(t)
	store := openTestChatStore(t, dbPath)
	conv, err := store.CreateConversationMode("default", "agent", "general", "claude", "", "opus", ai.ModeCoder, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.CreateTurn(conv.ID, "default", "turn-3", "inst", "hi"); err != nil {
		t.Fatal(err)
	}
	mailbox := agentControlDir(&globalConfig{DBPath: dbPath}, "turn-3")
	os.MkdirAll(mailbox, 0o700)
	os.WriteFile(filepath.Join(mailbox, agentControlPIDFile), []byte(strconv.Itoa(deadPID(t))), 0o600)

	start := time.Now()
	res, err := stopChatAgent(context.Background(), store, "default", mailbox, conv.ID, "turn-3", "w1", 10*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if res.Requested || res.Detail != detailTurnGone || res.TurnStatus != "active" || time.Since(start) > 2*time.Second {
		t.Errorf("crashed turn: %+v after %s, want an immediate no-op saying the process is gone", res, time.Since(start))
	}
	if _, err := os.Stat(mailbox); !os.IsNotExist(err) {
		t.Errorf("the crashed turn's folder is still there: %v", err)
	}
}

// The sweep (run by reconcile) removes folders whose pid is dead, and old
// ones with no pid; a live turn's folder and a new one being set up stay.
func TestSweepAgentControl(t *testing.T) {
	root := t.TempDir()
	mk := func(name, pid string) string {
		dir := filepath.Join(root, name)
		os.MkdirAll(dir, 0o700)
		if pid != "" {
			os.WriteFile(filepath.Join(dir, agentControlPIDFile), []byte(pid), 0o600)
		}
		return dir
	}
	dead := mk("dead", strconv.Itoa(deadPID(t)))
	live := mk("live", strconv.Itoa(os.Getpid()))
	fresh := mk("fresh", "")
	old := mk("old", "")
	past := time.Now().Add(-2 * agentControlSweepAge)
	os.Chtimes(old, past, past)

	if n := sweepAgentControl(root, time.Now()); n != 2 {
		t.Errorf("swept %d, want 2", n)
	}
	for dir, want := range map[string]bool{dead: false, old: false, live: true, fresh: true} {
		_, err := os.Stat(dir)
		if exists := err == nil; exists != want {
			t.Errorf("%s exists = %v, want %v", filepath.Base(dir), exists, want)
		}
	}
}

func TestChatTurnStopRejectsDashIDs(t *testing.T) {
	dbPath := newChatCLITestDB(t)
	for _, args := range [][]string{
		{"turn", "stop", "--agent=-x", "--", "c1", "t1"},
		{"turn", "stop", "--agent=w1", "--", "c1", "-t1"},
	} {
		if out, code := runChatHistoryRoot(t, dbPath, args...); code == 0 {
			t.Errorf("%v: exit 0 (%s), want invalid input", args, out)
		}
	}
}
