package openaiapi

import (
	"encoding/json"
	"reflect"
	"sort"
	"strings"
	"testing"
)

// toolDecls validates a request body and returns the declarations it holds.
func toolDecls(t *testing.T, tools string) []toolDecl {
	t.Helper()
	req := decodeRequest(t, toolBody(`"tools":[`+tools+`]`, userHi))
	if err := validateChat(req); err != nil {
		t.Fatalf("the test tools are not valid: %+v", err)
	}
	return req.toolDecls
}

func TestToolSpecsKeepTheFlatSchemaAndFoldTheWholeOneIntoTheDescription(t *testing.T) {
	const params = `{"type":"object","properties":{"group":{"type":"string","description":"Lowercase letters only."},"contacts":{"type":"array","items":{"type":"object","properties":{"email":{"type":"string"}},"required":["email"]}},"mode":{"type":"string","enum":["a","b"]}},"required":["group","contacts"]}`
	decls := toolDecls(t, `{"type":"function","function":{"name":"add_contacts","description":"Add contacts.","parameters":`+params+`}}`)
	specs := toolSpecs(decls)
	if len(specs) != 1 {
		t.Fatalf("specs: %+v", specs)
	}
	s := specs[0]
	if s.Name != "add_contacts" {
		t.Errorf("name = %q", s.Name)
	}
	// Everything monomind drops (a property's description, the items of an array)
	// is still in front of the model, in the description.
	if want := "Add contacts.\n\nParameters (JSON Schema):\n" + params; s.Description != want {
		t.Errorf("description:\n%s\nwant:\n%s", s.Description, want)
	}
	if s.Schema["type"] != "object" {
		t.Errorf("schema type = %v", s.Schema["type"])
	}
	props, _ := s.Schema["properties"].(map[string]interface{})
	var names []string
	for k := range props {
		names = append(names, k)
	}
	sort.Strings(names)
	if !reflect.DeepEqual(names, []string{"contacts", "group", "mode"}) {
		t.Errorf("top-level properties = %v", names)
	}
	if mode, _ := props["mode"].(map[string]interface{}); !reflect.DeepEqual(mode["enum"], []interface{}{"a", "b"}) {
		t.Errorf("a string enum must reach monomind: %v", props["mode"])
	}
	if !reflect.DeepEqual(s.Schema["required"], []string{"group", "contacts"}) {
		t.Errorf("required = %v", s.Schema["required"])
	}
	// What goes to monomind is plain JSON.
	if _, err := json.Marshal(specs); err != nil {
		t.Errorf("the specs do not marshal: %v", err)
	}
}

func TestToolSpecsOfToolsWithLittleToFold(t *testing.T) {
	decls := toolDecls(t, strings.Join([]string{
		`{"type":"function","function":{"name":"bare"}}`,
		`{"type":"function","function":{"name":"described","description":"Does a thing."}}`,
		`{"type":"function","function":{"name":"noprops","description":"No arguments.","parameters":{"type":"object","properties":{}}}}`,
		`{"type":"function","function":{"name":"schemaonly","parameters":{"type":"object","properties":{"x":{"type":"integer"}}}}}`,
		`{"type":"function","function":{"name":"boolprop","parameters":{"type":"object","properties":{"x":true}}}}`,
		`{"type":"function","function":{"name":"emptyobject","description":"Takes nothing.","parameters":{"type":"object"}}}`,
		`{"type":"function","function":{"name":"closed","description":"Takes nothing, closed.","parameters":{"$schema":"http://json-schema.org/draft-07/schema#","type":"object","properties":{},"required":[],"additionalProperties":false}}}`,
	}, ","))
	specs := toolSpecs(decls)
	byName := map[string]string{}
	for _, s := range specs {
		byName[s.Name] = s.Description
	}
	if byName["bare"] != "" {
		t.Errorf("a tool with nothing to say has no description: %q", byName["bare"])
	}
	if byName["described"] != "Does a thing." || byName["noprops"] != "No arguments." || byName["emptyobject"] != "Takes nothing." || byName["closed"] != "Takes nothing, closed." {
		t.Errorf("a tool whose schema says nothing has nothing to fold: %q", byName)
	}
	if want := "Parameters (JSON Schema):\n" + `{"type":"object","properties":{"x":{"type":"integer"}}}`; byName["schemaonly"] != want {
		t.Errorf("a schema without a description: %q", byName["schemaonly"])
	}
	for _, s := range specs {
		if s.Name == "bare" {
			if _, hasProps := s.Schema["properties"]; !hasProps {
				t.Errorf("a schema always has properties, even none: %v", s.Schema)
			}
			if _, hasReq := s.Schema["required"]; hasReq {
				t.Errorf("no required list when there is none: %v", s.Schema)
			}
		}
		if s.Name == "boolprop" {
			if p, _ := s.Schema["properties"].(map[string]interface{}); p["x"] != true {
				t.Errorf("a boolean property is passed as it is: %v", s.Schema)
			}
		}
	}
}

