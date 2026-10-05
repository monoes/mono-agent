package openaiapi

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"
)

// What the budget of a request is charged is everything that costs work, and each time it costs it: a
// schema read (every time it is read, so a schema that two paths lead to and that is read twice is
// charged twice), a reference followed, a property defined, a name that a list mentions (a required
// list, what a dependency asks for, the keys of a const or an enum) and, at the end, each name that is
// told to monomind, the entries of an enum that a property that holds for every call is compared by,
// and the members of a const or an enum. The first round charged the schemas, the references and the
// properties; the third review showed that a request of lists and enums did far more work than the
// budget said, before any slot was held.

// chargedFor is the steps that naming the arguments of one schema takes from a fresh budget.
func chargedFor(t *testing.T, params string) int {
	t.Helper()
	b := newHoistBudget()
	if got := nameArguments(json.RawMessage(params), b); got.Spent || got.TooDeep || got.TooWide || got.Unreadable {
		t.Fatalf("%s was not read: %+v", params, got)
	}
	return b.used
}

func TestTheBudgetIsChargedForEverythingThatCostsWork(t *testing.T) {
	for _, c := range []struct {
		name, params string
		steps        int
	}{
		{"a schema read", `{}`, 1},
		{"each schema of an allOf", `{"allOf":[{},{}]}`, 1 + 2},
		{"a property defined", `{"properties":{"a":{},"b":{}}}`, 1 + 2},
		{"a required name, when it is met and when monomind is told of it", `{"required":["a","b"]}`, 1 + 2 + 2},
		{"a name met again is charged again and told of once", `{"required":["a","a"]}`, 1 + 2 + 1},
		{"the entries of the enum of a property that holds for every call", `{"properties":{"a":{"enum":["x","y","z"]}}}`, 1 + 1 + 3},
		{"the entries of the enum of every definition that is compared", `{"allOf":[{"properties":{"a":{"enum":["x","y"]}}},{"properties":{"a":{"enum":["y","x"]}}}]}`, 1 + 2 + 2 + 2 + 2},
		{"an enum of a branch that may not apply is not compared", `{"anyOf":[{"properties":{"a":{"enum":["x","y","z"]}}}]}`, 1 + 1 + 1},
		{"a property and a required name that is it", `{"required":["a"],"properties":{"a":{"enum":["x","y","z"]}}}`, 1 + 1 + 3 + 1 + 1},
		{"a reference followed", `{"$ref":"#/$defs/A","$defs":{"A":{}}}`, 1 + 1 + 1},
		{"a definition that two references lead to is read once, and the second is still a reference followed",
			`{"allOf":[{"$ref":"#/$defs/A"},{"$ref":"#/$defs/A"}],"$defs":{"A":{"required":["x"]}}}`, 1 + 2 + 2 + 1 + 1 + 1},
		{"a definition read as optional and as applied is read twice, and charged twice",
			`{"allOf":[{"anyOf":[{"$ref":"#/$defs/A"}]},{"$ref":"#/$defs/A"}],"$defs":{"A":{"properties":{"p":{}},"allOf":[{"properties":{"q":{}}}]}}}`, 1 + 7 + 6},
		{"a definition read as applied is not read again as optional",
			`{"allOf":[{"$ref":"#/$defs/A"},{"anyOf":[{"$ref":"#/$defs/A"}]}],"$defs":{"A":{"properties":{"p":{}}}}}`, 1 + 4 + 3},
		{"the members of a const and the keys they name", `{"const":{"a":1,"b":2}}`, 1 + 1 + 2 + 2},
		{"the members of an enum, and a key met again", `{"enum":[{"a":1},{"a":2},5]}`, 1 + 3 + 2 + 1},
		{"what a dependency asks for, and the property that asks", `{"dependentRequired":{"a":["b","c"]}}`, 1 + 1 + 2 + 3},
		{"the schema of a dependency", `{"dependencies":{"a":{"properties":{"p":{}}}}}`, 1 + 1 + 1 + 1 + 1},
	} {
		if got := chargedFor(t, c.params); got != c.steps {
			t.Errorf("%s: %d steps charged, want %d", c.name, got, c.steps)
		}
	}
}

