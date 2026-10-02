package openaiapi

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
)

// Validation of what a request says about tools: the ones it declares, the
// choice it makes and the calls and results its messages carry. A message names a
// parameter, never what the client wrote: a tool name, an argument or a result
// stays out of an error.

// validateTools checks tools and tool_choice, and keeps what it parsed in req.
func validateTools(req *ChatRequest) *apiError {
	if len(req.Tools) > maxTools {
		return errInvalid("invalid_value", "tools", fmt.Sprintf("at most %d tools are supported", maxTools))
	}
	decls := make([]toolDecl, 0, len(req.Tools))
	declared := make(map[string]bool, len(req.Tools))
	known := make(map[string]bool, len(req.Tools)) // the names the runtime knows the functions by
	var wire, back map[string]string
	budget := newHoistBudget() // what the functions of the request together may spend on naming their arguments
	for i, raw := range req.Tools {
		d, e := parseToolDecl(i, raw, budget)
		if e != nil {
			return e
		}
		if declared[d.Name] {
			return errInvalid("invalid_value", fmt.Sprintf("tools[%d].function.name", i), "tool names must be unique")
		}
		if known[d.Wire] { // an alias that is the name of another function, or of another alias
			return errInvalid("invalid_value", fmt.Sprintf("tools[%d].function.name", i), "a function name collides with the name another function is known by: rename one of them")
		}
		declared[d.Name], known[d.Wire] = true, true
		if d.Wire != d.Name {
			if wire == nil {
				wire, back = map[string]string{}, map[string]string{}
			}
			wire[d.Name], back[d.Wire] = d.Wire, d.Name
		}
		decls = append(decls, d)
	}
	pick, e := parseToolChoice(req.ToolChoice, declared)
	if e != nil {
		return e
	}
	req.toolDecls, req.toolPick = decls, pick
	req.toolWire, req.toolDeclared = wire, back
	return nil
}

func parseToolDecl(i int, raw json.RawMessage, budget *hoistBudget) (toolDecl, *apiError) {
	base := fmt.Sprintf("tools[%d]", i)
	var t struct {
		Type     string `json:"type"`
		Function *struct {
			Name        string          `json:"name"`
			Description string          `json:"description"`
			Parameters  json.RawMessage `json:"parameters"`
		} `json:"function"`
	}
	if err := json.Unmarshal(raw, &t); err != nil {
		return toolDecl{}, errInvalid("invalid_value", base, "a tool must be an object with a type and a function")
	}
	switch {
	case t.Type != "function":
		return toolDecl{}, errUnsupported(base+".type", "only tools of type function are supported")
	case t.Function == nil:
		return toolDecl{}, errInvalid("missing_required_parameter", base+".function", "a tool needs a function")
	case !toolNameRE.MatchString(t.Function.Name):
		return toolDecl{}, errInvalid("invalid_value", base+".function.name",
			fmt.Sprintf("a function name must match %s", toolNameRE))
	case len(t.Function.Description) > maxToolDescription:
		return toolDecl{}, errInvalid("invalid_value", base+".function.description",
			fmt.Sprintf("a description may have at most %d bytes", maxToolDescription))
	}
	d := toolDecl{Name: t.Function.Name, Wire: toolAlias(t.Function.Name), Description: t.Function.Description}
	param := base + ".function.parameters"
	var e *apiError
	if d.Params, e = inspectParams(param, t.Function.Parameters); e != nil {
		return toolDecl{}, e
	}
	names := nameArguments(d.Params, budget)
	switch {
	case names.Spent:
		return toolDecl{}, errInvalid("invalid_value", param,
			"the parameters of the functions of the request together hold more schemas and properties than are read to name their arguments: list the arguments in properties, or declare fewer functions with such schemas")
	case names.Overrun:
		return toolDecl{}, errInvalid("invalid_value", param,
			"the parameters nest anyOf, oneOf, allOf, if, then, else or $ref too deeply to name their arguments: list the arguments in properties")
	case names.Open && len(names.Props) == 0:
		return toolDecl{}, errInvalid("invalid_value", param,
			"the parameters name no property but allow other keys, so no argument of a call could be passed on: list the arguments in properties")
	}
	d.Props, d.Required = names.Props, names.Required
	return d, nil
}

