package openaiapi

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

const (
	weatherTool = `{"type":"function","function":{"name":"get_weather","description":"Get the weather.","parameters":{"type":"object","properties":{"city":{"type":"string","description":"A city."}},"required":["city"]}}}`
	userHi      = `{"role":"user","content":"hi"}`
)

// toolBody is a request with the given extra members (tools, tool_choice, …) and
// messages, as the JSON text of an object.
func toolBody(extra, messages string) string {
	if extra != "" {
		extra += ","
	}
	return `{"model":"m",` + extra + `"messages":[` + messages + `]}`
}

func TestValidateToolsAcceptsWhatClientsSend(t *testing.T) {
	long54 := strings.Repeat("a", 54)
	var sixtyFour []string
	for i := range 64 {
		sixtyFour = append(sixtyFour, fmt.Sprintf(`{"type":"function","function":{"name":"t%d"}}`, i))
	}
	cases := map[string]string{
		"a minimal tool":          toolBody(`"tools":[{"type":"function","function":{"name":"f"}}]`, userHi),
		"a tool with a schema":    toolBody(`"tools":[`+weatherTool+`]`, userHi),
		"strict and extra keys":   toolBody(`"tools":[{"type":"function","function":{"name":"f","strict":true,"parameters":{"type":"object","properties":{},"additionalProperties":false}}}]`, userHi),
		"empty tools":             toolBody(`"tools":[]`, userHi),
		"null tools":              toolBody(`"tools":null`, userHi),
		"a name of 54":            toolBody(`"tools":[{"type":"function","function":{"name":"`+long54+`"}}]`, userHi),
		"dashes and underscore":   toolBody(`"tools":[{"type":"function","function":{"name":"a-b_C9"}}]`, userHi),
		"64 tools":                toolBody(`"tools":[`+strings.Join(sixtyFour, ",")+`]`, userHi),
		"a string enum":           toolBody(`"tools":[{"type":"function","function":{"name":"f","parameters":{"type":"object","properties":{"m":{"type":"string","enum":["a","b"]}}}}}]`, userHi),
		"a nested numeric enum":   toolBody(`"tools":[{"type":"function","function":{"name":"f","parameters":{"type":"object","properties":{"o":{"type":"object","properties":{"n":{"type":"integer","enum":[1,2]}}}}}}}]`, userHi),
		"a numeric enum":          toolBody(`"tools":[{"type":"function","function":{"name":"f","parameters":{"type":"object","properties":{"n":{"type":"integer","enum":[1,2,3]}}}}}]`, userHi),
		"a mixed enum":            toolBody(`"tools":[{"type":"function","function":{"name":"f","parameters":{"type":"object","properties":{"n":{"enum":["a",2,null]}}}}}]`, userHi),
		"an empty enum":           toolBody(`"tools":[{"type":"function","function":{"name":"f","parameters":{"type":"object","properties":{"n":{"enum":[]}}}}}]`, userHi),
		"an enum that is no list": toolBody(`"tools":[{"type":"function","function":{"name":"f","parameters":{"type":"object","properties":{"n":{"enum":"a"}}}}}]`, userHi),
		"a property of true":      toolBody(`"tools":[{"type":"function","function":{"name":"f","parameters":{"type":"object","properties":{"x":true}}}}]`, userHi),
		"parallel false":          toolBody(`"tools":[`+weatherTool+`],"parallel_tool_calls":false`, userHi),
		"choice none":             toolBody(`"tools":[`+weatherTool+`],"tool_choice":"none"`, userHi),
		"choice auto":             toolBody(`"tools":[`+weatherTool+`],"tool_choice":"auto"`, userHi),
		"choice required":         toolBody(`"tools":[`+weatherTool+`],"tool_choice":"required"`, userHi),
		"choice a function":       toolBody(`"tools":[`+weatherTool+`],"tool_choice":{"type":"function","function":{"name":"get_weather"}}`, userHi),
		"auto without tools":      toolBody(`"tool_choice":"auto"`, userHi),
		"none without tools":      toolBody(`"tool_choice":"none"`, userHi),
		"a null choice":           toolBody(`"tools":[`+weatherTool+`],"tool_choice":null`, userHi),
		"a finished tool round":   toolBody(`"tools":[`+weatherTool+`]`, userHi+`,{"role":"assistant","content":null,"tool_calls":[{"id":"call_1","type":"function","function":{"name":"get_weather","arguments":"{\"city\":\"Paris\"}"}}]},{"role":"tool","tool_call_id":"call_1","content":"21 C"}`),
		"results as text parts":   toolBody(`"tools":[`+weatherTool+`]`, userHi+`,{"role":"assistant","tool_calls":[{"id":"c","type":"function","function":{"name":"f","arguments":"{}"}}]},{"role":"tool","tool_call_id":"c","content":[{"type":"text","text":"a"},{"type":"text","text":"b"}]}`),
		"two results, one call":   toolBody(``, userHi+`,{"role":"assistant","tool_calls":[{"id":"c","type":"function","function":{"name":"f","arguments":""}}]},{"role":"tool","tool_call_id":"c","content":"a"},{"role":"tool","tool_call_id":"c","content":""}`),
		"history without tools":   toolBody(``, userHi+`,{"role":"assistant","tool_calls":[{"id":"c","type":"function","function":{"name":"f","arguments":"{}"}}]},{"role":"tool","tool_call_id":"c","content":"r"}`),
	}
	for name, body := range cases {
		if err := validateChat(decodeRequest(t, body)); err != nil {
			t.Errorf("%s was rejected: %+v", name, err)
		}
	}
}

