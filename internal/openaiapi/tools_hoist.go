package openaiapi

import (
	"encoding/json"
	"net/url"
	"slices"
	"strconv"
	"strings"
)

// How much of a schema is read when the arguments of a function are named. The schema is
// the client's: the nesting of one that fits in the size limit is not, so a schema that
// goes past either bound is refused, not named in part.
const (
	maxHoistDepth = 8    // levels of combinators and references followed
	maxHoistNodes = 2000 // schemas visited in all
)

// argNames is what nameArguments found in a parameters schema.
type argNames struct {
	// Props and Required are the properties and the required names to tell monomind of.
	Props    map[string]json.RawMessage
	Required []string
	// Open says the schema lets arguments through that no property names: free-form keys
	// (additionalProperties that is true or a schema, a non-empty patternProperties), or a
	// reference that cannot be followed.
	Open bool
	// Overrun says the schema nests deeper, or holds more schemas, than is read.
	Overrun bool
}

// nameArguments says which arguments monomind has to be told of. monomind builds the
// shape of a call from the top-level properties of the schema alone and drops every other
// key from the call the client gets, so a schema that puts its arguments in the branches
// of a root anyOf, oneOf or allOf, in a then or an else, or behind a root $ref, would give
// the client {} at every call. The properties of those branches are named at the top level
// too: a branch that may or may not apply (anyOf, oneOf, if, then, else, dependentSchemas)
// makes what it names optional, one that applies (allOf, a $ref, the root itself) keeps
// the schema's required names required. A name that a required list mentions and nothing
// defines is let through as any value, and so is one that two branches define differently
// (monomind reads a property's type and an enum of strings, and nothing else of it).
//
// With no property to name and Open set, nothing about the arguments could be told to
// monomind: the caller refuses the function. With some named, the keys outside them are
// not passed on, which the docs say.
func nameArguments(params json.RawMessage) argNames {
	var doc map[string]any
	if len(params) == 0 || json.Unmarshal(params, &doc) != nil {
		return argNames{}
	}
	h := &hoister{doc: doc, props: map[string]json.RawMessage{}, views: map[string]string{},
		followed: map[string]int{}, mentioned: map[string]bool{}, required: map[string]bool{}}
	h.walk(doc, 0, false)
	out := argNames{Props: h.props, Open: h.open, Overrun: h.overrun}
	for _, name := range h.names {
		if _, has := h.props[name]; !has {
			h.props[name] = json.RawMessage("true")
		}
		if h.required[name] {
			out.Required = append(out.Required, name)
		}
	}
	return out
}

type hoister struct {
	doc      map[string]any // the whole schema, for the pointers of its references
	props    map[string]json.RawMessage
	views    map[string]string // what monomind reads of each property that is defined
	followed map[string]int    // the references read already, as which kind (the bits below)
	nodes    int
	open     bool
	overrun  bool
	// The names that required lists mention, once each and in the order they come, and
	// which of them are required of the call.
	names     []string
	mentioned map[string]bool
	required  map[string]bool
}

// The ways a reference is read: where the call may or may not have to satisfy it.
const (
	followedOptional = 1
	followedApplied  = 2
)

// walk reads one schema: its own properties and required names, then the schemas that go
// with it. optional says the schema is one of several that may apply, so nothing it
// requires is required of the call.
func (h *hoister) walk(node any, depth int, optional bool) {
	h.nodes++
	if depth > maxHoistDepth || h.nodes > maxHoistNodes {
		h.overrun = true
		return
	}
	m, ok := node.(map[string]any)
	if !ok {
		return // a boolean schema says nothing of arguments
	}
	if props, ok := m["properties"].(map[string]any); ok {
		for _, name := range sortedNames(props) {
			h.define(name, props[name])
		}
	}
	if list, ok := m["required"].([]any); ok {
		for _, v := range list {
			if name, ok := v.(string); ok {
				h.require(name, optional)
			}
		}
	}
	if ref, ok := m["$ref"].(string); ok {
		h.follow(ref, depth, optional)
	}
	for _, key := range []string{"$dynamicRef", "$recursiveRef"} {
		if _, has := m[key]; has {
			h.open = true // resolved at run time, by a scope that is not read
		}
	}
	h.walkEach(m["allOf"], depth, optional)
	for _, key := range []string{"anyOf", "oneOf"} {
		h.walkEach(m[key], depth, true)
	}
	for _, key := range []string{"if", "then", "else"} {
		if sub, has := m[key]; has {
			h.walk(sub, depth+1, true)
		}
	}
	if deps, ok := m["dependentSchemas"].(map[string]any); ok {
		for _, name := range sortedNames(deps) {
			h.walk(deps[name], depth+1, true)
		}
	}
	for _, key := range []string{"additionalProperties", "unevaluatedProperties"} {
		switch extra := m[key].(type) {
		case bool:
			h.open = h.open || extra
		case map[string]any:
			h.open = true
		}
	}
	if patterns, ok := m["patternProperties"].(map[string]any); ok && len(patterns) > 0 {
		h.open = true
	}
}