// inspectParams checks a parameters schema and returns it compact. An absent or
// null schema gives nothing. Only what would break the tool is refused: a type
// other than object, a top-level property that is not a schema and a required
// list that is not strings. Nested properties are not looked at: monomind drops
// them (nameArguments says which arguments it is told of). An enum that monomind
// cannot take is not refused either: toolSpecs leaves it out of what monomind gets.
func inspectParams(param string, raw json.RawMessage) (json.RawMessage, *apiError) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}
	if len(raw) > maxToolSchema {
		return nil, errInvalid("invalid_value", param, fmt.Sprintf("a parameters schema may have at most %d bytes", maxToolSchema))
	}
	var top map[string]json.RawMessage
	if raw[0] != '{' || json.Unmarshal(raw, &top) != nil {
		return nil, errInvalid("invalid_value", param, "parameters must be a JSON schema object")
	}
	if v, ok := top["type"]; ok && string(bytes.TrimSpace(v)) != `"object"` {
		return nil, errInvalid("invalid_value", param+".type", "the type of the parameters must be object")
	}
	if v, ok := top["properties"]; ok && string(bytes.TrimSpace(v)) != "null" {
		var props map[string]json.RawMessage
		if err := json.Unmarshal(v, &props); err != nil {
			return nil, errInvalid("invalid_value", param+".properties", "properties must be an object")
		}
		for _, p := range props {
			if e := inspectProperty(param+".properties", p); e != nil {
				return nil, e
			}
		}
	}
	if v, ok := top["required"]; ok && string(bytes.TrimSpace(v)) != "null" {
		var required []string
		if err := json.Unmarshal(v, &required); err != nil {
			return nil, errInvalid("invalid_value", param+".required", "required must be a list of strings")
		}
	}
	var buf bytes.Buffer
	if err := json.Compact(&buf, raw); err != nil {
		return nil, errInvalid("invalid_value", param, "parameters must be a JSON schema object")
	}
	return buf.Bytes(), nil
}

// inspectProperty checks one top-level property: a schema object, or a boolean. The
// error names the list of properties, not the property: its name is the client's.
func inspectProperty(param string, raw json.RawMessage) *apiError {
	raw = bytes.TrimSpace(raw)
	switch string(raw) {
	case "true", "false":
		return nil
	}
	var p map[string]json.RawMessage
	if len(raw) == 0 || raw[0] != '{' || json.Unmarshal(raw, &p) != nil {
		return errInvalid("invalid_value", param, "a property must be a schema object")
	}
	return nil
}

// parseToolChoice reads tool_choice. declared holds the names of the declared
// tools: a choice that forces a call needs one.
func parseToolChoice(raw json.RawMessage, declared map[string]bool) (toolChoice, *apiError) {
	if !present(raw) {
		return toolChoice{Mode: choiceAuto}, nil
	}
	raw = bytes.TrimSpace(raw)
	needTools := errUnsupported("tool_choice", "tool_choice needs tools: declare them with tools")
	switch raw[0] {
	case '"':
		var s string
		_ = json.Unmarshal(raw, &s)
		switch s {
		case choiceAuto, choiceNone:
			return toolChoice{Mode: s}, nil
		case choiceRequired:
			if len(declared) == 0 {
				return toolChoice{}, needTools
			}
			return toolChoice{Mode: choiceRequired}, nil
		}
	case '{':
		var c struct {
			Type     string `json:"type"`
			Function *struct {
				Name string `json:"name"`
			} `json:"function"`
		}
		if json.Unmarshal(raw, &c) != nil {
			break
		}
		switch {
		case c.Type != "function":
			return toolChoice{}, errUnsupported("tool_choice.type", "only a tool_choice of type function is supported")
		case c.Function == nil || c.Function.Name == "":
			return toolChoice{}, errInvalid("missing_required_parameter", "tool_choice.function.name", "tool_choice must name a function")
		case len(declared) == 0:
			return toolChoice{}, needTools
		case !declared[c.Function.Name]:
			return toolChoice{}, errInvalid("invalid_value", "tool_choice.function.name", "tool_choice names a function that is not in tools")
		}
		return toolChoice{Mode: choiceFunction, Name: c.Function.Name}, nil
	}
	return toolChoice{}, errInvalid("invalid_value", "tool_choice", "tool_choice must be none, auto, required or a function")
}

