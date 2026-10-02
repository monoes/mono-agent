package openaiapi

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/monoes/mono-agent/internal/monomind"
)

// The arguments of a call are what a model wrote, and a model may write a file. A client
// that sends them back is not refused for their size short of the request's own limit; what
// a tool returned keeps its cap.

// callWith is a follow-up that answers a call with the arguments given as a string.
func callWith(callID, arguments, result string) string {
	return toolChatBody("claude", weatherTools, weatherQuestion+
		`,{"role":"assistant","content":null,"tool_calls":[{"id":"`+callID+`","type":"function","function":{"name":"get_weather","arguments":`+jsonString(arguments)+`}}]},`+
		`{"role":"tool","tool_call_id":"`+callID+`","content":`+jsonString(result)+`}`)
}

func TestAHistoryCallMayCarryArgumentsOfAnySizeTheBodyLimitAllows(t *testing.T) {
	arguments := `{"city":"` + strings.Repeat("x", 300<<10) + `"}` // more than a result may be

	if err := validateChat(decodeRequest(t, callWith("call_a", arguments, "21 C"))); err != nil {
		t.Errorf("arguments of %d bytes were refused: %+v", len(arguments), err)
	}
	if err := validateChat(decodeRequest(t, callWith("call_a", arguments, strings.Repeat("r", maxToolResult+1)))); err == nil || err.Param != "messages[2].content" {
		t.Errorf("a result keeps its cap of %d bytes: got %+v", maxToolResult, err)
	}

	// A resume: the model wrote the arguments, the response carried them, and the client sends them back.
	leg := fakeLegExec(func(ctx context.Context, emit func(monomind.Event)) {
		emit(evStart(true, "monomind"))
		emit(evSession("sess-1"))
		emit(evCall("get_weather", arguments))
		<-ctx.Done()
	})
	script := &execScript{turns: []execFunc{leg, answers("It is 21 C.")}}
	h := toolHarness(t, script.exec)
	secret := h.key(t, "default", "app", false)
	rec := post(h, anyPolicy, secret, toolChatBody("claude", weatherTools, weatherQuestion))
	call := decodeToolReply(t, rec).Choices[0].Message.ToolCalls[0]
	if call.Function.Arguments != arguments {
		t.Fatalf("the response carries %d bytes of arguments, want %d", len(call.Function.Arguments), len(arguments))
	}
	rec = post(h, anyPolicy, secret, callWith(call.ID, call.Function.Arguments, "21 C"))
	if calls := script.calls(); rec.Code != http.StatusOK || len(calls) != 2 || calls[1].Resume != "sess-1" {
		t.Errorf("the follow-up with the arguments the response carried must resume the session: status %d, %d turns: %.200s", rec.Code, len(calls), rec.Body)
	}

	// A replay: a follow-up the server made no call for carries the transcript, arguments included.
	script = &execScript{turns: []execFunc{answers("It is 21 C.")}}
	h = toolHarness(t, script.exec)
	rec = post(h, anyPolicy, h.key(t, "default", "app", false), callWith("call_elsewhere", arguments, "21 C"))
	calls := script.calls()
	if rec.Code != http.StatusOK || len(calls) != 1 || calls[0].Resume != "" || !strings.Contains(calls[0].Prompt, strings.Repeat("x", 300<<10)) {
		t.Errorf("a replay carries the arguments: status %d, %d turns: %.200s", rec.Code, len(calls), rec.Body)
	}
}

// The request's own limit still bounds the arguments: a body over it is 413, not a 400 about them.
func TestArgumentsAreBoundedByTheBodyLimitAndNothingElse(t *testing.T) {
	h := toolHarness(t, answers("x"), func(_ *Deps, c *Config) { c.BodyLimit = 1 << 20 })
	arguments := `{"city":"` + strings.Repeat("x", 1<<20) + `"}`
	rec := post(h, anyPolicy, h.key(t, "default", "app", false), callWith("call_a", arguments, "21 C"))
	if rec.Code != http.StatusRequestEntityTooLarge || decodeErrorBody(t, rec)["code"] != "request_too_large" {
		t.Errorf("arguments over the body limit: status %d: %.200s", rec.Code, rec.Body)
	}
}
