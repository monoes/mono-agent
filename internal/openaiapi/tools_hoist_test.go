package openaiapi

import (
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"
)

// monomind turns a declared function into the arguments it lets through from the top-level
// properties of its schema alone: a key that is not one of them is dropped from the call
// the client gets. So a schema whose arguments sit in the branches of a root anyOf, oneOf or
// allOf, behind a root $ref, or in a map of free-form keys, gave the client {} without a
// word. The properties of the branches are named at the top level for monomind (the whole
// schema still goes into the description), and a schema whose arguments cannot be named at
// all is refused.

// oneTool is a request that declares get_weather with the given parameters.
func oneTool(params string) string {
	return toolBody(`"tools":[{"type":"function","function":{"name":"get_weather","description":"d","parameters":`+params+`}}]`, userHi)
}

// named validates a schema and says which properties and required names are passed on.
func named(t *testing.T, params string) (props map[string]json.RawMessage, required []string) {
	t.Helper()
	req := decodeRequest(t, oneTool(params))
	if err := validateChat(req); err != nil {
		t.Fatalf("%s was refused: %+v", params, err)
	}
	d := req.toolDecls[0]
	required = slices.Clone(d.Required)
	slices.Sort(required)
	return d.Props, required
}

func keysOf(m map[string]json.RawMessage) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}

