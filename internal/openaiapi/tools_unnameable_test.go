package openaiapi

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
)

// A function whose arguments cannot be named for monomind is refused so that no call reaches the
// client as {}: that protects a request that gives the tools to the model, and no other. A request
// that passes none (tool_choice none) is served as a plain conversation, whatever its tools say,
// and the refusal says which bound a schema went past.

// freeFormTool takes any keys and names none: monomind could be told of no argument of it.
const freeFormTool = `{"type":"function","function":{"name":"run_query","description":"Runs a query.","parameters":{"type":"object","additionalProperties":true}}}`

func TestAFreeFormFunctionIsRefusedWhileTheToolsAreActive(t *testing.T) {
	for name, choice := range map[string]string{
		"no tool_choice":         "",
		"auto":                   `,"tool_choice":"auto"`,
		"required":               `,"tool_choice":"required"`,
		"another function named": `,"tool_choice":{"type":"function","function":{"name":"get_weather"}}`,
		"the function named":     `,"tool_choice":{"type":"function","function":{"name":"run_query"}}`,
	} {
		err := validateChat(decodeRequest(t, toolBody(`"tools":[`+weatherTool+`,`+freeFormTool+`]`+choice, userHi)))
		if err == nil || err.Status != http.StatusBadRequest || err.Code != "invalid_value" || err.Param != "tools[1].function.parameters" {
			t.Errorf("%s: got %+v, want 400 invalid_value on tools[1].function.parameters: it is not dropped without a word", name, err)
			continue
		}
		if b := string(err.body()); strings.Contains(b, "run_query") {
			t.Errorf("%s: the error echoes the function: %s", name, b)
		}
	}
}

func TestAFreeFormFunctionDoesNotRefuseARequestThatPassesNoTools(t *testing.T) {
	req := decodeRequest(t, toolBody(`"tools":[`+weatherTool+`,`+freeFormTool+`],"tool_choice":"none"`, userHi))
	if err := validateChat(req); err != nil {
		t.Fatalf("tool_choice none: %+v", err)
	}
	if req.toolsActive() || len(req.toolDecls) != 2 {
		t.Errorf("both functions are declared and none is passed: active %v, %d declared", req.toolsActive(), len(req.toolDecls))
	}

	// Served: the turn is a plain one, with the history of an earlier round as text.
	script := &execScript{turns: []execFunc{answers("It was 21 C."), answers("Fine.")}}
	h := toolHarness(t, script.exec)
	secret := h.key(t, "default", "app", false)
	both := weatherTools[:len(weatherTools)-1] + `,` + freeFormTool + `]`
	body := strings.Replace(followUp("call_1", "21 C"), weatherTools, both+`,"tool_choice":"none"`, 1)
	if rec := post(h, anyPolicy, secret, body); rec.Code != http.StatusOK {
		t.Fatalf("a conversation with tool history under tool_choice none: %d %s", rec.Code, rec.Body)
	}
	opts := script.calls()[0]
	if len(opts.Tools) != 0 || opts.OnToolCall != nil || !strings.Contains(opts.Prompt, "[tool get_weather (call_1)]") {
		t.Errorf("a plain turn that renders the history: tools %d, prompt %q", len(opts.Tools), opts.Prompt)
	}
	if rec := post(h, anyPolicy, secret, toolChatBody("claude", both+`,"tool_choice":"none"`, weatherQuestion)); rec.Code != http.StatusOK {
		t.Errorf("a first request under tool_choice none: %d %s", rec.Code, rec.Body)
	}

	// The same request with the tools active is the refusal.
	rec := post(h, anyPolicy, secret, strings.Replace(body, `,"tool_choice":"none"`, "", 1))
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "tools[1].function.parameters") {
		t.Errorf("the same tools with the choice left to the model: %d %s", rec.Code, rec.Body)
	}
}

// Nothing is read of the schemas of a request that passes no tools, so what they refer to costs it
// nothing either: more than the request-wide budget of schemas is no reason to refuse it.
func TestNoSchemaIsReadForARequestThatPassesNoTools(t *testing.T) {
	properties := make([]string, 0, 7000)
	for i := range 7000 {
		properties = append(properties, fmt.Sprintf(`"%s":{}`, base36(i)))
	}
	dense := `{"type":"object","properties":{` + strings.Join(properties, ",") + `}}`
	tools := make([]string, 0, 16)
	for i := range 16 { // 112,000 properties: more than a request may spend
		tools = append(tools, fmt.Sprintf(`{"type":"function","function":{"name":"g%d","parameters":%s}}`, i, dense))
	}
	list := `"tools":[` + strings.Join(tools, ",") + `]`
	if err := validateChat(decodeRequest(t, toolBody(list+`,"tool_choice":"none"`, userHi))); err != nil {
		t.Errorf("tool_choice none: %+v", err)
	}
	if err := validateChat(decodeRequest(t, toolBody(list, userHi))); err == nil || !strings.Contains(err.Message, "together") {
		t.Errorf("the same tools with the choice left to the model: %+v", err)
	}
}

// The refusal says which bound a schema went past: how deep it nests, or how many schemas it holds.
func TestTheRefusalOfASchemaPastWhatIsReadSaysWhichBound(t *testing.T) {
	deep := strings.Repeat(`{"anyOf":[`, maxHoistDepth+1) + `{"properties":{"p":{"type":"string"}}}` + strings.Repeat(`]}`, maxHoistDepth+1)
	wide := `{"anyOf":[{"properties":{"p":{"type":"string"}}}` + strings.Repeat(`,{}`, maxHoistNodes) + `]}`
	for name, c := range map[string]struct{ params, says, notSays string }{
		"too deep": {deep, fmt.Sprintf("more than %d levels deep", maxHoistDepth), "schemas that"},
		"too wide": {wide, fmt.Sprintf("more than %d schemas", maxHoistNodes), "levels deep"},
	} {
		err := validateChat(decodeRequest(t, oneTool(c.params)))
		if err == nil || err.Param != "tools[0].function.parameters" || !strings.Contains(err.Message, c.says) || strings.Contains(err.Message, c.notSays) {
			t.Errorf("%s: %+v, want a refusal on the parameters that says %q and not %q", name, err, c.says, c.notSays)
		}
	}
}
