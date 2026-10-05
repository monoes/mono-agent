package openaiapi

import (
	"net/http"
	"regexp"
	"runtime"
	"strings"
	"testing"
	"time"
)

// The log line of a request with tools adds how many tools it declared and how the leg that
// answered started, and never the name of a function: not the one declared, not the one
// called. A word that merely contains "leg=replay" would pass a Contains, so the leg is
// read as a token of its own, exactly once.
func TestTheLogLineOfARequestWithToolsHasTheCountAndTheLegAndNoName(t *testing.T) {
	legRE := regexp.MustCompile(`(?:^| )leg=(first|resume|replay)(?: |$)`)
	tools := `"tools":[` + declare(markerName) + `,` + weatherTool + `]`
	followUpBody := func(id string) string {
		return toolChatBody("claude", tools, weatherQuestion+
			`,{"role":"assistant","content":null,"tool_calls":[{"id":"`+id+`","type":"function","function":{"name":"`+markerName+`","arguments":"{\"q\":\"x\"}"}}]},`+
			`{"role":"tool","tool_call_id":"`+id+`","content":"found it"}`)
	}
	for _, stream := range []bool{false, true} {
		extra := ""
		if stream {
			extra = `,"stream":true`
		}
		script := &execScript{turns: []execFunc{callsTheFirstDeclared(`{"q":"x"}`), answers("Done."), answers("Done again.")}}
		h := toolHarness(t, script.exec)
		secret := h.key(t, "default", "app", false)

		first := post(h, anyPolicy, secret, toolChatBody("claude", tools+extra, weatherQuestion))
		var id string
		if stream {
			id = firstMatch(`"id":"(call_[a-z0-9]+)"`, first.Body.String())
		} else {
			id = decodeToolReply(t, first).Choices[0].Message.ToolCalls[0].ID
		}
		resumed := post(h, anyPolicy, secret, strings.Replace(followUpBody(id), `"model":"claude"`, `"model":"claude"`+extra, 1))
		replayed := post(h, anyPolicy, secret, strings.Replace(followUpBody("call_elsewhere"), `"model":"claude"`, `"model":"claude"`+extra, 1))

		for want, rec := range map[string]*httpRecorder{"first": first, "resume": resumed, "replay": replayed} {
			if rec.Code != http.StatusOK {
				t.Fatalf("stream %v, %s: %d %s", stream, want, rec.Code, rec.Body)
			}
			line := logLineOf(h, rec)
			m := legRE.FindAllStringSubmatch(line, -1)
			if strings.Count(line, "leg=") != 1 || len(m) != 1 || m[0][1] != want || !strings.Contains(line, " tools=2 ") {
				t.Errorf("stream %v, %s: the log line must say tools=2 and the leg %q once: %q", stream, want, want, line)
			}
			if strings.Contains(line, markerName) || strings.Contains(line, "get_weather") || strings.Contains(line, "call_") {
				t.Errorf("stream %v, %s: the log line names a function or a call: %q", stream, want, line)
			}
		}
	}
}

// A leg starts what it must stop: the handler goroutines of a call, the keep-alive of a stream,
// the runtime's own. Many of them, first legs, resumes and replays, streamed and not, leave
// nothing behind.
func TestLegsLeaveNoGoroutinesBehind(t *testing.T) {
	script := &execScript{}
	h := toolHarness(t, script.exec)
	secret := h.key(t, "default", "app", false)
	round := func(stream bool) {
		extra := ""
		if stream {
			extra = `,"stream":true`
		}
		script.mu.Lock()
		script.turns, script.opts = []execFunc{callsWeather(true, "sess-1", "Looking."), answers("It is 21 C."), answers("It is 21 C.")}, nil
		script.mu.Unlock()
		rec := post(h, anyPolicy, secret, toolChatBody("claude", weatherTools+extra, weatherQuestion))
		id := firstMatch(`"id":"(call_[a-z0-9]+)"`, rec.Body.String())
		if stream {
			post(h, anyPolicy, secret, strings.Replace(followUp(id, "21 C"), `"model":"claude"`, `"model":"claude"`+extra, 1)) // a resume
		} else {
			id = decodeToolReply(t, rec).Choices[0].Message.ToolCalls[0].ID
			post(h, anyPolicy, secret, followUp(id, "21 C")) // a resume
		}
		post(h, anyPolicy, secret, strings.Replace(followUp("call_elsewhere", "21 C"), `"model":"claude"`, `"model":"claude"`+extra, 1)) // a replay
	}
	round(false) // whatever the first request starts for good (the catalog, the store) is running
	round(true)
	base := runtime.NumGoroutine()
	for range 15 {
		round(false)
		round(true)
	}
	deadline := time.Now().Add(5 * time.Second)
	for runtime.NumGoroutine() > base && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if got := runtime.NumGoroutine(); got > base {
		buf := make([]byte, 1<<16)
		t.Errorf("%d goroutines after 90 requests, %d before them:\n%s", got, base, buf[:runtime.Stack(buf, true)])
	}
}