func TestArgumentsOfRootCombinatorsAndReferencesAreNamedAtTheTopLevel(t *testing.T) {
	const (
		city = `{"properties":{"city":{"type":"string"}},"required":["city"]}`
		zip  = `{"properties":{"zip":{"type":"string"}},"required":["zip"]}`
	)
	cases := []struct {
		name, params string
		props        []string
		required     []string
	}{
		{"an ordinary schema", `{"type":"object","properties":{"city":{"type":"string"}},"required":["city"]}`, []string{"city"}, []string{"city"}},
		{"a root anyOf: optional, each branch could be the one", `{"anyOf":[` + city + `,` + zip + `]}`, []string{"city", "zip"}, nil},
		{"a root oneOf", `{"type":"object","oneOf":[` + city + `,` + zip + `]}`, []string{"city", "zip"}, nil},
		{"a root allOf: required, every branch applies", `{"allOf":[` + city + `,{"properties":{"units":{"type":"string"}}}]}`, []string{"city", "units"}, []string{"city"}},
		{"a root $ref into $defs", `{"$ref":"#/$defs/Args","$defs":{"Args":{"type":"object",` + city[1:] + `}}`, []string{"city"}, []string{"city"}},
		{"a root $ref into definitions", `{"$ref":"#/definitions/Args","definitions":{"Args":` + city + `}}`, []string{"city"}, []string{"city"}},
		{"an allOf of a $ref and properties of its own", `{"allOf":[{"$ref":"#/$defs/Base"},{"properties":{"units":{"type":"string"}}}],"$defs":{"Base":` + city + `}}`, []string{"city", "units"}, []string{"city"}},
		{"$ref branches of an anyOf", `{"anyOf":[{"$ref":"#/$defs/A"},{"$ref":"#/$defs/B"}],"$defs":{"A":` + city + `,"B":` + zip + `}}`, []string{"city", "zip"}, nil},
		{"properties beside a root anyOf", `{"type":"object","properties":{"city":{"type":"string"}},"required":["city"],"anyOf":[{"properties":{"units":{"type":"string"}}}]}`, []string{"city", "units"}, []string{"city"}},
		{"a root required that a branch defines", `{"required":["city"],"anyOf":[{"properties":{"city":{"type":"string"}}},` + zip + `]}`, []string{"city", "zip"}, []string{"city"}},
		{"names that are only required", `{"oneOf":[{"required":["a"]},{"required":["b"]}]}`, []string{"a", "b"}, nil},
		{"a nested combinator", `{"anyOf":[{"allOf":[` + city + `]},{"anyOf":[` + zip + `]}]}`, []string{"city", "zip"}, nil},
		{"a reference that loops", `{"$ref":"#/$defs/A","$defs":{"A":{"properties":{"x":{"type":"string"}},"allOf":[{"$ref":"#/$defs/A"}]}}}`, []string{"x"}, nil},
		{"a pointer with an escaped segment", `{"$ref":"#/$defs/a~1b","$defs":{"a/b":` + city + `}}`, []string{"city"}, []string{"city"}},
		{"a root $ref to the schema itself", `{"$ref":"#","properties":{"x":{"type":"string"}}}`, []string{"x"}, nil},
		{"a root $ref to the schema itself, naming nothing: no arguments, not free-form ones", `{"$ref":"#"}`, nil, nil},
		{"a pointer into an array", `{"anyOf":[` + city + `],"allOf":[{"$ref":"#/anyOf/0"}]}`, []string{"city"}, []string{"city"}},
		{"a reference met as optional first and as required after", `{"allOf":[{"anyOf":[{"$ref":"#/$defs/A"}]},{"$ref":"#/$defs/A"}],"$defs":{"A":` + city + `}}`, []string{"city"}, []string{"city"}},
		{"an if, a then and an else: each may apply", `{"if":{"properties":{"mode":{"enum":["a"]}}},"then":` + city + `,"else":` + zip + `}`, []string{"city", "mode", "zip"}, nil},
		{"dependentSchemas: optional", `{"type":"object","properties":{"x":{"type":"string"}},"dependentSchemas":{"x":` + zip + `}}`, []string{"x", "zip"}, nil},
		// What a property asks for when it is there is an argument too, and none of it is asked of every
		// call: the keys (a call may carry them), the schemas they bring (dependencies, as a schema, is
		// dependentSchemas of draft-07) and the names a list asks for (dependencies as a list, and
		// dependentRequired).
		{"dependentSchemas whose key nothing defines", `{"dependentSchemas":{"x":` + zip + `}}`, []string{"x", "zip"}, nil},
		{"dependencies, a schema: optional", `{"type":"object","properties":{"x":{"type":"string"}},"dependencies":{"x":` + zip + `}}`, []string{"x", "zip"}, nil},
		{"dependencies, a schema whose key nothing defines", `{"dependencies":{"mode":{"properties":{"level":{"type":"integer"}},"required":["level"]}}}`, []string{"level", "mode"}, nil},
		{"dependencies, a list of names", `{"type":"object","properties":{"card":{"type":"string"}},"dependencies":{"card":["billing","zip"]}}`, []string{"billing", "card", "zip"}, nil},
		{"dependentRequired", `{"type":"object","properties":{"card":{"type":"string"}},"dependentRequired":{"card":["billing"]}}`, []string{"billing", "card"}, nil},
		{"dependentRequired with nothing else", `{"dependentRequired":{"credit_card":["billing_address"]}}`, []string{"billing_address", "credit_card"}, nil},
		{"dependencies of both forms in a branch", `{"anyOf":[{"dependencies":{"a":["b"],"c":{"properties":{"d":{"type":"string"}}}}}]}`, []string{"a", "b", "c", "d"}, nil},
		{"a boolean schema and other things that are no schema as a dependency", `{"properties":{"x":{"type":"string"}},"dependencies":{"x":true,"y":false,"z":"nope","w":[1,null]}}`, []string{"w", "x", "y", "z"}, nil},
		{"a name a dependency asks for that the schema requires anyway", `{"required":["b"],"dependentRequired":{"a":["b"]}}`, []string{"a", "b"}, []string{"b"}},
		// The arguments of a call are an object, so a const or an enum of objects names arguments: a
		// call that is one of them has those keys.
		{"a root const that is an object", `{"const":{"city":"Paris"}}`, []string{"city"}, nil},
		{"a root enum of objects", `{"enum":[{"city":"Paris"},{"zip":"75001"}]}`, []string{"city", "zip"}, nil},
		{"a const beside properties", `{"type":"object","properties":{"city":{"type":"string"}},"const":{"city":"Paris","units":"c"}}`, []string{"city", "units"}, nil},
		{"a const in the branches of an anyOf", `{"anyOf":[{"const":{"city":"Paris"}},{"enum":[{"zip":"1"}]}]}`, []string{"city", "zip"}, nil},
		{"a const behind a $ref", `{"$ref":"#/$defs/A","$defs":{"A":{"const":{"city":"Paris"}}}}`, []string{"city"}, nil},
		{"a branch that no call can match is not an error", `{"anyOf":[{"const":5},{"properties":{"city":{"type":"string"}}}]}`, []string{"city"}, nil},
		{"the const of an if is a condition", `{"if":{"const":5},"then":{"properties":{"city":{"type":"string"}}}}`, []string{"city"}, nil},
		{"the const of a property is not the schema's", `{"properties":{"mode":{"const":5,"type":"integer"}}}`, []string{"mode"}, nil},
		{"an empty enum", `{"enum":[]}`, nil, nil},
	}
	for _, c := range cases {
		props, required := named(t, c.params)
		if got := keysOf(props); !slices.Equal(got, c.props) {
			t.Errorf("%s: the properties passed on are %v, want %v", c.name, got, c.props)
		}
		if !slices.Equal(required, c.required) {
			t.Errorf("%s: required is %v, want %v", c.name, required, c.required)
		}
	}
}