// A schema without top-level properties still tells the model how to call: it is
// all in the description, which is the only place monomind does not shrink.
func TestToolSpecsFoldASchemaThatHasNoTopLevelProperties(t *testing.T) {
	for name, params := range map[string]string{
		"a root anyOf":         `{"anyOf":[{"type":"object","properties":{"a":{"type":"string"}},"required":["a"]},{"type":"object","properties":{"b":{"type":"integer"}},"required":["b"]}]}`,
		"free-form properties": `{"type":"object","additionalProperties":{"type":"string"}}`,
		"a reference":          `{"$ref":"#/$defs/Args","$defs":{"Args":{"type":"object","properties":{"city":{"type":"string"}},"required":["city"]}}}`,
		"a description only":   `{"type":"object","description":"Pass the city as city."}`,
		"pattern properties":   `{"type":"object","patternProperties":{"^x-":{"type":"string"}},"properties":{}}`,
		"required only":        `{"type":"object","required":["city"]}`,
		"properties as null":   `{"type":"object","properties":null,"oneOf":[{"required":["a"]}]}`,
	} {
		specs := toolSpecs(toolDecls(t, `{"type":"function","function":{"name":"f","description":"Does it.","parameters":`+params+`}}`))
		if want := "Does it.\n\nParameters (JSON Schema):\n" + params; specs[0].Description != want {
			t.Errorf("%s: description %q, want %q", name, specs[0].Description, want)
		}
	}
}

// monomind's tool bridge takes an enum of strings only and rejects every call of a
// tool with another: the flat copy leaves such an enum out, and the model still reads
// it in the description, where the whole schema is.
func TestToolSpecsLeaveAnEnumMonomindCannotTakeOutOfTheFlatCopyOnly(t *testing.T) {
	const params = `{"type":"object","properties":{"n":{"type":"integer","enum":[1,2,3]},"m":{"enum":["a",2,null]},"e":{"type":"string","enum":[]},"x":{"enum":"a"},"ok":{"type":"string","enum":["a","b"]},"plain":{"type":"string"}}}`
	specs := toolSpecs(toolDecls(t, `{"type":"function","function":{"name":"f","parameters":`+params+`}}`))
	props, _ := specs[0].Schema["properties"].(map[string]interface{})
	for _, name := range []string{"n", "m", "e", "x"} {
		p, _ := props[name].(map[string]interface{})
		if _, has := p["enum"]; has {
			t.Errorf("property %s: monomind would reject every call of the tool: %v", name, p)
		}
	}
	if p, _ := props["n"].(map[string]interface{}); p["type"] != "integer" {
		t.Errorf("only the enum is left out: %v", props["n"])
	}
	if p, _ := props["ok"].(map[string]interface{}); !reflect.DeepEqual(p["enum"], []interface{}{"a", "b"}) {
		t.Errorf("a string enum must reach monomind: %v", props["ok"])
	}
	if !strings.Contains(specs[0].Description, `"n":{"type":"integer","enum":[1,2,3]}`) || !strings.HasSuffix(specs[0].Description, params) {
		t.Errorf("the model must still read every enum: %q", specs[0].Description)
	}
}

func TestToolSpecsOrderFollowsTheRequest(t *testing.T) {
	decls := toolDecls(t, `{"type":"function","function":{"name":"b"}},{"type":"function","function":{"name":"a"}}`)
	specs := toolSpecs(decls)
	if specs[0].Name != "b" || specs[1].Name != "a" {
		t.Errorf("order: %v %v", specs[0].Name, specs[1].Name)
	}
}

func TestToolsHashTellsTheSameDeclarationsFromOthers(t *testing.T) {
	a := toolDecls(t, weatherTool)
	b := toolDecls(t, weatherTool)
	other := toolDecls(t, strings.Replace(weatherTool, "Get the weather.", "Get the weather today.", 1))
	reordered := toolDecls(t, `{"type":"function","function":{"name":"x"}},`+weatherTool)
	reordered2 := toolDecls(t, weatherTool+`,{"type":"function","function":{"name":"x"}}`)
	if toolsHash(a) != toolsHash(b) {
		t.Error("the same declarations hash differently")
	}
	if toolsHash(a) == toolsHash(other) {
		t.Error("a changed description must change the hash")
	}
	if toolsHash(reordered) != toolsHash(reordered2) {
		t.Error("the order of the tools does not matter")
	}
	if toolsHash(nil) != toolsHash(nil) || toolsHash(nil) == toolsHash(a) {
		t.Error("no tools is a hash of its own")
	}
}

