//go:build !windows

package openaiapi

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
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

// realMonomindWithAFakeCodex is a gateway whose legs run on the real monomind, which
// drives the fake codex in place of the model (as it drives codex, fence protocol and
// resume included). spawnLog is the file the fake writes one line per process to; mode
// is what the fake does (testdata/fake-codex.py). No model is called.
func realMonomindWithAFakeCodex(t *testing.T, mode string) (h *harness, spawnLog string) {
	t.Helper()
	if os.Getenv("MONOMIND_SMOKE") != "1" {
		t.Skip("set MONOMIND_SMOKE=1 to run tools against the real monomind and python3, with a fake codex (no model is called)")
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
	spawnLog = filepath.Join(t.TempDir(), "spawns.jsonl")
	t.Setenv("CODEX_CLI_BIN", fake)
	t.Setenv("FAKE_CODEX_STATE", t.TempDir())
	t.Setenv("FAKE_CODEX_LOG", spawnLog)
	t.Setenv("FAKE_CODEX_MODE", mode)
	h = newHarness(t, monomind.Exec, withReadAccess, func(d *Deps, _ *Config) {
		d.Bin = func(context.Context) (string, error) { return bin, nil }
	})
	return h, spawnLog
}

// The whole loop against the real monomind, with a fake codex in place of the
// model: a leg ends at the call and kills what it started, the follow-up
// continues the thread, a follow-up nobody remembers starts from its transcript,
// and the start event of a read-only codex leg is accepted by a sandboxed policy.
// No model is called.
//
//	MONOMIND_SMOKE=1 go test ./internal/openaiapi -run TestToolsOverTheRealMonomind -v
func TestToolsOverTheRealMonomindWithAFakeCodex(t *testing.T) {
	h, spawnLog := realMonomindWithAFakeCodex(t, "parallel") // both calls arrive together: a response carries the first
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
		Session: "th_that_never_existed", ToolsHash: toolsHash(firstRequest.toolDecls), Convo: convoHash("codex", firstRequest, len(firstRequest.Messages)), Args: argsHash(`{"city":"Paris"}`)})
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

// monomind refuses a function whose name, with its prefix, is longer than 64 characters (a
// name of 60 failed in the spike), so a name of 55 to 64 is given to it, and to the model, by
// an alias: the real monomind takes the alias and the call that comes back is the client's own
// name again, with its arguments.
//
//	MONOMIND_SMOKE=1 go test ./internal/openaiapi -run TestALongToolName -v
func TestALongToolNameOverTheRealMonomind(t *testing.T) {
	h, _ := realMonomindWithAFakeCodex(t, "single") // one call, of the function named by FAKE_CODEX_TOOL
	t.Setenv("FAKE_CODEX_TOOL", aliasOf(longTool))  // what the model is told to call
	secret := h.key(t, "default", "app", false)
	tool := `{"type":"function","function":{"name":"` + longTool + `","description":"Does a thing.","parameters":{"type":"object","properties":{"city":{"type":"string"}},"required":["city"]}}}`
	rec := post(h, Policy{Max: Sandboxed}, secret, toolChatBody("codex/gpt-6-astra", `"tools":[`+tool+`]`, weatherQuestion))
	if rec.Code != http.StatusOK {
		t.Fatalf("a function of 58 characters: %d %s", rec.Code, rec.Body)
	}
	calls := decodeToolReply(t, rec).Choices[0].Message.ToolCalls
	if len(calls) != 1 || calls[0].Function.Name != longTool || calls[0].Function.Arguments != `{"city":"Paris"}` {
		t.Errorf("the call must carry the name the client declared and its arguments: %s", rec.Body)
	}
}

// A coding client sends every tool of every MCP server it has, which is more than 64: the most
// the gateway takes is 128, and the real monomind must take that many in one tools file.
//
//	MONOMIND_SMOKE=1 go test ./internal/openaiapi -run TestAsManyToolsAsTheGatewayTakes -v
func TestAsManyToolsAsTheGatewayTakesOverTheRealMonomind(t *testing.T) {
	h, _ := realMonomindWithAFakeCodex(t, "single") // one call: get_weather for Paris
	secret := h.key(t, "default", "app", false)
	tools := []string{weatherTool}
	for i := 1; i < 128; i++ {
		tools = append(tools, fmt.Sprintf(`{"type":"function","function":{"name":"tool_%03d","description":"Does thing %d.","parameters":{"type":"object","properties":{"x":{"type":"string"}}}}}`, i, i))
	}
	rec := post(h, Policy{Max: Sandboxed}, secret, toolChatBody("codex/gpt-6-astra", `"tools":[`+strings.Join(tools, ",")+`]`, weatherQuestion))
	if rec.Code != http.StatusOK {
		t.Fatalf("128 tools: %d %s", rec.Code, rec.Body)
	}
	if calls := decodeToolReply(t, rec).Choices[0].Message.ToolCalls; len(calls) != 1 || calls[0].Function.Name != "get_weather" || calls[0].Function.Arguments != `{"city":"Paris"}` {
		t.Errorf("the call among 128 tools: %s", rec.Body)
	}
}

// monomind keeps of a call only the keys of the top-level properties of the schema it was
// given, so a schema whose arguments sit in a root anyOf, oneOf or allOf, behind a root
// $ref, or in a then, gave the client {} at every call: nameArguments names the arguments
// of the branches at the top level, and the call arrives whole. No model is called.
//
//	MONOMIND_SMOKE=1 go test ./internal/openaiapi -run TestArgumentsOutsideTheTopLevel -v
func TestArgumentsOutsideTheTopLevelPropertiesReachTheClientOverTheRealMonomind(t *testing.T) {
	h, _ := realMonomindWithAFakeCodex(t, "single") // one call: get_weather for Paris
	secret := h.key(t, "default", "app", false)
	const city = `{"type":"object","properties":{"city":{"type":"string"}},"required":["city"]}`
	const zip = `{"type":"object","properties":{"zip":{"type":"string"}},"required":["zip"]}`
	for name, params := range map[string]string{
		"properties at the top level": city,
		"a root anyOf":                `{"anyOf":[` + city + `,` + zip + `]}`,
		"a root oneOf":                `{"type":"object","oneOf":[` + city + `,` + zip + `]}`,
		"a root allOf":                `{"allOf":[` + city + `]}`,
		"a root $ref":                 `{"$ref":"#/$defs/Args","$defs":{"Args":` + city + `}}`,
		"a then":                      `{"type":"object","properties":{"units":{"type":"string"}},"if":{"required":["units"]},"then":` + city + `}`,
	} {
		tools := `"tools":[{"type":"function","function":{"name":"get_weather","description":"Get the weather.","parameters":` + params + `}}]`
		rec := post(h, Policy{Max: Sandboxed}, secret, toolChatBody("codex/gpt-6-astra", tools, weatherQuestion))
		if rec.Code != 200 {
			t.Errorf("%s: %d %s", name, rec.Code, rec.Body)
			continue
		}
		if calls := decodeToolReply(t, rec).Choices[0].Message.ToolCalls; len(calls) != 1 || calls[0].Function.Arguments != `{"city":"Paris"}` {
			t.Errorf("%s: the call reached the client as %s", name, rec.Body)
		}
	}
}
