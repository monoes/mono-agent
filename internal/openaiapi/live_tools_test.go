package openaiapi

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"net/http"
	"os"
	"os/exec"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/monoes/mono-agent/internal/monomind"
)

// One tool conversation per runtime that serves tools (see live_test.go for how
// to run it; it calls real models). The function is the caller's: its result is a
// three-digit reading no model would guess, so an answer that contains it came from
// the result.
// The follow-up must be served by resuming the session (the log line says which
// path served it), and the same follow-up sent again, whose record is used up, by a
// replay of the transcript: both must give an answer that uses the result.
//
// It skips a runtime only when the first request cannot be answered right now (503,
// 429, 502). Every other failure fails it, the follow-ups included: a broken resume
// or replay must not hide behind a skip.
func TestLiveToolLoopPerRuntime(t *testing.T) {
	var nativeAttempts, nativeDenied, leftovers, strays atomic.Int32
	_, secret, h := liveGateway(t, func(inner ExecFunc) ExecFunc {
		return func(ctx context.Context, opts monomind.ExecOptions, onEvent func(monomind.Event)) (*monomind.TurnResult, error) {
			res, err := inner(ctx, opts, func(ev monomind.Event) {
				if ev.Type == monomind.EventToolActivity && ev.Phase != "end" {
					nativeAttempts.Add(1)
					if ev.Denied {
						nativeDenied.Add(1)
					}
				}
				onEvent(ev)
			})
			leftovers.Add(int32(len(besidesTmp(opts.Cwd))))
			strays.Add(int32(len(processesIn(opts.Cwd))))
			return res, err
		}
	})
	var runtimes []string
	for _, m := range decodeModelList(t, h.serve(anyPolicy, http.MethodGet, "/v1/models", secret, "")).Data {
		if m.Monoagent.Model == "default" && slices.Contains(m.Monoagent.Capabilities, "tools") {
			runtimes = append(runtimes, m.Monoagent.Runtime)
		}
	}
	if len(runtimes) == 0 {
		t.Skip("no installed runtime serves tool calling here")
	}
	for _, rt := range runtimes {
		t.Run(rt, func(t *testing.T) {
			nativeAttempts.Store(0)
			nativeDenied.Store(0)
			leftovers.Store(0)
			strays.Store(0)
			liveToolLoop(t, h, secret, rt)
			t.Logf("%s: the runtime tried %d of its own tools, %d denied; %d files and %d processes left in the turns' folders", rt, nativeAttempts.Load(), nativeDenied.Load(), leftovers.Load(), strays.Load())
			if leftovers.Load() != 0 {
				t.Errorf("%s: a tool turn left %d files in its folder", rt, leftovers.Load())
			}
			if strays.Load() != 0 {
				t.Errorf("%s: %d processes were still running in a turn's folder after the turn returned", rt, strays.Load())
			}
		})
	}
}

