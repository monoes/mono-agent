package openaiapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/monoes/mono-agent/internal/monomind"
)

// What happens to the record of a call, and to the time a response has, around a
// failure of a resume, a call that a runtime repeats and a first leg that fails.

// firstCall runs a first request and returns the id of the call it ended at.
func firstCall(t *testing.T, h *harness, secret string) string {
	t.Helper()
	rec := post(h, anyPolicy, secret, toolChatBody("claude", weatherTools, weatherQuestion))
	reply := decodeToolReply(t, rec)
	if len(reply.Choices[0].Message.ToolCalls) != 1 {
		t.Fatalf("the first request did not end at a call: %s", rec.Body)
	}
	return reply.Choices[0].Message.ToolCalls[0].ID
}

// A resume that ended before the model ran, because the runtime was rate limited,
// out of quota or not signed in, left the session as it was: the client's retry
// continues it instead of paying for a replay of the whole transcript.
func TestToolsAResumeThatFailedBeforeTheModelRanKeepsItsSession(t *testing.T) {
	for name, fail := range map[string]execFunc{
		"rate limited":  scriptedExec(evStart(false, "monomind"), evError(monomind.ErrRateLimited, "slow down"), evDone(1)),
		"out of quota":  scriptedExec(evStart(false, "monomind"), evError(monomind.ErrQuota, "usage limit"), evDone(1)),
		"not signed in": scriptedExec(evStart(false, "monomind"), evError(monomind.ErrAuth, "please sign in"), evDone(1)),
		"over budget":   scriptedExec(evStart(false, "monomind"), evError(monomind.ErrBudget, "budget used up"), evDone(1)),
	} {
		for _, stream := range []bool{false, true} {
			script := &execScript{turns: []execFunc{callsWeather(true, "sess-1", ""), fail, answers("It is 21 C.")}}
			h := toolHarness(t, script.exec)
			secret := h.key(t, "default", "app", false)
			body := followUp(firstCall(t, h, secret), "21 C")
			if stream {
				body = strings.Replace(body, `"model":"claude"`, `"model":"claude","stream":true`, 1)
			}

			if rec := post(h, anyPolicy, secret, body); rec.Code == http.StatusOK && !strings.Contains(rec.Body.String(), `"error"`) {
				t.Fatalf("%s (stream %v): the follow-up should have failed: %s", name, stream, rec.Body)
			}
			rec := post(h, anyPolicy, secret, body)
			calls := script.calls()
			if rec.Code != http.StatusOK || len(calls) != 3 || calls[2].Resume != "sess-1" {
				t.Errorf("%s (stream %v): the retry must continue the session: status %d, %d turns", name, stream, rec.Code, len(calls))
			}
		}
	}
}

// evNativeStart and evNativeDenied are a runtime's own tool starting, and monomind
// refusing it (monomind#357): codex using one of the user's own MCP servers, claude
// trying Bash.
func evNativeStart(id string) monomind.Event {
	return monomind.Event{V: 1, Type: monomind.EventToolActivity, ID: id, Name: "lookup", CoderFields: monomind.CoderFields{Phase: "start", Kind: "mcp"}}
}

func evNativeDenied(id string) monomind.Event {
	return monomind.Event{V: 1, Type: monomind.EventToolActivity, ID: id, CoderFields: monomind.CoderFields{Phase: "end", Denied: true}}
}