// validateToolMessages checks the calls the assistant messages of the
// conversation made and the results its tool messages give: every result answers
// a call an earlier message made.
func validateToolMessages(req *ChatRequest) *apiError {
	known := map[string]bool{}
	for i, m := range req.Messages {
		param := fmt.Sprintf("messages[%d]", i)
		switch m.Role {
		case "assistant":
			if len(m.ToolCalls) > maxHistoryCalls {
				return errInvalid("invalid_value", param+".tool_calls", fmt.Sprintf("at most %d tool calls per message are supported", maxHistoryCalls))
			}
			for j, c := range m.ToolCalls {
				if e := validateHistoryCall(fmt.Sprintf("%s.tool_calls[%d]", param, j), c); e != nil {
					return e
				}
				known[c.ID] = true
			}
		case "tool":
			switch {
			case m.ToolCallID == "":
				return errInvalid("missing_required_parameter", param+".tool_call_id", "a tool message must say which call it answers")
			case !known[m.ToolCallID]:
				return errInvalid("invalid_value", param+".tool_call_id", "a tool message must answer a tool call of an assistant message before it")
			case len(m.Content.Text) > maxToolResult:
				return errInvalid("invalid_value", param+".content", fmt.Sprintf("a tool result may have at most %d bytes", maxToolResult))
			}
		}
	}
	return nil
}

// isCallToken reports whether s can be the id or the name of a tool call of the conversation:
// 1 to limit printable ASCII characters (every id and name real clients send is, such as
// call_abc123, toolu_01A09q90qw90lq917835lq9 and functions.get_weather:0), none of the
// characters that the transcript gives a meaning to or that could end a line or
// a tag: no space, no control character, and none of [ ] < > & ' " or a backtick. What it
// allows is a list, not what it refuses: a character that renders as nothing, or as a
// letter of another width, is not one of the 94.
func isCallToken(s string, limit int) bool {
	if s == "" || len(s) > limit {
		return false
	}
	for i := 0; i < len(s); i++ {
		if c := s[i]; c < 0x21 || c > 0x7e || strings.IndexByte("[]<>&'\"`", c) >= 0 {
			return false
		}
	}
	return true
}

func validateHistoryCall(param string, c ToolCall) *apiError {
	const tokenRule = "of 1 to %d printable ASCII characters, none of [ ] < > & ' \" or a backtick"
	switch {
	case !isCallToken(c.ID, maxCallID):
		return errInvalid("invalid_value", param+".id", fmt.Sprintf("a tool call needs an id "+tokenRule, maxCallID))
	case c.Type != "" && c.Type != "function":
		return errUnsupported(param+".type", "only tool calls of type function are supported")
	case !isCallToken(c.Function.Name, maxCallName):
		return errInvalid("invalid_value", param+".function.name", fmt.Sprintf("a tool call needs a function name "+tokenRule, maxCallName))
	}
	if args := bytes.TrimSpace(c.Function.Arguments); len(args) > 0 && string(args) != "null" {
		if args[0] != '"' {
			return errInvalid("invalid_value", param+".function.arguments", "arguments must be a string of JSON")
		}
	}
	return nil
}