// What monomind gets of a property is its type and an enum of strings, and it holds every call to
// them: a call that does not match is rejected, and the client gets the answer of a model that gave
// up after its rounds. So a property is given its type and its enum only where the schema is
// certain of them for every call: in the root's own properties, in an allOf and behind a $ref that
// applies. A name that is only found in a branch that may or may not apply (anyOf, oneOf, if, then,
// else, dependentSchemas) is any value: `{"city":"Paris"}` is a call the schema allows when only
// one branch says city is one of Rome and Milan. A name that two certain definitions disagree on
// is any value too.
func TestAPropertyKeepsItsTypeAndEnumOnlyWhereTheSchemaIsCertainOfThem(t *testing.T) {
	const city = `{"type":"string","description":"a"}`
	for _, c := range []struct {
		name, params string
		want         string // the property as monomind is told of it: "any" for a value of any kind
	}{
		{"the root's own properties", `{"properties":{"id":` + city + `}}`, city},
		{"an allOf", `{"allOf":[{"properties":{"id":` + city + `}}]}`, city},
		{"a reference that applies", `{"$ref":"#/$defs/A","$defs":{"A":{"properties":{"id":` + city + `}}}}`, city},
		{"a reference inside an allOf", `{"allOf":[{"$ref":"#/$defs/A"}],"$defs":{"A":{"properties":{"id":` + city + `}}}}`, city},
		{"two certain definitions that agree: the first is kept", `{"properties":{"id":` + city + `},"allOf":[{"properties":{"id":{"type":"string","minLength":1}}}]}`, city},
		{"two certain definitions that agree on an enum in another order", `{"properties":{"id":{"enum":["a","b"],"description":"first"}},"allOf":[{"properties":{"id":{"enum":["b","a"]}}}]}`, `{"enum":["a","b"],"description":"first"}`},
		{"two certain definitions that disagree on the type", `{"properties":{"id":{"type":"string"}},"allOf":[{"properties":{"id":{"type":"integer"}}}]}`, "any"},
		{"two certain definitions that disagree on the enum", `{"properties":{"id":{"enum":["a","b"]}},"allOf":[{"properties":{"id":{"enum":["a","c"]}}}]}`, "any"},
		{"a third that agrees with the first does not undo the giving up", `{"properties":{"id":{"type":"string"}},"allOf":[{"properties":{"id":{"type":"integer"}}},{"properties":{"id":{"type":"string"}}}]}`, "any"},

		{"an anyOf branch: it may not apply", `{"anyOf":[{"properties":{"id":` + city + `}},{"properties":{"zip":{"type":"string"}}}]}`, "any"},
		{"a oneOf branch", `{"oneOf":[{"properties":{"id":{"enum":["Rome","Milan"]}}},{"properties":{"zip":{"type":"string"}}}]}`, "any"},
		{"a then", `{"if":{"required":["zip"]},"then":{"properties":{"id":{"type":"number"}}}}`, "any"},
		{"an else", `{"if":{"required":["zip"]},"else":{"properties":{"id":{"enum":["Rome"]}}}}`, "any"},
		{"the condition of an if", `{"if":{"properties":{"id":{"enum":["Rome"]}},"required":["id"]},"then":{"properties":{"zip":{"type":"string"}}}}`, "any"},
		{"a dependent schema", `{"dependentSchemas":{"zip":{"properties":{"id":{"type":"integer"}}}}}`, "any"},
		{"a dependency that is a schema", `{"dependencies":{"zip":{"properties":{"id":{"type":"integer"}}}}}`, "any"},
		{"a name that a dependency asks for", `{"dependencies":{"zip":["id"]}}`, "any"},
		{"a name of a const", `{"const":{"id":"Paris"}}`, "any"},
		{"a name of an enum", `{"enum":[{"id":1},{"id":2}]}`, "any"},
		{"an anyOf branch that is a reference", `{"anyOf":[{"$ref":"#/$defs/A"}],"$defs":{"A":{"properties":{"id":` + city + `}}}}`, "any"},
		{"an allOf inside an anyOf branch: still optional", `{"anyOf":[{"allOf":[{"properties":{"id":` + city + `}}]}]}`, "any"},
		{"branches that say the same: still any value", `{"anyOf":[{"properties":{"id":` + city + `}},{"properties":{"id":` + city + `}}]}`, "any"},
		{"branches that disagree", `{"anyOf":[{"properties":{"id":{"type":"string"}}},{"properties":{"id":{"type":"integer"}}}]}`, "any"},

		{"a certain definition and a branch that says another: the certain one", `{"properties":{"id":` + city + `},"anyOf":[{"properties":{"id":{"type":"integer"}}}]}`, city},
		{"a branch first, then a certain definition: the certain one", `{"allOf":[{"anyOf":[{"properties":{"id":{"type":"integer"}}}]},{"properties":{"id":` + city + `}}]}`, city},
		{"a reference met as optional and then as applied: the applied one", `{"anyOf":[{"$ref":"#/$defs/A"}],"allOf":[{"$ref":"#/$defs/A"}],"$defs":{"A":{"properties":{"id":` + city + `}}}}`, city},
	} {
		props, _ := named(t, c.params)
		got := string(props["id"])
		if c.want == "any" {
			if got != "true" {
				t.Errorf("%s: the property is %s, want any value (true)", c.name, got)
			}
			continue
		}
		var gotM, wantM map[string]any
		if json.Unmarshal([]byte(got), &gotM) != nil || json.Unmarshal([]byte(c.want), &wantM) != nil || !reflect.DeepEqual(gotM, wantM) {
			t.Errorf("%s: the property is %s, want %s", c.name, got, c.want)
		}
	}
}