// A tool of the runtime's own that ran in a resumed leg did what it did: when the leg
// then fails, it is not run again from the transcript (a replay would run the tool a
// second time) and its session is not given back for a retry (which would run it again).
// A tool monomind denied did nothing, and the leg is as untouched as one that never
// tried: claude tries tools often, and every attempt is denied.
func TestToolsAResumeInWhichARuntimeToolRanIsNeitherReplayedNorGivenBack(t *testing.T) {
	runnerError := []monomind.Event{evError(monomind.ErrRunnerError, "boom"), evDone(1)}
	rateLimited := []monomind.Event{evError(monomind.ErrRateLimited, "slow down"), evDone(1)}
	leg := func(activity ...monomind.Event) func(ending []monomind.Event) execFunc {
		return func(ending []monomind.Event) execFunc {
			return scriptedExec(append(append([]monomind.Event{evStart(false, "monomind")}, activity...), ending...)...)
		}
	}
	deniedStart := evNativeStart("tu_1")
	deniedStart.Denied = true
	for _, c := range []struct {
		name     string
		leg      func(ending []monomind.Event) execFunc
		ending   []monomind.Event
		replayed bool // the failed resume is run again from the transcript, in the same request
		kept     bool // otherwise: the retry continues the session
	}{
		{"a tool ran, then the runtime failed", leg(evNativeStart("tu_1")), runnerError, false, false},
		{"a tool ran, then the runtime was rate limited", leg(evNativeStart("tu_1")), rateLimited, false, false},
		{"a tool ran and another was denied, then the runtime failed", leg(evNativeStart("tu_1"), evNativeStart("tu_2"), evNativeDenied("tu_2")), runnerError, false, false},
		{"a tool was denied, then the runtime failed", leg(evNativeStart("tu_1"), evNativeDenied("tu_1")), runnerError, true, false},
		{"a tool was denied, then the runtime was rate limited", leg(evNativeStart("tu_1"), evNativeDenied("tu_1")), rateLimited, false, true},
		{"a tool whose start was marked denied, then the runtime failed", leg(deniedStart), runnerError, true, false},
		{"no tool, then the runtime failed", leg(), runnerError, true, false},
	} {
		for _, stream := range []bool{false, true} {
			script := &execScript{turns: []execFunc{callsWeather(true, "sess-1", ""), c.leg(c.ending), answers("It is 21 C.")}}
			h := toolHarness(t, script.exec)
			secret := h.key(t, "default", "app", false)
			body := followUp(firstCall(t, h, secret), "21 C")
			if stream {
				body = strings.Replace(body, `"model":"claude"`, `"model":"claude","stream":true`, 1)
			}

			first := post(h, anyPolicy, secret, body)
			calls := script.calls()
			if c.replayed {
				if len(calls) != 3 || calls[2].Resume != "" || first.Code != http.StatusOK {
					t.Errorf("%s (stream %v): the failed resume must be run again from the transcript: %d turns, status %d", c.name, stream, len(calls), first.Code)
				}
				continue
			}
			if len(calls) != 2 || (first.Code == http.StatusOK && !strings.Contains(first.Body.String(), `"error"`)) {
				t.Errorf("%s (stream %v): the follow-up must fail and run nothing again: %d turns, status %d: %s", c.name, stream, len(calls), first.Code, first.Body)
				continue
			}
			retry := post(h, anyPolicy, secret, body)
			calls = script.calls()
			if len(calls) != 3 || (calls[2].Resume == "sess-1") != c.kept {
				t.Errorf("%s (stream %v): the retry must %s the session: %d turns, resumed %q", c.name, stream, map[bool]string{true: "continue", false: "not continue"}[c.kept], len(calls), calls[len(calls)-1].Resume)
			}
			if retry.Code != http.StatusOK {
				t.Errorf("%s (stream %v): the retry got %d: %s", c.name, stream, retry.Code, retry.Body)
			}
		}
	}
}

// deadlineRecorder is a response recorder that takes a write deadline as net/http's
// real writer does, and keeps the last one it was given.
type deadlineRecorder struct {
	*httptest.ResponseRecorder
	deadline time.Time
}

func (d *deadlineRecorder) SetWriteDeadline(t time.Time) error {
	d.deadline = t
	return nil
}

// serveTimed posts a chat request and says how long after its start the response
// was given to be written.
func serveTimed(h *harness, secret, body string) (*deadlineRecorder, time.Duration) {
	mux := http.NewServeMux()
	h.g.Mount(mux, anyPolicy)
	r := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Authorization", "Bearer "+secret)
	rec := &deadlineRecorder{ResponseRecorder: httptest.NewRecorder()}
	begin := time.Now()
	mux.ServeHTTP(rec, r)
	return rec, rec.deadline.Sub(begin)
}

// A resume the runtime cannot continue is run again from the transcript in the same
// request: a second turn. The response has the time of both, so that the answer of the
// second is not lost to the deadline set for the first.
func TestToolsAResumeIsGivenTheTimeOfTwoTurns(t *testing.T) {
	script := &execScript{turns: []execFunc{callsWeather(true, "sess-1", ""), answers("It is 21 C.")}}
	h := toolHarness(t, script.exec)
	secret := h.key(t, "default", "app", false)

	rec, first := serveTimed(h, secret, toolChatBody("claude", weatherTools, weatherQuestion))
	id := decodeToolReply(t, rec.ResponseRecorder).Choices[0].Message.ToolCalls[0].ID
	_, resumed := serveTimed(h, secret, followUp(id, "21 C"))

	turn := h.g.cfg.TurnTimeout
	if first < turn || first > turn+3*turnGrace {
		t.Errorf("a first leg is one turn: its response was given %v", first)
	}
	if resumed < first+turn {
		t.Errorf("a resume may be followed by a replay, a second turn: its response was given %v, a first leg's %v", resumed, first)
	}
}