// requestOf is a request that declares copies functions with the given schema.
func requestOf(t *testing.T, params string, copies int) string {
	t.Helper()
	list := make([]string, 0, copies)
	for i := range copies {
		list = append(list, fmt.Sprintf(`{"type":"function","function":{"name":"f%d","parameters":%s}}`, i, params))
	}
	body := toolChatBody("claude", `"tools":[`+strings.Join(list, ",")+`]`, userHi)
	if int64(len(body)) > defaultBodyLimit {
		t.Fatalf("a body of %d bytes is more than the gateway takes (%d)", len(body), defaultBodyLimit)
	}
	return body
}

// namedLikeARequest names the arguments of copies functions with one budget, as a request does, and
// says how many were named before the budget ran out, and how many names they gave monomind.
func namedLikeARequest(params string, copies int) (b *hoistBudget, named, names int, spent bool) {
	b = newHoistBudget()
	for range copies {
		got := nameArguments(json.RawMessage(params), b)
		if got.Spent {
			return b, named, names, true
		}
		named++
		names += len(got.Props)
	}
	return b, named, names, false
}

// requiredNames is a schema that requires n names of three characters.
func requiredNames(n int) string {
	names := make([]string, 0, n)
	for i := range n {
		names = append(names, `"`+base36(i)+`"`)
	}
	return `{"type":"object","required":[` + strings.Join(names, ",") + `]}`
}

// A body of 36 functions that require 9,000 names each is 1.9 MB: it named 324,000 arguments, for
// 0.3 s and 75 MiB, against a budget of 100,000 steps, before a slot was held.
func TestTheRequiredNamesOfManyFunctionsAreBoundedByTheBudget(t *testing.T) {
	params := requiredNames(9000)
	b, named, names, spent := namedLikeARequest(params, 36)
	if !spent {
		t.Fatalf("36 functions of 9,000 required names were all named: %d names for a budget of %d steps", names, maxHoistSteps)
	}
	t.Logf("%d functions of 9,000 required names were named before the budget ran out: %d names, %d steps", named, names, b.used)
	if names > maxHoistSteps || b.used > maxHoistSteps {
		t.Errorf("%d functions were named, %d names, %d steps of %d", named, names, b.used, maxHoistSteps)
	}
	if _, _, _, spent := namedLikeARequest(params, 1); spent {
		t.Errorf("one function of 9,000 required names is within what is read")
	}

	begin := time.Now()
	err := validateChat(decodeRequest(t, requestOf(t, params, 36)))
	if took := time.Since(begin); took > 2*time.Second*slowdown {
		t.Errorf("the refusal took %v", took)
	}
	if err == nil || err.Code != "invalid_value" || !strings.HasSuffix(err.Param, ".function.parameters") || !strings.Contains(err.Message, "together") {
		t.Fatalf("36 functions of 9,000 required names: %+v, want 400 invalid_value on a function's parameters that says the functions together are too much", err)
	}
	if strings.Contains(string(err.body()), `"f`) {
		t.Errorf("the refusal names a function: %s", err.body())
	}
}

// enumLeaf is a schema that defines x with an enum of n distinct strings.
func enumLeaf(n int) string {
	values := make([]string, 0, n)
	for i := range n {
		values = append(values, `"`+base36(i)+`"`)
	}
	return `{"properties":{"x":{"type":"string","enum":[` + strings.Join(values, ",") + `]}}}`
}