// What monomind gets is the flat copy, which is what the call is parsed with: the
// properties of the branches are in it, and the whole schema is still in the description.
func TestToolSpecsNameTheHoistedPropertiesAndStillFoldTheWholeSchema(t *testing.T) {
	const params = `{"anyOf":[{"properties":{"city":{"type":"string"}},"required":["city"]},{"properties":{"zip":{"type":"string"}},"required":["zip"]}]}`
	specs := toolSpecs(decodeAndValidate(t, oneTool(params)).toolDecls)
	flat, _ := specs[0].Schema["properties"].(map[string]interface{})
	if len(flat) != 2 || flat["city"] == nil || flat["zip"] == nil {
		t.Errorf("the flat copy must name the properties of both branches: %v", specs[0].Schema)
	}
	if _, has := specs[0].Schema["required"]; has {
		t.Errorf("neither branch is required of a call: %v", specs[0].Schema)
	}
	if !strings.HasSuffix(specs[0].Description, params) {
		t.Errorf("the whole schema is still in the description: %q", specs[0].Description)
	}
}

func decodeAndValidate(t *testing.T, body string) *ChatRequest {
	t.Helper()
	req := decodeRequest(t, body)
	if err := validateChat(req); err != nil {
		t.Fatalf("refused: %+v", err)
	}
	return req
}

