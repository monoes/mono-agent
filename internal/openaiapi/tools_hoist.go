package openaiapi

import (
	"encoding/json"
	"net/url"
	"reflect"
	"slices"
	"strconv"
	"strings"
)

// How much of a schema is read when the arguments of a function are named. The schema is
// the client's: the nesting of one that fits in the size limit is not, so a schema that
// goes past either bound is refused, not named in part.
const (
	maxHoistDepth = 8       // levels of combinators and references followed
	maxHoistNodes = 2000    // schemas visited in one function
	maxHoistSteps = 100_000 // steps of work in all the functions of a request (hoistBudget says what a step is)
)

// hoistBudget is what the whole request may spend on naming arguments, and it is spent before anything
// starts (a slot is not held yet, so nothing else bounds it). A step is one of these, taken each time
// the work is done: a schema read (one that two paths lead to is read twice and charged twice), a
// reference followed, a property defined, a name that a list mentions (a required list, what a
// dependency asks for, the keys of a const or an enum), a member of a const or an enum, an entry of the
// enum of a property that holds for every call (they are compared with those of another definition),
// and each name told to monomind at the end. So however the schemas of a request refer to each other,
// and whatever lists and enums they hold, naming their arguments costs a bounded amount of work.
type hoistBudget struct {
	left     int
	used     int
	marshals int // properties marshalled: what the tests of the cost count
}

func newHoistBudget() *hoistBudget { return &hoistBudget{left: maxHoistSteps} }

// spend takes n steps from the budget and says whether there were that many; when there were not,
// there are none left.
func (b *hoistBudget) spend(n int) bool {
	if n > b.left {
		b.left = 0
		return false
	}
	b.left -= n
	b.used += n
	return true
}

// argNames is what nameArguments found in a parameters schema.
type argNames struct {
	// Props and Required are the properties and the required names to tell monomind of.
	Props    map[string]json.RawMessage
	Required []string
	// Open says the schema lets arguments through that no property names: free-form keys
	// (additionalProperties or unevaluatedProperties that is true or a schema, a non-empty
	// patternProperties), or a reference that cannot be followed ($dynamicRef, a $ref that is not
	// local or leads nowhere).
	Open bool
	// TooDeep says the schema nests combinators and references deeper than is read, and TooWide
	// that it holds more schemas than is read.
	TooDeep, TooWide bool
	// Spent says the request has spent what it may on naming arguments (hoistBudget).
	Spent bool
	// Unreadable says the schema could not be read as a JSON object at all.
	Unreadable bool
	// Impossible says a const or an enum that every call must match holds a value that is not an
	// object, and the arguments of a call are always one: no call could match.
	Impossible bool
}

