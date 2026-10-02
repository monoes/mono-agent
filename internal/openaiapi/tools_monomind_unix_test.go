//go:build !windows

package openaiapi

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/monoes/mono-agent/internal/monomind"
)

// fakeSpawn is one line of the log the fake codex writes: one process of one
// `codex exec`.
type fakeSpawn struct {
	PID    int      `json:"pid"`
	Round  int      `json:"round"`
	Argv   []string `json:"argv"`
	Prompt string   `json:"prompt"`
}

func readSpawns(t *testing.T, path string) []fakeSpawn {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	var out []fakeSpawn
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 1<<20), 16<<20)
	for sc.Scan() {
		var s fakeSpawn
		if err := json.Unmarshal(sc.Bytes(), &s); err != nil {
			t.Fatalf("a line of the fake codex's log: %v", err)
		}
		out = append(out, s)
	}
	return out
}

// noneAlive waits for the processes to be gone: a cancelled leg must not leave
// the runtime it killed behind.
func noneAlive(pids []int) []int {
	deadline := time.Now().Add(5 * time.Second)
	for {
		var alive []int
		for _, pid := range pids {
			if err := syscall.Kill(pid, 0); err == nil || errors.Is(err, syscall.EPERM) {
				alive = append(alive, pid)
			}
		}
		if len(alive) == 0 || time.Now().After(deadline) {
			return alive
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// The whole loop against the real monomind, with a fake codex in place of the
// model (monomind drives it as it drives codex, fence protocol and resume
// included): a leg ends at the call and kills what it started, the follow-up
// continues the thread, a follow-up nobody remembers starts from its transcript,
// and the start event of a read-only codex leg is accepted by a sandboxed policy.
// No model is called.
//
//	MONOMIND_SMOKE=1 go test ./internal/openaiapi -run TestToolsOverTheRealMonomind -v
func TestToolsOverTheRealMonomindWithAFakeCodex(t *testing.T) {
	if os.Getenv("MONOMIND_SMOKE") != "1" {
		t.Skip("set MONOMIND_SMOKE=1 to run the tool loop against the real monomind and python3, with a fake codex (no model is called)")
	}
	bin, err := monomind.Find()
	if err != nil {
		t.Fatalf("MONOMIND_SMOKE=1 but monomind was not found: %v", err)
	}
	if _, err := exec.LookPath("python3"); err != nil {
		t.Fatalf("MONOMIND_SMOKE=1 but python3 is not on PATH: %v", err)
	}
	fake, err := filepath.Abs(filepath.Join("testdata", "fake-codex.py"))
	if err != nil {
		t.Fatal(err)
	}
	spawnLog := filepath.Join(t.TempDir(), "spawns.jsonl")
	t.Setenv("CODEX_CLI_BIN", fake)
	t.Setenv("FAKE_CODEX_STATE", t.TempDir())
	t.Setenv("FAKE_CODEX_LOG", spawnLog)
	t.Setenv("FAKE_CODEX_MODE", "parallel") // both calls arrive together: a response carries the first

	h := newHarness(t, monomind.Exec, withReadAccess, func(d *Deps, _ *Config) {
		d.Bin = func(context.Context) (string, error) { return bin, nil }
	})
	secret := h.key(t, "default", "app", false)
	policy := Policy{Max: Sandboxed}

	rec := post(h, policy, secret, toolChatBody("codex/gpt-6-astra", weatherTools, weatherQuestion))
	if rec.Code != 200 {
		t.Fatalf("first request: %d %s", rec.Code, rec.Body)
	}
	first := decodeToolReply(t, rec)
	msg := first.Choices[0].Message
	if first.Choices[0].FinishReason != "tool_calls" || len(msg.ToolCalls) != 1 || msg.ToolCalls[0].Function.Name != "get_weather" ||
		msg.ToolCalls[0].Function.Arguments != `{"city":"Paris"}` || msg.Content == nil || *msg.Content != "looking both up" {
		t.Fatalf("first response (the first of two parallel calls, and the words before them): %s", rec.Body)
	}
	if got := rec.Header().Get("X-Monoagent-Sandbox"); got != "sandboxed" {
		t.Errorf("X-Monoagent-Sandbox = %q", got)
	}
	spawns := readSpawns(t, spawnLog)
	if len(spawns) != 1 || !slices.Contains(spawns[0].Argv, "--sandbox") || !slices.Contains(spawns[0].Argv, "read-only") || slices.Contains(spawns[0].Argv, "workspace-write") {
		t.Fatalf("the first leg's codex: %+v: a codex leg runs read-only", spawns)
	}
	if alive := noneAlive([]int{spawns[0].PID}); len(alive) != 0 {
		t.Errorf("the cancelled leg left %v running", alive)
	}
	thread := "th_fake_" + strconv.Itoa(spawns[0].PID)

	// The follow-up continues the codex thread with the result and nothing else.
	t.Setenv("FAKE_CODEX_MODE", "single")
	rec = post(h, policy, secret, strings.Replace(followUp(msg.ToolCalls[0].ID, `{"city":"Paris","temp_c":21}`), `"model":"claude"`, `"model":"codex/gpt-6-astra"`, 1))
	if rec.Code != 200 {
		t.Fatalf("follow-up: %d %s", rec.Code, rec.Body)
	}
	if got := decodeToolReply(t, rec).Choices[0].Message.Content; got == nil || *got != "FINAL: temps seen 21" {
		t.Fatalf("follow-up answer: %s", rec.Body)
	}
	spawns = readSpawns(t, spawnLog)
	if len(spawns) != 2 || !slices.Contains(spawns[1].Argv, "resume") || !slices.Contains(spawns[1].Argv, thread) {
		t.Fatalf("the follow-up must resume thread %s: %+v", thread, spawns)
	}
	if !strings.Contains(spawns[1].Prompt, "Result of get_weather (call "+msg.ToolCalls[0].ID+")") || strings.Contains(spawns[1].Prompt, "What is the weather in Paris?") {
		t.Errorf("the resumed prompt is the result and nothing the thread already holds: %q", spawns[1].Prompt)
	}

	// A follow-up the server never made a call for starts from its transcript.
	rec = post(h, policy, secret, strings.Replace(followUp("call_from_elsewhere", `{"city":"Paris","temp_c":18}`), `"model":"claude"`, `"model":"codex/gpt-6-astra"`, 1))
	if rec.Code != 200 {
		t.Fatalf("replay: %d %s", rec.Code, rec.Body)
	}
	if got := decodeToolReply(t, rec).Choices[0].Message.Content; got == nil || *got != "FINAL: temps seen 18" {
		t.Fatalf("replay answer: %s", rec.Body)
	}
	spawns = readSpawns(t, spawnLog)
	if len(spawns) != 3 || slices.Contains(spawns[2].Argv, "resume") || !strings.Contains(spawns[2].Prompt, "[tool get_weather (call_from_elsewhere)]") {
		t.Fatalf("a replay is a new thread whose prompt is the transcript: %+v", spawns)
	}

	// A resume of a thread codex does not know is replayed in the same request.
	firstRequest := toolRequest(t, weatherTools, weatherQuestion)
	h.g.conts.put(contRecord{CallID: "call_gone", KeyID: keyIDOf(t, h, secret), ProfileID: "default", Model: "codex/gpt-6-astra", Name: "get_weather",
		Session: "th_that_never_existed", ToolsHash: toolsHash(firstRequest.toolDecls), Convo: convoHash(firstRequest, len(firstRequest.Messages))})
	rec = post(h, policy, secret, strings.Replace(followUp("call_gone", `{"city":"Paris","temp_c":16}`), `"model":"claude"`, `"model":"codex/gpt-6-astra"`, 1))
	if rec.Code != 200 {
		t.Fatalf("resume of a thread that is gone: %d %s", rec.Code, rec.Body)
	}
	if got := decodeToolReply(t, rec).Choices[0].Message.Content; got == nil || *got != "FINAL: temps seen 16" {
		t.Fatalf("answer after the fallback: %s", rec.Body)
	}

	var pids []int
	for _, s := range readSpawns(t, spawnLog) {
		pids = append(pids, s.PID)
	}
	if alive := noneAlive(pids); len(alive) != 0 {
		t.Errorf("processes of the fake codex are still running: %v", alive)
	}
}