func TestValidateToolsRejections(t *testing.T) {
	tool := func(function string) string { return `"tools":[{"type":"function","function":` + function + `}]` }
	call := func(c string) string {
		return userHi + `,{"role":"assistant","tool_calls":[` + c + `]},{"role":"tool","tool_call_id":"c","content":"r"}`
	}
	big := strings.Repeat("x", maxToolResult+1)
	var sixtyFive []string
	for i := range 65 {
		sixtyFive = append(sixtyFive, fmt.Sprintf(`{"type":"function","function":{"name":"t%d"}}`, i))
	}
	cases := []struct {
		name, body, code, param string
	}{
		{"65 tools", toolBody(`"tools":[`+strings.Join(sixtyFive, ",")+`]`, userHi), "invalid_value", "tools"},
		{"not an object", toolBody(`"tools":["f"]`, userHi), "invalid_value", "tools[0]"},
		{"a non-function type", toolBody(`"tools":[{"type":"retrieval"}]`, userHi), "unsupported_parameter", "tools[0].type"},
		{"no type", toolBody(`"tools":[{"function":{"name":"f"}}]`, userHi), "unsupported_parameter", "tools[0].type"},
		{"no function", toolBody(`"tools":[{"type":"function"}]`, userHi), "missing_required_parameter", "tools[0].function"},
		{"no name", toolBody(tool(`{"description":"d"}`), userHi), "invalid_value", "tools[0].function.name"},
		{"a name of 55", toolBody(tool(`{"name":"`+strings.Repeat("a", 55)+`"}`), userHi), "invalid_value", "tools[0].function.name"},
		{"a name with a space", toolBody(tool(`{"name":"get weather"}`), userHi), "invalid_value", "tools[0].function.name"},
		{"a name with a dot", toolBody(tool(`{"name":"a.b"}`), userHi), "invalid_value", "tools[0].function.name"},
		{"a name that is not text", toolBody(tool(`{"name":5}`), userHi), "invalid_value", "tools[0]"},
		{"a duplicate name", toolBody(`"tools":[`+weatherTool+`,`+weatherTool+`]`, userHi), "invalid_value", "tools[1].function.name"},
		{"a description too long", toolBody(tool(`{"name":"f","description":"`+strings.Repeat("d", maxToolDescription+1)+`"}`), userHi), "invalid_value", "tools[0].function.description"},
		{"parameters as a string", toolBody(tool(`{"name":"f","parameters":"x"}`), userHi), "invalid_value", "tools[0].function.parameters"},
		{"parameters as an array", toolBody(tool(`{"name":"f","parameters":[]}`), userHi), "invalid_value", "tools[0].function.parameters"},
		{"parameters too large", toolBody(tool(`{"name":"f","parameters":{"type":"object","description":"`+strings.Repeat("p", maxToolSchema)+`"}}`), userHi), "invalid_value", "tools[0].function.parameters"},
		{"parameters of another type", toolBody(tool(`{"name":"f","parameters":{"type":"array"}}`), userHi), "invalid_value", "tools[0].function.parameters.type"},
		{"properties as an array", toolBody(tool(`{"name":"f","parameters":{"type":"object","properties":[]}}`), userHi), "invalid_value", "tools[0].function.parameters.properties"},
		{"a property that is not a schema", toolBody(tool(`{"name":"f","parameters":{"type":"object","properties":{"x":3}}}`), userHi), "invalid_value", "tools[0].function.parameters.properties"},
		{"required as a string", toolBody(tool(`{"name":"f","parameters":{"type":"object","required":"x"}}`), userHi), "invalid_value", "tools[0].function.parameters.required"},
		{"required with a number", toolBody(tool(`{"name":"f","parameters":{"type":"object","required":[1]}}`), userHi), "invalid_value", "tools[0].function.parameters.required"},

		{"an unknown choice", toolBody(`"tools":[`+weatherTool+`],"tool_choice":"sometimes"`, userHi), "invalid_value", "tool_choice"},
		{"a choice of a number", toolBody(`"tools":[`+weatherTool+`],"tool_choice":3`, userHi), "invalid_value", "tool_choice"},
		{"a choice of an undeclared function", toolBody(`"tools":[`+weatherTool+`],"tool_choice":{"type":"function","function":{"name":"other"}}`, userHi), "invalid_value", "tool_choice.function.name"},
		{"a choice of another type", toolBody(`"tools":[`+weatherTool+`],"tool_choice":{"type":"allowed_tools"}`, userHi), "unsupported_parameter", "tool_choice.type"},
		{"required without tools", toolBody(`"tool_choice":"required"`, userHi), "unsupported_parameter", "tool_choice"},
		{"a named function without tools", toolBody(`"tool_choice":{"type":"function","function":{"name":"f"}}`, userHi), "unsupported_parameter", "tool_choice"},
		{"legacy functions", toolBody(`"functions":[{"name":"f"}]`, userHi), "unsupported_parameter", "functions"},
		{"legacy function_call", toolBody(`"function_call":"auto"`, userHi), "unsupported_parameter", "function_call"},
		{"the function role", toolBody(``, userHi+`,{"role":"function","name":"f","content":"x"}`), "unsupported_parameter", "messages[1].role"},

		{"a result without an id", toolBody(``, userHi+`,{"role":"tool","content":"x"}`), "missing_required_parameter", "messages[1].tool_call_id"},
		{"a result of no call", toolBody(``, userHi+`,{"role":"tool","tool_call_id":"nope","content":"x"}`), "invalid_value", "messages[1].tool_call_id"},
		{"a result before its call", toolBody(``, userHi+`,{"role":"tool","tool_call_id":"c","content":"x"},{"role":"assistant","tool_calls":[{"id":"c","type":"function","function":{"name":"f","arguments":"{}"}}]}`), "invalid_value", "messages[1].tool_call_id"},
		{"a result too large", toolBody(``, userHi+`,{"role":"assistant","tool_calls":[{"id":"c","type":"function","function":{"name":"f","arguments":"{}"}}]},{"role":"tool","tool_call_id":"c","content":"`+big+`"}`), "invalid_value", "messages[2].content"},
		{"an image in a result", toolBody(``, userHi+`,{"role":"assistant","tool_calls":[{"id":"c","type":"function","function":{"name":"f","arguments":"{}"}}]},{"role":"tool","tool_call_id":"c","content":[{"type":"image_url","image_url":{"url":"u"}}]}`), "unsupported_parameter", "messages[2].content[0].type"},
		{"a call without an id", toolBody(``, call(`{"type":"function","function":{"name":"f","arguments":"{}"}}`)), "invalid_value", "messages[1].tool_calls[0].id"},
		{"a call with a long id", toolBody(``, call(`{"id":"`+strings.Repeat("i", maxCallID+1)+`","type":"function","function":{"name":"f","arguments":"{}"}}`)), "invalid_value", "messages[1].tool_calls[0].id"},
		{"a call of another type", toolBody(``, call(`{"id":"c","type":"custom","function":{"name":"f","arguments":"{}"}}`)), "unsupported_parameter", "messages[1].tool_calls[0].type"},
		{"a call without a name", toolBody(``, call(`{"id":"c","type":"function","function":{"arguments":"{}"}}`)), "invalid_value", "messages[1].tool_calls[0].function.name"},
		{"arguments as an object", toolBody(``, call(`{"id":"c","type":"function","function":{"name":"f","arguments":{"a":1}}}`)), "invalid_value", "messages[1].tool_calls[0].function.arguments"},
		{"arguments too long", toolBody(``, call(`{"id":"c","type":"function","function":{"name":"f","arguments":"`+strings.Repeat("a", maxCallArguments+1)+`"}}`)), "invalid_value", "messages[1].tool_calls[0].function.arguments"},
	}
	for _, c := range cases {
		err := validateChat(decodeRequest(t, c.body))
		if err == nil || err.Code != c.code || err.Param != c.param || err.Status != 400 {
			t.Errorf("%s: got %+v, want 400 %s on %s", c.name, err, c.code, c.param)
		}
	}
}