func liveToolLoop(t *testing.T, h *harness, secret, rt string) {
	t.Helper()
	reading := 100 + rand.IntN(900)
	const tools = `[{"type":"function","function":{"name":"get_sensor_reading","description":"Get the current reading of a sensor, in parts per million. Always use it to answer a question about a sensor.","parameters":{"type":"object","properties":{"sensor":{"type":"string","description":"The id of the sensor."}},"required":["sensor"]}}}]`
	question := jsonString("What is the current reading of the sensor oslo-roof-7? Use the get_sensor_reading function, then answer in one sentence that includes the number.")
	send := func(body string) *httpRecorder {
		return h.serve(anyPolicy, http.MethodPost, "/v1/chat/completions", secret, body)
	}

	// The first request: the model must call the function. A model that answers
	// without calling got 1 of 10 trials wrong in the spike, so it gets a second try.
	var first toolReply
	var firstSecs float64
	for attempt := 1; ; attempt++ {
		begin := time.Now()
		rec := send(`{"model":"` + rt + `","tools":` + tools + `,"messages":[{"role":"user","content":` + question + `}]}`)
		switch rec.Code {
		case http.StatusOK:
		case http.StatusServiceUnavailable, http.StatusTooManyRequests:
			t.Skipf("%s is not usable right now: %d %s", rt, rec.Code, rec.Body)
		case http.StatusBadGateway:
			t.Skipf("%s's runner failed on this machine: %d %s", rt, rec.Code, rec.Body)
		default:
			t.Fatalf("%s: first request: %d %s", rt, rec.Code, rec.Body)
		}
		first, firstSecs = decodeToolReply(t, rec), time.Since(begin).Seconds()
		if first.Choices[0].FinishReason == "tool_calls" || attempt == 2 {
			break
		}
		t.Logf("%s: the model answered without calling the function (%q); trying once more", rt, clipRunes(contentOf(first), 80))
	}
	msg := first.Choices[0].Message
	if first.Choices[0].FinishReason != "tool_calls" || len(msg.ToolCalls) != 1 || msg.ToolCalls[0].Function.Name != "get_sensor_reading" {
		t.Fatalf("%s: the model did not call the function: %+v", rt, first.Choices[0])
	}
	var args struct {
		Sensor string `json:"sensor"`
	}
	if err := json.Unmarshal([]byte(msg.ToolCalls[0].Function.Arguments), &args); err != nil || strings.TrimSpace(args.Sensor) == "" {
		t.Fatalf("%s: arguments %q are not a JSON object with a sensor", rt, msg.ToolCalls[0].Function.Arguments)
	}
	t.Logf("%s: first leg %.1fs: called %s for %q", rt, firstSecs, msg.ToolCalls[0].Function.Name, args.Sensor)

	// The caller runs the call and sends the conversation again with the result.
	assistant, _ := json.Marshal(msg)
	result := jsonString(fmt.Sprintf(`{"sensor":%s,"reading_ppm":%d}`, jsonString(args.Sensor), reading))
	followUp := `{"model":"` + rt + `","tools":` + tools + `,"messages":[{"role":"user","content":` + question + `},` + string(assistant) +
		`,{"role":"tool","tool_call_id":"` + msg.ToolCalls[0].ID + `","content":` + result + `}]}`
	answer := func(what string) string {
		begin := time.Now()
		rec := send(followUp)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: %s: %d %s", rt, what, rec.Code, rec.Body)
		}
		got := decodeToolReply(t, rec)
		text := contentOf(got)
		if got.Choices[0].FinishReason != "stop" || !strings.Contains(text, strconv.Itoa(reading)) {
			t.Fatalf("%s: %s: the answer does not use the result %d: finish %q, %q", rt, what, reading, got.Choices[0].FinishReason, clipRunes(text, 200))
		}
		leg := ""
		for _, f := range strings.Fields(logLineOf(h, rec)) {
			if v, ok := strings.CutPrefix(f, "leg="); ok {
				leg = v
			}
		}
		t.Logf("%s: %s %.1fs, served by %s: %q", rt, what, time.Since(begin).Seconds(), leg, clipRunes(text, 120))
		return leg
	}
	if leg := answer("follow-up"); leg != legResume {
		t.Errorf("%s: the follow-up was served by a %s, not by resuming the session: resume is not shown to work live on %s", rt, leg, rt)
	}
	// The record is used up: the same follow-up again starts from its transcript.
	if leg := answer("the same follow-up again"); leg != legReplay {
		t.Errorf("%s: the repeated follow-up was served by %q, want a replay of the transcript", rt, leg)
	}
}

func contentOf(r toolReply) string {
	if c := r.Choices[0].Message.Content; c != nil {
		return *c
	}
	return ""
}

// The check for processes left behind must be able to see one: a check that reports
// none whatever happens (lsof matching names, not the folder behind a symlink such as
// macOS's /var) would let a leak through every live run. This one runs by default.
func TestProcessesInSeesAProcessInTheFolder(t *testing.T) {
	if _, err := exec.LookPath("lsof"); err != nil {
		t.Skip("lsof is not installed")
	}
	dir := t.TempDir()
	cmd := exec.Command("sleep", "30")
	cmd.Dir = dir
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	pid := strconv.Itoa(cmd.Process.Pid)
	if got := processesIn(dir); !slices.Contains(got, pid) {
		t.Errorf("processesIn lists %v for a folder that process %s runs in", got, pid)
	}
	_ = cmd.Process.Kill()
	_ = cmd.Wait()
	if got := processesIn(dir); len(got) != 0 {
		t.Errorf("processesIn lists %v for a folder nothing runs in", got)
	}
}

// processesIn lists the processes, other than this one, whose working directory is
// under dir; none where lsof is not installed. A leg that ends at a call has its
// process group killed, so a process still in the turn's folder after the turn
// returned would be one that outlived it. The kernel may take a moment to reap the
// group: it looks again for up to two seconds.
func processesIn(dir string) []string {
	lsof, err := exec.LookPath("lsof")
	if err != nil {
		return nil
	}
	var pids []string
	for deadline := time.Now().Add(2 * time.Second); ; time.Sleep(100 * time.Millisecond) {
		out, _ := exec.Command(lsof, "-a", "-d", "cwd", "-t", "+D", dir).Output()
		pids = slices.DeleteFunc(strings.Fields(string(out)), func(pid string) bool { return pid == strconv.Itoa(os.Getpid()) })
		if len(pids) == 0 || time.Now().After(deadline) {
			return pids
		}
	}
}