// A function whose arguments cannot be named, even after that, would get {} at every call:
// the request is refused, and the answer names no property and no function.
func TestAFunctionWhoseArgumentsCannotBeNamedIsRefused(t *testing.T) {
	const marker = "MARKERname"
	for _, c := range []struct{ name, params string }{
		{"free-form values", `{"type":"object","additionalProperties":{"type":"string","description":"` + marker + `"}}`},
		{"additionalProperties true and nothing else", `{"type":"object","additionalProperties":true,"description":"` + marker + `"}`},
		{"pattern properties only", `{"type":"object","patternProperties":{"^` + marker + `-":{"type":"string"}}}`},
		{"a reference that cannot be followed", `{"$ref":"https://example.com/` + marker + `.json"}`},
		{"a reference to nothing", `{"$ref":"#/$defs/` + marker + `"}`},
		{"free-form values inside a branch", `{"anyOf":[{"additionalProperties":{"type":"string"}},{"type":"object"}],"description":"` + marker + `"}`},
		{"a root combinator whose branches name nothing, over free-form values", `{"allOf":[{"additionalProperties":true}],"description":"` + marker + `"}`},
		{"a dynamic reference", `{"$dynamicRef":"#meta","description":"` + marker + `"}`},
		{"unevaluated properties", `{"type":"object","unevaluatedProperties":true,"description":"` + marker + `"}`},
	} {
		body := toolBody(`"tools":[{"type":"function","function":{"name":"`+marker+`","parameters":`+c.params+`}}]`, userHi)
		err := validateChat(decodeRequest(t, body))
		if err == nil || err.Status != http.StatusBadRequest || err.Code != "invalid_value" || err.Param != "tools[0].function.parameters" {
			t.Errorf("%s: got %+v, want 400 invalid_value on tools[0].function.parameters", c.name, err)
			continue
		}
		if b := string(err.body()); strings.Contains(b, marker) {
			t.Errorf("%s: the error echoes what the client wrote: %s", c.name, b)
		}
	}
}