// What a request names stays out of the error: a message that quoted a tool name
// could carry whatever the client wrote into a log or a terminal.
func TestValidateToolsErrorsEchoNothingTheClientWrote(t *testing.T) {
	const marker = "MARKER-xyz"
	bodies := []string{
		toolBody(`"tools":[{"type":"function","function":{"name":"bad `+marker+`"}}]`, userHi),
		toolBody(`"tools":[{"type":"function","function":{"name":"`+marker+`"}},{"type":"function","function":{"name":"`+marker+`"}}]`, userHi),
		toolBody(`"tools":[`+weatherTool+`],"tool_choice":{"type":"function","function":{"name":"`+marker+`"}}`, userHi),
		toolBody(``, userHi+`,{"role":"tool","tool_call_id":"`+marker+`","content":"x"}`),
		toolBody(``, userHi+`,{"role":"assistant","tool_calls":[{"id":"c","type":"`+marker+`","function":{"name":"f","arguments":"{}"}}]}`),
		toolBody(`"tools":[{"type":"function","function":{"name":"f","parameters":{"type":"object","properties":{"`+marker+`":3}}}}]`, userHi),
	}
	for _, body := range bodies {
		err := validateChat(decodeRequest(t, body))
		if err == nil {
			t.Errorf("%s was accepted", body)
			continue
		}
		if b := string(err.body()); strings.Contains(b, marker) {
			t.Errorf("the error echoes what the client wrote: %s", b)
		}
	}
}

