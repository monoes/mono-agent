package openaiapi

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"math"
	"reflect"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/monoes/mono-agent/internal/monomind"
)

// toolSpecs maps the declared functions onto the tool specs monomind takes.
//
// monomind keeps only the top-level properties of a schema, and of those only
// the type and a string enum: a property's description, the items of an array
// and every nested key are dropped, and so is every argument of a call that no
// top-level property names. So a spec carries the flat schema monomind can use,
// which names the arguments of a root anyOf, oneOf or allOf, of a local $ref and
// of an if, a then and an else at the top level too (nameArguments), and the
// whole parameters schema, compact, goes at the end of the description, where
// the model reads it (a rule that lived only in a property's description was
// lost with the flat schema and kept when folded in). A schema with nothing at
// its top level is folded too: the description is all the model would have of it.
func toolSpecs(decls []toolDecl) []monomind.ToolSpec {
	specs := make([]monomind.ToolSpec, 0, len(decls))
	for _, d := range decls {
		props := make(map[string]interface{}, len(d.Props))
		for name, raw := range d.Props {
			if v, err := decodeKeepingNumbers(raw); err == nil {
				leaveOutForeignEnum(v)
				props[name] = v
			}
		}
		schema := map[string]interface{}{"type": "object", "properties": props}
		if len(d.Required) > 0 {
			schema["required"] = d.Required
		}
		desc := d.Description
		if !paramsSayNothing(d.Params) {
			if desc != "" {
				desc += "\n\n"
			}
			desc += "Parameters (JSON Schema):\n" + string(d.Params)
		}
		specs = append(specs, monomind.ToolSpec{Name: d.Wire, Description: desc, Schema: schema})
	}
	return specs
}

// leaveOutForeignEnum removes the enum of a property when it is not a non-empty
// list of strings: monomind's tool bridge takes no other, and rejects every call of
// a tool that has one. The model still reads the enum in the description.
func leaveOutForeignEnum(prop interface{}) {
	p, ok := prop.(map[string]interface{})
	if !ok {
		return
	}
	enum, has := p["enum"]
	if !has {
		return
	}
	list, _ := enum.([]interface{})
	if len(list) == 0 || slices.ContainsFunc(list, func(e interface{}) bool { _, isString := e.(string); return !isString }) {
		delete(p, "enum")
	}
}

// paramsSayNothing reports whether a parameters schema tells the model nothing that
// a tool without parameters does not: there is none, or it is an object with no
// properties, no required list and no keyword of substance. Any other schema is
// folded into the description.
func paramsSayNothing(params json.RawMessage) bool {
	if len(params) == 0 {
		return true
	}
	var top map[string]json.RawMessage // params went through inspectParams: a JSON object
	_ = json.Unmarshal(params, &top)
	for key, v := range top {
		switch key {
		case "type", "$schema":
		case "properties", "required":
			if s := string(v); s != "null" && s != "{}" && s != "[]" {
				return false
			}
		case "additionalProperties":
			if string(v) != "false" {
				return false
			}
		default:
			return false
		}
	}
	return true
}