func TestArgsMatchChecksArgumentsAgainstTheDeclaredSchema(t *testing.T) {
	const schema = `{"type":"object","properties":{
	  "city":{"type":"string","minLength":2},
	  "days":{"type":"integer","minimum":1,"maximum":7},
	  "units":{"type":"string","enum":["c","f"]},
	  "tags":{"type":"array","items":{"type":"string"},"maxItems":2},
	  "geo":{"type":"object","properties":{"lat":{"type":"number"}},"required":["lat"],"additionalProperties":false},
	  "id":{"anyOf":[{"type":"string"},{"type":"integer"}]},
	  "note":{"type":["string","null"]}},
	  "required":["city"]}`
	d := toolDecls(t, `{"type":"function","function":{"name":"f","parameters":`+schema+`}}`)[0]
	cases := []struct {
		name, args string
		ok         bool
	}{
		{"the required property only", `{"city":"Paris"}`, true},
		{"everything", `{"city":"Paris","days":3,"units":"c","tags":["a"],"geo":{"lat":1.5},"id":7,"note":null}`, true},
		{"a missing required property", `{"days":3}`, false},
		{"the wrong type", `{"city":5}`, false},
		{"too short", `{"city":"P"}`, false},
		{"a float for an integer", `{"city":"Paris","days":2.5}`, false},
		{"below the minimum", `{"city":"Paris","days":0}`, false},
		{"above the maximum", `{"city":"Paris","days":8}`, false},
		{"outside the enum", `{"city":"Paris","units":"k"}`, false},
		{"too many items", `{"city":"Paris","tags":["a","b","c"]}`, false},
		{"an item of the wrong type", `{"city":"Paris","tags":[1]}`, false},
		{"a nested required property", `{"city":"Paris","geo":{}}`, false},
		{"a nested extra property that is refused", `{"city":"Paris","geo":{"lat":1,"lon":2}}`, false},
		{"anyOf, the first branch", `{"city":"Paris","id":"x"}`, true},
		{"anyOf, no branch", `{"city":"Paris","id":true}`, false},
		{"a nullable type", `{"city":"Paris","note":"n"}`, true},
		{"an unknown property is allowed by default", `{"city":"Paris","extra":1}`, true},
		{"not an object", `["Paris"]`, false},
		{"not JSON", `{city`, false},
		{"empty", ``, false},
	}
	for _, c := range cases {
		if got := argsMatch(d, json.RawMessage(c.args)); got != c.ok {
			t.Errorf("%s: argsMatch = %v, want %v", c.name, got, c.ok)
		}
	}
}

func TestArgsMatchWithoutASchemaTakesAnyObject(t *testing.T) {
	d := toolDecls(t, `{"type":"function","function":{"name":"f"}}`)[0]
	for args, want := range map[string]bool{`{}`: true, `{"a":1}`: true, `[]`: false, `"x"`: false, ``: false} {
		if got := argsMatch(d, json.RawMessage(args)); got != want {
			t.Errorf("%q: %v, want %v", args, got, want)
		}
	}
}

func TestArgsMatchSurvivesSchemasItDoesNotUnderstand(t *testing.T) {
	for _, params := range []string{
		`{"type":"object","properties":{"x":{"$ref":"#/$defs/X"}},"$defs":{"X":{"type":"string"}}}`,
		`{"type":"object","properties":{"x":{"type":"frobnicate"}}}`,
		`{"type":"object","properties":{"x":{"pattern":"(?<=a)b"}}}`,
		`{"type":"object","properties":{"x":{"oneOf":[]}}}`,
		`{"type":"object","properties":{"x":true,"y":false}}`,
	} {
		d := toolDecls(t, `{"type":"function","function":{"name":"f","parameters":`+params+`}}`)[0]
		if !argsMatch(d, json.RawMessage(`{"x":"a"}`)) {
			t.Errorf("a schema keyword that is not understood must not fail a call: %s", params)
		}
	}
}

func TestCompactArgs(t *testing.T) {
	for in, want := range map[string]string{
		`{"city": "Paris"}`:      `{"city":"Paris"}`,
		"{\n  \"a\": [1, 2]\n}":  `{"a":[1,2]}`,
		``:                       `{}`,
		`null`:                   `{}`,
		`{"unterminated`:         `{"unterminated`,
		`{"a":"é","b":"<tag>&"}`: `{"a":"é","b":"<tag>&"}`,
	} {
		if got := compactArgs(json.RawMessage(in)); got != want {
			t.Errorf("compactArgs(%q) = %q, want %q", in, got, want)
		}
	}
}
