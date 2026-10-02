package openaiapi

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"net/http"
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
	var nativeAttempts, nativeDenied, leftovers atomic.Int32
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
			liveToolLoop(t, h, secret, rt)
			t.Logf("%s: the runtime tried %d of its own tools, %d denied; %d files left in the turns' folders", rt, nativeAttempts.Load(), nativeDenied.Load(), leftovers.Load())
			if leftovers.Load() != 0 {
				t.Errorf("%s: a tool turn left %d files in its folder", rt, leftovers.Load())
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