func (h *hoister) walkEach(branches any, depth int, optional bool) {
	list, _ := branches.([]any)
	for _, b := range list {
		h.walk(b, depth+1, optional)
	}
}

// follow reads what a local reference points at as if it stood where the reference does.
// A reference is read once for each way it is met, which ends a loop and keeps a schema
// that refers twice to the same definitions, level after level, from costing a path for
// each. One that leaves the document or points at nothing leaves arguments that nothing
// names.
func (h *hoister) follow(ref string, depth int, optional bool) {
	kind := followedApplied
	if optional {
		kind = followedOptional
	}
	have := h.followed[ref]
	if have&followedApplied != 0 || have&kind != 0 {
		return
	}
	h.followed[ref] = have | kind
	target, ok := h.resolve(ref)
	if !ok {
		h.open = true
		return
	}
	h.walk(target, depth+1, optional)
}

// resolve follows a JSON pointer in a fragment ("#/$defs/Args") through the document.
func (h *hoister) resolve(ref string) (any, bool) {
	ptr, local := strings.CutPrefix(ref, "#")
	if !local || (ptr != "" && ptr[0] != '/') {
		return nil, false
	}
	if ptr == "" {
		return h.doc, true
	}
	var cur any = h.doc
	for _, seg := range strings.Split(ptr[1:], "/") {
		if un, err := url.PathUnescape(seg); err == nil {
			seg = un
		}
		seg = strings.ReplaceAll(strings.ReplaceAll(seg, "~1", "/"), "~0", "~")
		switch c := cur.(type) {
		case map[string]any:
			next, ok := c[seg]
			if !ok {
				return nil, false
			}
			cur = next
		case []any:
			i, err := strconv.Atoi(seg)
			if err != nil || i < 0 || i >= len(c) {
				return nil, false
			}
			cur = c[i]
		default:
			return nil, false
		}
	}
	return cur, true
}

// define names a property. A name that is defined again keeps its first definition when
// the second says the same to monomind, and is let through as any value when it does not.
// What is not a schema (a branch's property is not checked as a top-level one is) is any
// value too.
func (h *hoister) define(name string, prop any) {
	raw := json.RawMessage("true")
	switch p := prop.(type) {
	case map[string]any:
		if b, err := json.Marshal(p); err == nil {
			raw = b
		}
	case bool:
		raw = json.RawMessage(strconv.FormatBool(p))
	}
	view := propertyView(prop)
	have, seen := h.props[name]
	switch {
	case !seen:
		h.props[name], h.views[name] = raw, view
	case h.views[name] != view && string(have) != "true":
		h.props[name], h.views[name] = json.RawMessage("true"), ""
	}
}

// require notes a name of a required list: it is an argument, and required of the call
// unless the schema it is in is one of several that may apply.
func (h *hoister) require(name string, optional bool) {
	if !h.mentioned[name] {
		h.mentioned[name] = true
		h.names = append(h.names, name)
	}
	if !optional {
		h.required[name] = true
	}
}

// propertyView is what monomind reads of a property: its type, and its enum when that
// lists strings only (toolSpecs leaves any other out).
func propertyView(prop any) string {
	p, ok := prop.(map[string]any)
	if !ok {
		return ""
	}
	typ, _ := json.Marshal(p["type"])
	view := string(typ)
	if list, ok := p["enum"].([]any); ok && len(list) > 0 {
		values := make([]string, 0, len(list))
		for _, v := range list {
			s, isString := v.(string)
			if !isString {
				return view
			}
			values = append(values, s)
		}
		slices.Sort(values)
		view += "|" + strings.Join(values, "\x00")
	}
	return view
}

func sortedNames[V any](m map[string]V) []string {
	names := make([]string, 0, len(m))
	for k := range m {
		names = append(names, k)
	}
	slices.Sort(names)
	return names
}