// toolsHash identifies a set of declared functions, whatever their order: a
// runtime session is only continued with the tools it was started with.
func toolsHash(decls []toolDecl) string {
	type entry struct {
		Name, Description string
		Params            json.RawMessage
	}
	entries := make([]entry, 0, len(decls))
	for _, d := range decls {
		entries = append(entries, entry{d.Name, d.Description, d.Params})
	}
	slices.SortFunc(entries, func(a, b entry) int { return strings.Compare(a.Name, b.Name) })
	b, _ := json.Marshal(entries)
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// compactArgs is a call's arguments as the string of JSON OpenAI's API puts in
// function.arguments: compact, and {} when the model gave none.
func compactArgs(raw json.RawMessage) string {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || string(raw) == "null" {
		return "{}"
	}
	var buf bytes.Buffer
	if json.Compact(&buf, raw) != nil {
		return string(raw)
	}
	return buf.String()
}

// argsMatch reports whether a call's arguments are a JSON object that the
// declared schema accepts. Only the keywords that matter for arguments are
// checked (type, enum, const, required, properties, additionalProperties,
// items, anyOf, oneOf, allOf, the numeric and length bounds); a schema or a
// keyword that is not understood accepts. The answer is for the log: a call that
// does not match is still returned, and the client decides.
func argsMatch(d toolDecl, args json.RawMessage) bool {
	v, err := decodeFloats(args)
	if err != nil {
		return false
	}
	if _, ok := v.(map[string]interface{}); !ok {
		return false
	}
	if len(d.Params) == 0 {
		return true
	}
	schema, err := decodeFloats(d.Params)
	if err != nil {
		return true
	}
	return schemaAccepts(schema, v, 0)
}

// maxSchemaDepth is how deep schemaAccepts looks: a schema is the client's, and
// the nesting of one that fits in the size limit is not.
const maxSchemaDepth = 64

func schemaAccepts(schema, v interface{}, depth int) bool {
	s, ok := schema.(map[string]interface{})
	if !ok || depth > maxSchemaDepth {
		return true
	}
	if t, has := s["type"]; has && !typeAccepts(t, v) {
		return false
	}
	if enum, ok := s["enum"].([]interface{}); ok && len(enum) > 0 &&
		!slices.ContainsFunc(enum, func(e interface{}) bool { return reflect.DeepEqual(e, v) }) {
		return false
	}
	if c, has := s["const"]; has && !reflect.DeepEqual(c, v) {
		return false
	}
	if branches, ok := s["anyOf"].([]interface{}); ok && len(branches) > 0 && !anyBranch(branches, v, depth) {
		return false
	}
	if branches, ok := s["oneOf"].([]interface{}); ok && len(branches) > 0 && !anyBranch(branches, v, depth) {
		return false
	}
	if branches, ok := s["allOf"].([]interface{}); ok {
		for _, b := range branches {
			if !schemaAccepts(b, v, depth+1) {
				return false
			}
		}
	}
	switch x := v.(type) {
	case map[string]interface{}:
		return objectAccepts(s, x, depth)
	case []interface{}:
		return arrayAccepts(s, x, depth)
	case string:
		n := float64(utf8.RuneCountInString(x))
		return atLeast(s["minLength"], n) && atMost(s["maxLength"], n)
	case float64:
		return atLeast(s["minimum"], x) && atMost(s["maximum"], x) &&
			above(s["exclusiveMinimum"], x) && below(s["exclusiveMaximum"], x)
	}
	return true
}

func anyBranch(branches []interface{}, v interface{}, depth int) bool {
	for _, b := range branches {
		if schemaAccepts(b, v, depth+1) {
			return true
		}
	}
	return false
}

// typeAccepts reports whether v is of a JSON Schema type, or of one of a list of
// them. A type that is not one of the seven is not understood, and accepts.
func typeAccepts(t, v interface{}) bool {
	switch t := t.(type) {
	case string:
		switch t {
		case "string":
			_, ok := v.(string)
			return ok
		case "number":
			_, ok := v.(float64)
			return ok
		case "integer":
			f, ok := v.(float64)
			return ok && f == math.Trunc(f) // a number too large for a float64 is ±Inf: it may be one, and accepts
		case "boolean":
			_, ok := v.(bool)
			return ok
		case "object":
			_, ok := v.(map[string]interface{})
			return ok
		case "array":
			_, ok := v.([]interface{})
			return ok
		case "null":
			return v == nil
		}
		return true
	case []interface{}:
		if len(t) == 0 {
			return true
		}
		for _, one := range t {
			if typeAccepts(one, v) {
				return true
			}
		}
		return false
	}
	return true
}

func objectAccepts(s, x map[string]interface{}, depth int) bool {
	props, _ := s["properties"].(map[string]interface{})
	if required, ok := s["required"].([]interface{}); ok {
		for _, r := range required {
			if name, isString := r.(string); isString {
				if _, has := x[name]; !has {
					return false
				}
			}
		}
	}
	for k, val := range x {
		if p, has := props[k]; has {
			if !schemaAccepts(p, val, depth+1) {
				return false
			}
			continue
		}
		switch extra := s["additionalProperties"].(type) {
		case bool:
			if !extra {
				return false
			}
		case map[string]interface{}:
			if !schemaAccepts(extra, val, depth+1) {
				return false
			}
		}
	}
	return true
}

func arrayAccepts(s map[string]interface{}, x []interface{}, depth int) bool {
	n := float64(len(x))
	if !atLeast(s["minItems"], n) || !atMost(s["maxItems"], n) {
		return false
	}
	if items, ok := s["items"].(map[string]interface{}); ok {
		for _, e := range x {
			if !schemaAccepts(items, e, depth+1) {
				return false
			}
		}
	}
	return true
}

// The bounds of a schema: each accepts when the keyword is absent or not a number.
func atLeast(bound interface{}, v float64) bool {
	b, ok := bound.(float64)
	return !ok || v >= b
}

func atMost(bound interface{}, v float64) bool {
	b, ok := bound.(float64)
	return !ok || v <= b
}

func above(bound interface{}, v float64) bool {
	b, ok := bound.(float64)
	return !ok || v > b
}

func below(bound interface{}, v float64) bool {
	b, ok := bound.(float64)
	return !ok || v < b
}
