package openaiapi

import (
	"strings"
	"testing"
)

const (
	callParis = `{"id":"call_a","type":"function","function":{"name":"get_weather","arguments":"{\"city\":\"Paris\"}"}}`
	callRome  = `{"id":"call_b","type":"function","function":{"name":"get_weather","arguments":"{\"city\":\"Rome\"}"}}`
)

// toolRequest builds a validated request from messages.
func toolRequest(t *testing.T, extra, messages string) *ChatRequest {
	t.Helper()
	req := decodeRequest(t, toolBody(extra, messages))
	if err := validateChat(req); err != nil {
		t.Fatalf("the test request is not valid: %+v", err)
	}
	return req
}

func TestToolChoiceLine(t *testing.T) {
	cases := []struct {
		pick toolChoice
		want string
	}{
		{toolChoice{}, ""},
		{toolChoice{Mode: choiceAuto}, ""},
		{toolChoice{Mode: choiceNone}, ""},
		{toolChoice{Mode: choiceRequired}, "You must call at least one of the available functions before you answer."},
		{toolChoice{Mode: choiceFunction, Name: "get_weather"}, "You must call the function get_weather before you answer. Never answer without calling it."},
	}
	for _, c := range cases {
		if got := toolChoiceLine(c.pick); got != c.want {
			t.Errorf("toolChoiceLine(%+v) = %q, want %q", c.pick, got, c.want)
		}
	}
}

func TestReplayPromptRendersACompletedRoundAsData(t *testing.T) {
	req := toolRequest(t, `"tools":[`+weatherTool+`]`,
		`{"role":"system","content":"Be brief."},{"role":"user","content":"What is the weather in Paris?"},`+
			`{"role":"assistant","content":null,"tool_calls":[`+callParis+`]},`+
			`{"role":"tool","tool_call_id":"call_a","content":"{\"temp_c\":21}"}`)
	got := replayPrompt(req, true)
	want := "The conversation so far, oldest first." +
		"\n\n[user]\nWhat is the weather in Paris?" +
		"\n\n[assistant]\n(called the function get_weather with arguments {\"city\":\"Paris\"})" +
		"\n\n[tool get_weather (call_a)]\n{\"temp_c\":21}" +
		"\n\n" + toolOutro
	if got != want {
		t.Errorf("replay prompt:\n%s\nwant:\n%s", got, want)
	}
	if strings.Contains(got, "Be brief.") {
		t.Error("a system message belongs to the system prompt, not the transcript")
	}
	for _, phrase := range []string{"Use the function results above", "call a function again only if you still need one"} {
		if !strings.HasSuffix(got, toolOutro) || !strings.Contains(toolOutro, phrase) {
			t.Errorf("the transcript must end by saying %q", phrase)
		}
	}
	if strings.Contains(got, "last user message only") {
		t.Error("a transcript that ends in a tool result must not tell the model to answer the last user message only")
	}
}

func TestReplayPromptKeepsWhatTheAssistantSaidAndEveryCall(t *testing.T) {
	req := toolRequest(t, `"tools":[`+weatherTool+`]`,
		`{"role":"user","content":"Paris and Rome?"},`+
			`{"role":"assistant","content":"Let me look.","tool_calls":[`+callParis+`,`+callRome+`]},`+
			`{"role":"tool","tool_call_id":"call_b","content":"Rome: 25"},`+
			`{"role":"tool","tool_call_id":"call_a","content":"Paris: 21"},`+
			`{"role":"assistant","content":"Paris is 21, Rome is 25."},`+
			`{"role":"user","content":"Thanks. And Oslo?"}`)
	got := replayPrompt(req, true)
	for _, want := range []string{
		"[assistant]\nLet me look.\n(called the function get_weather with arguments {\"city\":\"Paris\"})\n(called the function get_weather with arguments {\"city\":\"Rome\"})",
		"[tool get_weather (call_b)]\nRome: 25\n\n[tool get_weather (call_a)]\nParis: 21",
		"[assistant]\nParis is 21, Rome is 25.\n\n[user]\nThanks. And Oslo?",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("the transcript lacks %q:\n%s", want, got)
		}
	}
}

func TestReplayPromptOfAConversationWithoutDeclaredTools(t *testing.T) {
	req := toolRequest(t, `"tools":[`+weatherTool+`],"tool_choice":"none"`,
		`{"role":"user","content":"hi"},{"role":"assistant","tool_calls":[`+callParis+`]},{"role":"tool","tool_call_id":"call_a","content":"21"}`)
	got := replayPrompt(req, false)
	if !strings.HasSuffix(got, plainToolOutro) || strings.Contains(got, "call a function again") {
		t.Errorf("a turn that cannot call a function must not be told it may:\n%s", got)
	}
	if !strings.Contains(got, "[tool get_weather (call_a)]\n21") {
		t.Errorf("the history is still rendered:\n%s", got)
	}
}