// codex repeats an identical call two or three times in a leg that ends at a call (the
// spike saw an edit written twice). A leg ends at the first: the repeats, which arrive
// while the leg is being cancelled, start no second call, no second record and no
// result.
func TestToolsAnIdenticalCallRepeatedWhileTheLegEndsIsOneCallAndOneRecord(t *testing.T) {
	repeats := func() execFunc {
		return fakeLegExec(func(ctx context.Context, emit func(monomind.Event)) {
			emit(evStart(false, "workspace-write"))
			emit(evSession("th_1"))
			for range 3 {
				emit(evCall("get_weather", `{"city":"Paris"}`))
			}
			<-ctx.Done()
		})
	}
	for _, stream := range []bool{false, true} {
		h := toolHarness(t, repeats())
		secret := h.key(t, "default", "app", false)
		extra := weatherTools
		if stream {
			extra += `,"stream":true`
		}
		rec := post(h, anyPolicy, secret, toolChatBody("codex/gpt-6-astra", extra, weatherQuestion))
		calls := 0
		if stream {
			data, _ := sseEvents(rec.Body.String())
			for _, c := range decodeRawChunks(t, data) {
				if raw, ok := c.Choices[0].Delta["tool_calls"]; ok && strings.Contains(string(raw), `"id"`) {
					calls++
				}
			}
		} else {
			calls = len(decodeToolReply(t, rec).Choices[0].Message.ToolCalls)
		}
		if rec.Code != http.StatusOK || calls != 1 || h.g.conts.size() != 1 {
			t.Errorf("stream %v: status %d, %d calls, %d records: %s", stream, rec.Code, calls, h.g.conts.size(), rec.Body)
		}
	}
}

// A first leg that hit the quota has no session to keep and no record to give back.
func TestToolsAFirstLegThatHitTheQuotaIsATooManyRequests(t *testing.T) {
	quota := scriptedExec(evStart(false, "monomind"), evError(monomind.ErrQuota, "usage limit"), evDone(1))
	h := toolHarness(t, (&execScript{turns: []execFunc{quota}}).exec)
	rec := post(h, anyPolicy, h.key(t, "default", "app", false), toolChatBody("claude", weatherTools, weatherQuestion))
	if rec.Code != http.StatusTooManyRequests || h.g.conts.size() != 0 {
		t.Errorf("status %d, %d records: %s", rec.Code, h.g.conts.size(), rec.Body)
	}
}

// A resume that failed for any other reason may have touched the session, and a
// session the runtime could not continue is gone: neither is kept for a retry.
func TestToolsAResumeThatFailedForAnotherReasonIsNotKept(t *testing.T) {
	quota := scriptedExec(evStart(false, "monomind"), evError(monomind.ErrQuota, "usage limit"), evDone(1))
	for name, failures := range map[string][]execFunc{
		"a runner error after the model spoke":                    {scriptedExec(evStart(false, "monomind"), evText("It is"), evError(monomind.ErrRunnerError, "boom"), evDone(1))},
		"a quota after the model spoke":                           {scriptedExec(evStart(false, "monomind"), evText("It is"), evError(monomind.ErrQuota, "usage limit"), evDone(1))},
		"a timeout":                                               {scriptedExec(evStart(false, "monomind"), evError(monomind.ErrTimeout, "too slow"), evDone(1))},
		"a session that is gone, and a replay that hit the quota": {unknownSession(), quota},
	} {
		turns := append([]execFunc{callsWeather(true, "sess-1", "")}, failures...)
		script := &execScript{turns: append(turns, answers("It is 21 C."))}
		h := toolHarness(t, script.exec)
		secret := h.key(t, "default", "app", false)
		id := firstCall(t, h, secret)

		if rec := post(h, anyPolicy, secret, followUp(id, "21 C")); rec.Code == http.StatusOK {
			t.Fatalf("%s: the follow-up should have failed: %s", name, rec.Body)
		}
		rec := post(h, anyPolicy, secret, followUp(id, "21 C"))
		calls := script.calls()
		if last := calls[len(calls)-1]; rec.Code != http.StatusOK || len(calls) != len(turns)+1 || last.Resume != "" {
			t.Errorf("%s: the retry must start from the transcript: status %d, %d turns, resume %q", name, rec.Code, len(calls), last.Resume)
		}
	}
}