// The arguments of a call are always an object: a const or an enum that every call must match (the
// root's, an allOf's, a reference that applies) and that holds anything else leaves no call that could
// match, and the function is refused with a message that names no value. A branch that may not apply
// is only a branch that cannot match, and what is not the schema's own const (a property's) is not
// looked at; with tool_choice none no schema is read.
func TestAConstOrEnumThatNoCallCanMatchIsRefused(t *testing.T) {
	const marker = "MARKERvalue"
	for _, c := range []struct{ name, params string }{
		{"a root const that is a number", `{"const":5,"description":"` + marker + `"}`},
		{"a root const that is a string", `{"const":"` + marker + `"}`},
		{"a root const that is null", `{"const":null,"description":"` + marker + `"}`},
		{"a root const that is a list", `{"const":["` + marker + `"]}`},
		{"a root enum of strings", `{"enum":["` + marker + `","b"]}`},
		{"a root enum with one value that is no object", `{"enum":[{"city":"Paris"},"` + marker + `"]}`},
		{"a const that is false", `{"const":false}`},
		{"a const in an allOf", `{"allOf":[{"const":"` + marker + `"}]}`},
		{"an enum behind a $ref that applies", `{"$ref":"#/$defs/A","$defs":{"A":{"enum":["` + marker + `"]}}}`},
		{"a const beside properties", `{"type":"object","properties":{"city":{"type":"string"}},"const":"` + marker + `"}`},
	} {
		body := toolBody(`"tools":[{"type":"function","function":{"name":"get_weather","parameters":`+c.params+`}}]`, userHi)
		err := validateChat(decodeRequest(t, body))
		if err == nil || err.Status != http.StatusBadRequest || err.Code != "invalid_value" || err.Param != "tools[0].function.parameters" {
			t.Errorf("%s: got %+v, want 400 invalid_value on tools[0].function.parameters", c.name, err)
			continue
		}
		if b := string(err.body()); strings.Contains(b, marker) {
			t.Errorf("%s: the error echoes what the client wrote: %s", c.name, b)
		}
		none := toolBody(`"tools":[{"type":"function","function":{"name":"get_weather","parameters":`+c.params+`}}],"tool_choice":"none"`, userHi)
		if err := validateChat(decodeRequest(t, none)); err != nil {
			t.Errorf("%s: refused with tool_choice none, which passes no tools: %+v", c.name, err)
		}
	}
	for _, params := range []string{
		`{"anyOf":[{"const":5},{"properties":{"city":{"type":"string"}}}]}`,
		`{"if":{"const":5},"then":{"properties":{"city":{"type":"string"}}}}`,
		`{"properties":{"mode":{"const":5}}}`,
		`{"enum":[]}`,
		`{"const":{"city":"Paris"}}`,
		`{"enum":[{"city":"Paris"},{"city":"Rome"}]}`,
	} {
		if err := validateChat(decodeRequest(t, oneTool(params))); err != nil {
			t.Errorf("%s was refused: %+v", params, err)
		}
	}
}

// A function with no arguments, and one that has properties as well as free-form keys, are
// valid: only the properties are passed on.
func TestFunctionsWithoutArgumentsOrWithFreeFormKeysBesideTheirPropertiesStayValid(t *testing.T) {
	for _, params := range []string{
		`{}`, `{"type":"object"}`, `{"type":"object","properties":{}}`, `{"type":"object","additionalProperties":false}`,
		`{"type":"object","patternProperties":{}}`, `{"type":"object","description":"No arguments."}`,
		`{"anyOf":[{"type":"object"},{"type":"string"}]}`, `{"properties":{},"additionalProperties":false}`,
		`{"$ref":"#/$defs/T","$defs":{"T":true}}`, `{"$ref":"#/$defs/T","$defs":{"T":"not a schema"}}`,
	} {
		if props, _ := named(t, params); len(props) != 0 {
			t.Errorf("%s names properties: %v", params, keysOf(props))
		}
	}
	for _, params := range []string{
		`{"type":"object","properties":{"city":{"type":"string"}},"additionalProperties":true}`,
		`{"type":"object","properties":{"city":{"type":"string"}},"additionalProperties":{"type":"string"}}`,
		`{"type":"object","properties":{"city":{"type":"string"}},"patternProperties":{"^x-":{"type":"string"}}}`,
		`{"anyOf":[{"properties":{"city":{"type":"string"}}},{"additionalProperties":true}]}`,
	} {
		if props, _ := named(t, params); !slices.Equal(keysOf(props), []string{"city"}) {
			t.Errorf("%s: the properties passed on are %v, want city alone", params, keysOf(props))
		}
	}
}