func TestValidateToolsRecordsWhatItParsed(t *testing.T) {
	req := decodeRequest(t, toolBody(`"tools":[`+weatherTool+`,{"type":"function","function":{"name":"plain"}}],"tool_choice":{"type":"function","function":{"name":"plain"}}`, userHi))
	if err := validateChat(req); err != nil {
		t.Fatal(err)
	}
	if len(req.toolDecls) != 2 || req.toolDecls[0].Name != "get_weather" || req.toolDecls[1].Name != "plain" {
		t.Fatalf("declarations: %+v", req.toolDecls)
	}
	w := req.toolDecls[0]
	if w.Description != "Get the weather." || len(w.Props) != 1 || len(w.Required) != 1 || w.Required[0] != "city" {
		t.Errorf("the weather tool: %+v", w)
	}
	if string(w.Params) != `{"type":"object","properties":{"city":{"type":"string","description":"A city."}},"required":["city"]}` {
		t.Errorf("the schema is kept whole and compact: %s", w.Params)
	}
	if req.toolDecls[1].Params != nil {
		t.Errorf("a tool without parameters has no schema: %s", req.toolDecls[1].Params)
	}
	if req.toolPick.Mode != choiceFunction || req.toolPick.Name != "plain" {
		t.Errorf("choice: %+v", req.toolPick)
	}
	if !req.toolsActive() {
		t.Error("declared tools are active")
	}
}

func TestToolsActiveAndToolHistory(t *testing.T) {
	cases := []struct {
		name, body      string
		active, history bool
	}{
		{"no tools", toolBody(``, userHi), false, false},
		{"empty tools", toolBody(`"tools":[]`, userHi), false, false},
		{"tools", toolBody(`"tools":[`+weatherTool+`]`, userHi), true, false},
		{"tools but none chosen", toolBody(`"tools":[`+weatherTool+`],"tool_choice":"none"`, userHi), false, false},
		{"history only", toolBody(``, userHi+`,{"role":"assistant","tool_calls":[{"id":"c","type":"function","function":{"name":"f","arguments":"{}"}}]}`), false, true},
		{"tools and history", toolBody(`"tools":[`+weatherTool+`]`, userHi+`,{"role":"assistant","tool_calls":[{"id":"c","type":"function","function":{"name":"f","arguments":"{}"}}]},{"role":"tool","tool_call_id":"c","content":"r"}`), true, true},
	}
	for _, c := range cases {
		req := decodeRequest(t, c.body)
		if err := validateChat(req); err != nil {
			t.Fatalf("%s: %+v", c.name, err)
		}
		if req.toolsActive() != c.active || req.hasToolHistory() != c.history {
			t.Errorf("%s: active=%v history=%v, want %v %v", c.name, req.toolsActive(), req.hasToolHistory(), c.active, c.history)
		}
	}
}

// A schema is data the client wrote: whatever it holds must not break the
// decoding or the validation.
func TestValidateToolsSurvivesOddSchemas(t *testing.T) {
	for _, params := range []string{`{}`, `null`, `{"type":"object","properties":null}`, `{"anyOf":[{"type":"string"}]}`, `{"$ref":"#/$defs/x"}`} {
		body := toolBody(`"tools":[{"type":"function","function":{"name":"f","parameters":`+params+`}}]`, userHi)
		var probe map[string]any
		if err := json.Unmarshal([]byte(body), &probe); err != nil {
			t.Fatal(err)
		}
		_ = validateChat(decodeRequest(t, body)) // may accept or refuse, never panic
	}
}