func TestReplayPromptHandlesOddArgumentsAndResults(t *testing.T) {
	req := toolRequest(t, ``,
		`{"role":"user","content":"x"},`+
			`{"role":"assistant","tool_calls":[{"id":"c1","type":"function","function":{"name":"a","arguments":""}},{"id":"c2","function":{"name":"b","arguments":null}}]},`+
			`{"role":"tool","tool_call_id":"c1","content":""},`+
			`{"role":"tool","tool_call_id":"c2","content":[{"type":"text","text":"l1"},{"type":"text","text":"l2"}]}`)
	got := replayPrompt(req, true)
	for _, want := range []string{
		"(called the function a with arguments {})", "(called the function b with arguments {})",
		"[tool a (c1)]\n\n\n[tool b (c2)]\nl1\nl2",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("the transcript lacks %q:\n%s", want, got)
		}
	}
}

func TestResumePromptIsTheResultThenWhateverTheUserSaidNext(t *testing.T) {
	req := toolRequest(t, `"tools":[`+weatherTool+`]`,
		`{"role":"user","content":"Weather in Paris?"},`+
			`{"role":"assistant","content":"Checking.","tool_calls":[`+callParis+`]},`+
			`{"role":"tool","tool_call_id":"call_a","content":"{\"temp_c\":21,\n \"sky\":\"fog\"}"},`+
			`{"role":"user","content":"Also say it in French."}`)
	got := resumePrompt(req, 1)
	want := resumeIntro +
		"\n\nResult of get_weather (call call_a): {\"temp_c\":21,\n \"sky\":\"fog\"}" +
		"\n\nAlso say it in French."
	if got != want {
		t.Errorf("resume prompt:\n%s\nwant:\n%s", got, want)
	}
	if strings.Contains(got, "Weather in Paris?") || strings.Contains(got, "Checking.") {
		t.Error("the session already holds the conversation: the resume prompt carries only what is new")
	}
	// The session holds the CLI's note that the call was rejected: say that the
	// caller really ran it.
	for _, phrase := range []string{"caller", "result"} {
		if !strings.Contains(resumeIntro, phrase) {
			t.Errorf("the resume prompt must say %q", phrase)
		}
	}
}

func TestResumePromptWithoutAFollowingMessage(t *testing.T) {
	req := toolRequest(t, ``, `{"role":"user","content":"q"},{"role":"assistant","tool_calls":[`+callParis+`]},{"role":"tool","tool_call_id":"call_a","content":"21"}`)
	if got, want := resumePrompt(req, 1), resumeIntro+"\n\nResult of get_weather (call call_a): 21"; got != want {
		t.Errorf("resume prompt:\n%s\nwant:\n%s", got, want)
	}
}

func TestResumePromptKeepsEveryResultOfTheCall(t *testing.T) {
	req := toolRequest(t, ``, `{"role":"user","content":"q"},{"role":"assistant","tool_calls":[`+callParis+`]},{"role":"tool","tool_call_id":"call_a","content":"first"},{"role":"tool","tool_call_id":"call_a","content":"second"}`)
	got := resumePrompt(req, 1)
	if !strings.Contains(got, "(call call_a): first\n\nResult of get_weather (call call_a): second") {
		t.Errorf("resume prompt:\n%s", got)
	}
}

func TestTrailingRoundFindsTheLastAssistantMessageWhenItMadeCalls(t *testing.T) {
	cases := []struct {
		name, messages string
		index          int
		ok             bool
	}{
		{"a first request", `{"role":"user","content":"q"}`, -1, false},
		{"a call answered", `{"role":"user","content":"q"},{"role":"assistant","tool_calls":[` + callParis + `]},{"role":"tool","tool_call_id":"call_a","content":"r"}`, 1, true},
		{"a call answered and a user message", `{"role":"user","content":"q"},{"role":"assistant","tool_calls":[` + callParis + `]},{"role":"tool","tool_call_id":"call_a","content":"r"},{"role":"user","content":"more"}`, 1, true},
		{"a call not answered", `{"role":"user","content":"q"},{"role":"assistant","tool_calls":[` + callParis + `]},{"role":"user","content":"more"}`, -1, false},
		{"the last assistant message made no call", `{"role":"user","content":"q"},{"role":"assistant","tool_calls":[` + callParis + `]},{"role":"tool","tool_call_id":"call_a","content":"r"},{"role":"assistant","content":"done"},{"role":"user","content":"again"}`, -1, false},
		{"two calls", `{"role":"user","content":"q"},{"role":"assistant","tool_calls":[` + callParis + `,` + callRome + `]},{"role":"tool","tool_call_id":"call_a","content":"r"},{"role":"tool","tool_call_id":"call_b","content":"r"}`, 1, true},
	}
	for _, c := range cases {
		req := toolRequest(t, ``, c.messages)
		i, ok := trailingRound(req)
		if i != c.index || ok != c.ok {
			t.Errorf("%s: trailingRound = %d, %v, want %d, %v", c.name, i, ok, c.index, c.ok)
		}
	}
}

// A runtime whose native tools monomind gates (claude, chat-only) needs nothing
// more. Any other keeps its native tools in play, which the spike saw pull the
// model away from the declared ones (codex: 31 of 31 native attempts, and the
// declared tool used 0 of 4 times), so its leg runs read-only.
func TestLegAccess(t *testing.T) {
	for class, want := range map[Class]string{ChatOnly: "", Sandboxed: "read", Unconfined: "read"} {
		if got := legAccess(ModelInfo{Class: class}); got != want {
			t.Errorf("legAccess(%s) = %q, want %q", class, got, want)
		}
	}
}