// nameArguments says which arguments monomind has to be told of. monomind builds the
// shape of a call from the top-level properties of the schema alone and drops every other
// key from the call the client gets, so a schema that puts its arguments in the branches
// of a root anyOf, oneOf or allOf, in a then or an else, or behind a root $ref, would give
// the client {} at every call. The properties of those branches are named at the top level
// too: a branch that may or may not apply (anyOf, oneOf, if, then, else, dependentSchemas,
// dependencies) makes what it names optional, one that applies (allOf, a $ref, the root itself)
// keeps the schema's required names required; the names that dependentRequired and the lists of
// dependencies ask for, and the properties that ask, are arguments a call may carry, optional. monomind holds a call to the type and the enum of
// a property it is told of, so only what holds for every call gives them: a name that only a
// branch that may not apply defines is any value, as is a name that a required list mentions
// and nothing defines, and one that two definitions that hold for every call say differently
// (monomind reads a property's type and an enum of strings, and nothing else of it).
//
// A const or an enum of objects names the keys of its members too (optional, any value). With no
// property to name and Open set, nothing about the arguments could be told to monomind, and with
// a const or an enum that every call must match and that holds something other than an object no
// call could match (Impossible): the caller refuses the function. With some named, the keys
// outside them are not passed on, which the docs say.
func nameArguments(params json.RawMessage, budget *hoistBudget) argNames {
	if len(params) == 0 {
		return argNames{}
	}
	v, err := decodeKeepingNumbers(params)
	if err != nil {
		return argNames{Unreadable: true}
	}
	if v == nil {
		return argNames{} // null: no schema
	}
	doc, isObject := v.(map[string]any)
	if !isObject {
		return argNames{Unreadable: true}
	}
	if budget == nil {
		budget = newHoistBudget()
	}
	h := &hoister{doc: doc, budget: budget, props: map[string]json.RawMessage{}, views: map[string]string{}, sure: map[string]bool{},
		followed: map[uintptr]int{}, mentioned: map[string]bool{}, required: map[string]bool{}}
	h.walk(doc, 0, false)
	out := argNames{Props: h.props, Open: h.open, TooDeep: h.tooDeep, TooWide: h.tooWide, Spent: h.spent, Impossible: h.impossible}
	for _, name := range h.names {
		if !h.step() { // each name told to monomind costs a step, as it did when a list mentioned it
			out.Spent = true
			break
		}
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
	budget   *hoistBudget
	spent    bool           // the budget ran out
	doc      map[string]any // the whole schema, for the pointers of its references
	props    map[string]json.RawMessage
	views    map[string]string // what monomind reads of each property that is defined for every call
	sure     map[string]bool   // the names whose entry in props is such a definition, and not any value
	followed map[uintptr]int   // the schemas that references led to, read already, as which kind (the bits below)
	nodes    int
	open     bool
	tooDeep  bool
	tooWide  bool
	// impossible says a const or an enum that applies to every call holds a value that is no object.
	impossible bool
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
	if h.spent || h.tooDeep || h.tooWide || !h.step() {
		return
	}
	h.nodes++
	switch {
	case depth > maxHoistDepth:
		h.tooDeep = true
		return
	case h.nodes > maxHoistNodes:
		h.tooWide = true
		return
	}
	m, ok := node.(map[string]any)
	if !ok {
		return // a boolean schema says nothing of arguments
	}
	if props, ok := m["properties"].(map[string]any); ok {
		for _, name := range sortedNames(props) {
			h.define(name, props[name], optional)
		}
	}
	if list, ok := m["required"].([]any); ok {
		for _, v := range list {
			if name, ok := v.(string); ok {
				h.require(name, optional)
			}
		}
	}
	h.walkValues(m, optional)
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
	h.walkDependencies(m, depth)
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

// walkValues reads the const and the enum of a schema. The arguments of a call are an object, so the
// members that are objects name arguments: a call that is one of them carries those keys, and
// monomind would drop every one of them if no property named it. Where the schema applies to every
// call (the root, an allOf, a reference that applies) a member that is not an object leaves no call
// that could match, which the caller refuses; in a branch that may not apply it only leaves a branch
// that cannot.
func (h *hoister) walkValues(m map[string]any, optional bool) {
	var members []any
	if v, has := m["const"]; has {
		members = append(members, v)
	}
	if list, ok := m["enum"].([]any); ok {
		members = append(members, list...)
	}
	for _, v := range members {
		if !h.step() {
			return
		}
		obj, isObject := v.(map[string]any)
		switch {
		case isObject:
			for _, name := range sortedNames(obj) {
				h.require(name, true)
			}
		case !optional:
			h.impossible = true
		}
	}
}

// walkDependencies reads what a property asks for when it is there: dependentSchemas, and
// dependencies of draft-07 where an entry is a schema, bring a schema that may not apply (a
// branch), and dependentRequired, and dependencies where an entry is a list, ask for names. The
// property that asks and the names it asks for are arguments a call may carry, and none of them is
// asked of every call.
func (h *hoister) walkDependencies(m map[string]any, depth int) {
	for _, key := range []string{"dependentSchemas", "dependentRequired", "dependencies"} {
		deps, ok := m[key].(map[string]any)
		if !ok {
			continue
		}
		for _, name := range sortedNames(deps) {
			h.require(name, true)
			if asked, isList := deps[name].([]any); isList {
				for _, v := range asked {
					if s, ok := v.(string); ok {
						h.require(s, true)
					}
				}
				continue
			}
			h.walk(deps[name], depth+1, true)
		}
	}
}

// step charges one step of work to the budget of the request.
func (h *hoister) step() bool { return h.charge(1) }

// charge charges n steps of work to the budget of the request.
func (h *hoister) charge(n int) bool {
	if !h.budget.spend(n) {
		h.spent = true
		return false
	}
	return true
}

func (h *hoister) walkEach(branches any, depth int, optional bool) {
	list, _ := branches.([]any)
	for _, b := range list {
		h.walk(b, depth+1, optional)
	}
}

// follow reads what a local reference points at as if it stood where the reference does.
// The schema a reference leads to is read once for each way it is met, which ends a loop and
// keeps a schema that refers twice to the same definitions, level after level, from costing
// a path for each. It is the schema that is remembered, not the text of the reference: a
// pointer has as many spellings as it has percent-encodable characters and numbers that mean
// one index, and a memory of the spellings would read the schema again for each. One that
// leaves the document or points at nothing leaves arguments that nothing names.
func (h *hoister) follow(ref string, depth int, optional bool) {
	if !h.step() {
		return
	}
	target, ok := h.resolve(ref)
	if !ok {
		h.open = true
		return
	}
	m, isSchema := target.(map[string]any)
	if !isSchema {
		return // a boolean says nothing of arguments
	}
	kind := followedApplied
	if optional {
		kind = followedOptional
	}
	id := reflect.ValueOf(m).Pointer()
	have := h.followed[id]
	if have&followedApplied != 0 || have&kind != 0 {
		return
	}
	h.followed[id] = have | kind
	h.walk(m, depth+1, optional)
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

// define names a property. monomind holds every call to the type and the enum it is told of, and
// rejects a call that does not match, so a property is given them only where the schema is certain
// of them for every call: in the root's own properties, in an allOf and behind a reference that
// applies. A name that only a branch that may or may not apply defines (optional) is any value:
// a call that the schema allows may say what that branch does not. Two certain definitions that
// say different things to monomind give each other up, and any value is what is told; the first is
// kept when they agree. What is not a schema (a branch's property is not checked as a top-level
// one is) is any value too.
func (h *hoister) define(name string, prop any, optional bool) {
	if !h.step() {
		return
	}
	if optional {
		if _, seen := h.props[name]; !seen {
			h.props[name] = json.RawMessage("true")
		}
		return
	}
	if !h.charge(enumEntries(prop)) { // the entries are sorted to be compared with another definition's
		return
	}
	view := propertyView(prop)
	switch {
	case !h.sure[name]: // the first definition that holds for every call, after any that does not
		h.props[name], h.views[name], h.sure[name] = h.marshal(prop), view, true
	case h.views[name] != view:
		h.props[name], h.views[name] = json.RawMessage("true"), ""
	}
}

// marshal is a property as monomind is given it: only a name that is met for the first time
// needs it, and a schema may define the same name in thousands of branches.
func (h *hoister) marshal(prop any) json.RawMessage {
	switch p := prop.(type) {
	case map[string]any:
		h.budget.marshals++
		if b, err := json.Marshal(p); err == nil {
			return b
		}
	case bool:
		return json.RawMessage(strconv.FormatBool(p))
	}
	return json.RawMessage("true")
}

// require notes a name that a list mentions (a required list, what a dependency asks for, the
// keys of a const or an enum): it is an argument, and required of the call unless the schema it is
// in is one of several that may apply. Each mention costs a step.
func (h *hoister) require(name string, optional bool) {
	if !h.step() {
		return
	}
	if !h.mentioned[name] {
		h.mentioned[name] = true
		h.names = append(h.names, name)
	}
	if !optional {
		h.required[name] = true
	}
}

// enumEntries is how many entries the enum of a property has.
func enumEntries(prop any) int {
	if p, ok := prop.(map[string]any); ok {
		if list, ok := p["enum"].([]any); ok {
			return len(list)
		}
	}
	return 0
}

// propertyView is what monomind reads of a property: its type, and its enum when that
// lists strings only (toolSpecs leaves any other out).
func propertyView(prop any) string {
	p, ok := prop.(map[string]any)
	if !ok {
		return ""
	}
	view := "-" // no type
	switch t := p["type"].(type) {
	case nil:
	case string:
		view = "s:" + t
	default:
		view = "~" // a list of types, or anything else: monomind reads none of it
	}
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
