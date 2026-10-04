package openaiapi

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
)

// A schema is the client's, and a number in it may be one that float64 cannot hold: 1e400, an
// integer of 400 digits. encoding/json refuses the whole document for one when it is decoded
// into an interface, so naming the arguments found nothing and the function was accepted all the
// same: monomind got a schema with no property and every call reached the client as {}. A number
// is read as the number it is, and kept as it was written.

var fourHundredDigits = strings.Repeat("9", 400)

func TestASchemaWithANumberFloat64CannotHoldStillNamesItsArguments(t *testing.T) {
	for _, c := range []struct {
		name, params string
		props        []string
		kept         string // what the property that holds the number still says, as written
	}{
		{"a bound of a property", `{"type":"object","properties":{"city":{"type":"string"},"n":{"type":"number","maximum":1e400}}}`, []string{"city", "n"}, `"maximum":1e400`},
		{"a negative bound", `{"type":"object","properties":{"city":{"type":"string"},"n":{"type":"number","minimum":-1e400}}}`, []string{"city", "n"}, `"minimum":-1e400`},
		{"a capital E and a plus", `{"type":"object","properties":{"city":{"type":"string"},"n":{"type":"number","maximum":1E+400}}}`, []string{"city", "n"}, `"maximum":1E+400`},
		{"an integer of 400 digits", `{"type":"object","properties":{"city":{"type":"string"},"n":{"type":"integer","default":` + fourHundredDigits + `}}}`, []string{"city", "n"}, `"default":` + fourHundredDigits},
		{"a negative integer of 400 digits", `{"type":"object","properties":{"city":{"type":"string"},"n":{"type":"integer","minimum":-` + fourHundredDigits + `}}}`, []string{"city", "n"}, `"minimum":-` + fourHundredDigits},
		{"a fraction of 400 digits", `{"type":"object","properties":{"city":{"type":"string"},"n":{"type":"number","maximum":0.` + fourHundredDigits + `}}}`, []string{"city", "n"}, `"maximum":0.` + fourHundredDigits},
		{"a negative exponent that float64 holds as zero", `{"type":"object","properties":{"city":{"type":"string"},"n":{"type":"number","minimum":1e-400}}}`, []string{"city", "n"}, `"minimum":1e-400`},
		{"the root", `{"type":"object","maximum":1e400,"properties":{"city":{"type":"string"}}}`, []string{"city"}, ""},
		{"a branch of a root anyOf", `{"anyOf":[{"properties":{"city":{"type":"string"}},"maximum":1e400},{"properties":{"zip":{"type":"string"}}}]}`, []string{"city", "zip"}, ""},
		{"a definition behind a root $ref", `{"$ref":"#/$defs/Args","$defs":{"Args":{"properties":{"city":{"type":"string"}},"maximum":1e400}}}`, []string{"city"}, ""},
		{"an enum that is dropped from the flat copy", `{"type":"object","properties":{"city":{"type":"string"},"n":{"enum":[1e400,2]}}}`, []string{"city", "n"}, `"enum":[1e400,2]`},
		{"an example in a property of a branch", `{"anyOf":[{"properties":{"n":{"examples":[1e400]}}},{"properties":{"city":{"type":"string"}}}]}`, []string{"city", "n"}, ""},
	} {
		props, _ := named(t, c.params)
		if got := keysOf(props); !slices.Equal(got, c.props) {
			t.Errorf("%s: the properties passed on are %v, want %v", c.name, got, c.props)
			continue
		}
		if c.kept != "" && !strings.Contains(string(props["n"]), c.kept) {
			t.Errorf("%s: the property that holds the number is %s, want it to say %s as written", c.name, props["n"], c.kept)
		}
	}
}

// What monomind is given is the flat copy: a property whose schema holds such a number is in it,
// and the number goes as it was written.
func TestToolSpecsKeepAPropertyWhoseSchemaHoldsANumberFloat64CannotHold(t *testing.T) {
	params := `{"type":"object","properties":{"city":{"type":"string"},"n":{"type":"integer","maximum":1e400,"default":` + fourHundredDigits + `}}}`
	specs := toolSpecs(decodeAndValidate(t, oneTool(params)).toolDecls)
	flat, _ := specs[0].Schema["properties"].(map[string]interface{})
	if flat["city"] == nil || flat["n"] == nil || len(flat) != 2 {
		t.Fatalf("the flat copy must name both properties: %v", specs[0].Schema)
	}
	b, err := json.Marshal(specs)
	if err != nil || !strings.Contains(string(b), `"maximum":1e400`) || !strings.Contains(string(b), `"default":`+fourHundredDigits) {
		t.Errorf("the specs marshal with the numbers as written: %v %.300s", err, b)
	}
	if !strings.HasSuffix(specs[0].Description, params) {
		t.Errorf("the whole schema is still in the description: %q", specs[0].Description)
	}
}