// chainOf is a schema whose root is an allOf of a chain of allOfs (the leaf at the end of it) and of
// references to the levels of the chain that apply, so that each level, and the leaf under it, is read
// once more for each of them: the schema that a definition and a path to it can make of a leaf.
func chainOf(levels int, leaf string) string {
	inner := leaf
	for range levels {
		inner = `{"allOf":[` + inner + `]}`
	}
	body := strings.TrimSuffix(strings.TrimPrefix(inner, `{"allOf":[`), `]}`)
	path := "#/allOf/0"
	var refs []string
	for i := 1; i <= levels; i++ {
		if i >= 2 {
			refs = append(refs, `{"$ref":"`+path+`"}`)
		}
		path += "/allOf/0"
	}
	all := body
	if len(refs) > 0 {
		all += "," + strings.Join(refs, ",")
	}
	return `{"allOf":[` + all + `]}`
}

// An enum of 9,000 entries under a chain of 8 allOfs was compared (sorted) each time the leaf was read,
// up to 8 times for a function, 35 functions of it: 0.54 s against 0.14 s for one read, before a slot
// was held. Each entry compared is a step.
func TestTheEnumsOfManyFunctionsAreBoundedByTheBudget(t *testing.T) {
	params := chainOf(8, enumLeaf(9000))
	b, named, _, spent := namedLikeARequest(params, 35)
	if !spent {
		t.Fatalf("35 functions of an enum of 9,000 entries read 8 times were all named, %d steps charged of %d", b.used, maxHoistSteps)
	}
	t.Logf("%d functions of an enum of 9,000 entries were named before the budget ran out: %d steps", named, b.used)
	if b.used > maxHoistSteps {
		t.Errorf("%d steps charged of %d", b.used, maxHoistSteps)
	}
	if named*9000 > maxHoistSteps {
		t.Errorf("%d functions were named: each compared at least 9,000 entries, which is more than the budget", named)
	}
	if _, _, _, spent := namedLikeARequest(chainOf(1, enumLeaf(9000)), 1); spent {
		t.Errorf("one function of an enum of 9,000 entries is within what is read")
	}

	begin := time.Now()
	err := validateChat(decodeRequest(t, requestOf(t, params, 35)))
	if took := time.Since(begin); took > 2*time.Second*slowdown {
		t.Errorf("the refusal took %v", took)
	}
	if err == nil || err.Code != "invalid_value" || !strings.Contains(err.Message, "together") {
		t.Fatalf("35 functions of an enum of 9,000 entries: %+v, want 400 invalid_value that says the functions together are too much", err)
	}
}

// A schema that a path leads to again is read again, and each read is charged: the chain reads its leaf
// once per level that applies it, and that is the work, whatever the leaf holds.
func TestAReadOfASchemaIsChargedEachTimeItIsRead(t *testing.T) {
	once := chargedFor(t, chainOf(1, `{"properties":{"a":{},"b":{}}}`))
	many := chargedFor(t, chainOf(8, `{"properties":{"a":{},"b":{}}}`))
	if many < 4*once {
		t.Errorf("a leaf read 8 times for %d steps against %d for a leaf read once", many, once)
	}
}

// The budget takes exactly the steps asked for or none, and a request that asked for more than there
// was has nothing left.
func TestTheBudgetTakesWhatItIsAskedForOrNothingAndRunsOutOnce(t *testing.T) {
	b := &hoistBudget{left: 5}
	if !b.spend(2) || !b.spend(3) || b.left != 0 || b.used != 5 {
		t.Fatalf("5 steps in two spends: left %d, used %d", b.left, b.used)
	}
	if b.spend(1) {
		t.Errorf("a step was found in an empty budget")
	}
	b = &hoistBudget{left: 5}
	if b.spend(6) || b.left != 0 || b.used != 0 {
		t.Errorf("6 steps asked of 5: left %d, used %d, want none left and none used", b.left, b.used)
	}
	if b.spend(1) {
		t.Errorf("a step was found after a spend that asked for too much")
	}
	if b := (&hoistBudget{left: 5}); !b.spend(0) || b.left != 5 {
		t.Errorf("no steps are free to ask for")
	}
}