// A schema is the client's: how much of it is looked at is bounded, and a schema that
// loops or fans out through its references costs a moment, not the server.
func TestHoistingIsBoundedWhateverTheSchemaDoes(t *testing.T) {
	var deep strings.Builder
	for range 500 {
		deep.WriteString(`{"properties":{"p":{"type":"string"}},"anyOf":[`)
	}
	deep.WriteString(`{}`)
	deep.WriteString(strings.Repeat(`]}`, 500))

	// 40 definitions, each referring to the next twice: 2^40 paths if every one were followed.
	var defs []string
	for i := range 40 {
		defs = append(defs, fmt.Sprintf(`"D%d":{"properties":{"p%d":{"type":"string"}},"allOf":[{"$ref":"#/$defs/D%d"},{"$ref":"#/$defs/D%d"}]}`, i, i, i+1, i+1))
	}
	defs = append(defs, `"D40":{"properties":{"last":{"type":"string"}}}`)
	fan := `{"$ref":"#/$defs/D0","$defs":{` + strings.Join(defs, ",") + `}}`

	for name, params := range map[string]string{"500 levels of anyOf": deep.String(), "a fan of references": fan} {
		done := make(chan *apiError, 1)
		go func() { done <- validateChat(decodeRequest(t, oneTool(params))) }()
		select {
		case err := <-done:
			if err != nil && err.Status != http.StatusBadRequest {
				t.Errorf("%s: %+v", name, err)
			}
		case <-time.After(10 * time.Second):
			t.Fatalf("%s: validation did not end", name)
		}
	}
}

// What is read of a schema has a bound, and a schema past it is refused, not named in part:
// arguments that went unnamed would be dropped without a word, which is what naming them
// is for. The bound is how deep the combinators and references nest, and how many schemas
// there are in all.
func TestASchemaThatNestsOrFansOutPastWhatIsReadIsRefusedNotNamedInPart(t *testing.T) {
	nest := func(levels int) string {
		return strings.Repeat(`{"anyOf":[`, levels) + `{"properties":{"p":{"type":"string"}}}` + strings.Repeat(`]}`, levels)
	}
	siblings := func(n int) string { // n schemas in all, the root among them
		return `{"anyOf":[{"properties":{"p":{"type":"string"}}}` + strings.Repeat(`,{}`, n-2) + `]}`
	}
	for _, c := range []struct {
		name, params string
		refused      bool
	}{
		{"nested as deep as is read", nest(maxHoistDepth), false},
		{"nested one level deeper", nest(maxHoistDepth + 1), true},
		{"as many schemas as are read", siblings(maxHoistNodes), false},
		{"one schema more", siblings(maxHoistNodes + 1), true},
	} {
		err := validateChat(decodeRequest(t, oneTool(c.params)))
		switch {
		case !c.refused && err != nil:
			t.Errorf("%s: refused: %+v", c.name, err)
		case c.refused && (err == nil || err.Status != http.StatusBadRequest || err.Code != "invalid_value" || err.Param != "tools[0].function.parameters"):
			t.Errorf("%s: got %+v, want 400 invalid_value on tools[0].function.parameters", c.name, err)
		}
	}
}

// A property of a branch is not checked the way a top-level one is: what is not a schema is
// let through as any value, so that monomind never gets a null where it reads a schema.
func TestAPropertyOfABranchThatIsNotASchemaIsLetThroughAsAnyValue(t *testing.T) {
	props, _ := named(t, `{"allOf":[{"properties":{"a":null,"b":5,"c":"x","d":[1]}},{"properties":{"e":false}}]}`)
	for _, name := range []string{"a", "b", "c", "d"} {
		if string(props[name]) != "true" {
			t.Errorf("property %s: got %s, want any value", name, props[name])
		}
	}
	if string(props["e"]) != "false" {
		t.Errorf("a boolean schema is passed as it is, as a top-level one is: %s", props["e"])
	}
	props, _ = named(t, `{"anyOf":[{"properties":{"a":null,"e":false}}]}`)
	for _, name := range []string{"a", "e"} {
		if string(props[name]) != "true" {
			t.Errorf("property %s of a branch that may not apply: got %s, want any value", name, props[name])
		}
	}
}