// The check of a call's arguments, which is for the log, reads such a number too: a call is not
// counted as one that does not match because it holds a number that is large.
func TestArgsMatchReadsNumbersFloat64CannotHold(t *testing.T) {
	d := toolDecls(t, `{"type":"function","function":{"name":"f","parameters":{"type":"object","properties":{
	  "n":{"type":"number"},"i":{"type":"integer","minimum":0},"small":{"type":"number","maximum":10},
	  "capped":{"type":"number","maximum":1e400},"floor":{"type":"number","minimum":1e400},
	  "list":{"type":"array","items":{"type":"integer","minimum":0}},
	  "geo":{"type":"object","properties":{"lat":{"type":"number","maximum":90}}}}}}}`)[0]
	for _, c := range []struct {
		name, args string
		ok         bool
	}{
		{"a number of 1e400 for a number", `{"n":1e400}`, true},
		{"a negative one", `{"n":-1e400}`, true},
		{"an integer of 400 digits for an integer", `{"i":` + fourHundredDigits + `}`, true},
		{"a negative integer of 400 digits for an integer that is not below 0", `{"i":-` + fourHundredDigits + `}`, false},
		{"a number that is past a bound of 10", `{"small":1e400}`, false},
		{"a finite number within a bound that float64 cannot hold", `{"capped":5}`, true},
		{"a finite number below a minimum that float64 cannot hold", `{"floor":5}`, false},
		{"numbers of the items of a list", `{"list":[` + fourHundredDigits + `,3]}`, true},
		{"a negative number past the minimum of the items of a list", `{"list":[3,-1e400]}`, false},
		{"a number of an object inside the call", `{"geo":{"lat":1.5}}`, true},
		{"a number of an object inside the call that is past its bound", `{"geo":{"lat":1e400}}`, false},
		{"data after the document", `{"n":1e400} {"n":2}`, false},
		{"an ordinary call is unchanged", `{"n":1.5,"i":3,"small":9}`, true},
	} {
		if got := argsMatch(d, json.RawMessage(c.args)); got != c.ok {
			t.Errorf("%s: argsMatch = %v, want %v", c.name, got, c.ok)
		}
	}
}

// Every way arguments cannot be named is a 400 on the parameters of the function, with a message
// of its own and none of what the client wrote. (A schema that cannot be read is not a case a
// request reaches, validation has read it as JSON before, so the answer is checked here.)
func TestEveryWayArgumentsCannotBeNamedIsRefusedOnTheParametersWithAMessageOfItsOwn(t *testing.T) {
	const param = "tools[3].function.parameters"
	seen := map[string]bool{}
	for name, c := range map[string]struct {
		names argNames
		want  string
	}{
		"unreadable": {argNames{Unreadable: true}, "could not be read"},
		"spent":      {argNames{Spent: true}, "together"},
		"too deep":   {argNames{TooDeep: true}, "levels deep"},
		"too wide":   {argNames{TooWide: true}, "more than 2000 schemas"},
		"open":       {argNames{Open: true}, "name no property"},
	} {
		err := unnameable(param, c.names)
		if err == nil || err.Status != 400 || err.Code != "invalid_value" || err.Param != param || !strings.Contains(err.Message, c.want) {
			t.Errorf("%s: %+v, want 400 invalid_value on %s saying %q", name, err, param, c.want)
			continue
		}
		if seen[err.Message] {
			t.Errorf("%s: the message is another way's: %s", name, err.Message)
		}
		seen[err.Message] = true
	}
	for name, names := range map[string]argNames{
		"nothing wrong":                    {},
		"free-form keys beside a property": {Open: true, Props: map[string]json.RawMessage{"a": json.RawMessage("true")}},
	} {
		if err := unnameable(param, names); err != nil {
			t.Errorf("%s: %+v", name, err)
		}
	}
}

// A schema that cannot be read at all is not passed over as one with no arguments: the caller
// refuses it.
func TestASchemaThatCannotBeReadIsSaidSoNotTakenForOneWithoutArguments(t *testing.T) {
	for _, params := range []string{`{"properties":{"city":`, `[1]`, `"x"`, `{"a":1} {"b":2}`} {
		if got := nameArguments(json.RawMessage(params), nil); !got.Unreadable || len(got.Props) != 0 {
			t.Errorf("%s: %+v, want Unreadable", params, got)
		}
	}
	for _, params := range []string{``, `null`, `{}`} {
		if got := nameArguments(json.RawMessage(params), nil); got.Unreadable {
			t.Errorf("%q is a schema with nothing in it, not an unreadable one: %+v", params, got)
		}
	}
}
