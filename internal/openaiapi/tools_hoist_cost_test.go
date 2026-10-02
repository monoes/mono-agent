package openaiapi

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"
)

// What naming the arguments costs is bounded by what the request says, whatever its schemas
// refer to: a reference is read once however it is spelled, a property is marshalled once
// however many branches define it, and the whole request has one budget, not one for each
// function. The tests count the work (the steps the budget was charged) and add one coarse
// bound on time, so that a slow machine does not fail them and a quadratic one cannot pass.

// spelled is the nth spelling of the pointer "#/$defs/AAAAAAAAAA": each letter is either itself
// or its percent-encoding (1,024 spellings of one pointer).
func spelled(n int) string {
	var b strings.Builder
	b.WriteString("#/$defs/")
	for bit := range 10 {
		if n&(1<<bit) != 0 {
			b.WriteString("%41")
		} else {
			b.WriteString("A")
		}
	}
	return b.String()
}

// manySpellings is a schema whose root has an anyOf of n references to one definition of
// props properties, each reference spelled another way.
func manySpellings(props, n int) string {
	branches := make([]string, 0, n)
	for i := range n {
		branches = append(branches, `{"$ref":"`+spelled(i)+`"}`)
	}
	properties := make([]string, 0, props)
	for i := range props {
		properties = append(properties, fmt.Sprintf(`"p%d":{"type":"string"}`, i))
	}
	return `{"anyOf":[` + strings.Join(branches, ",") + `],"$defs":{"AAAAAAAAAA":{"properties":{` + strings.Join(properties, ",") + `}}}}`
}

func TestAReferenceIsReadOnceHoweverItIsSpelled(t *testing.T) {
	params := manySpellings(300, 900)
	b := newHoistBudget()
	begin := time.Now()
	got := nameArguments(json.RawMessage(params), b)
	if got.TooDeep || got.TooWide || got.Spent || len(got.Props) != 300 {
		t.Fatalf("the 300 properties of the definition must be named: %d named, overrun %v, spent %v", len(got.Props), got.TooDeep || got.TooWide, got.Spent)
	}
	// 900 branches and 900 references, and the definition read once: a few thousand steps. Reading
	// it for each spelling is 270,000.
	if b.used > 3*900+300+10 {
		t.Errorf("%d steps for 900 spellings of one reference", b.used)
	}
	if took := time.Since(begin); took > 2*time.Second {
		t.Errorf("took %v", took)
	}
}

// 1,000 spellings of the index of an array: the same schema.
func TestSpellingsOfAnArrayIndexAreOneReference(t *testing.T) {
	properties := make([]string, 0, 300)
	for i := range 300 {
		properties = append(properties, fmt.Sprintf(`"p%d":{}`, i))
	}
	params := `{"anyOf":[{"properties":{` + strings.Join(properties, ",") + `}},{"$ref":"#/anyOf/0"},{"$ref":"#/anyOf/00"},{"$ref":"#/anyOf/+0"},{"$ref":"#/anyOf/000"},{"$ref":"#/anyOf/%30"}]}`
	b := newHoistBudget()
	if got := nameArguments(json.RawMessage(params), b); len(got.Props) != 300 {
		t.Fatalf("%d properties named", len(got.Props))
	}
	// The branch that is the definition, and the one time the references read it.
	if b.used > 2*300+60 {
		t.Errorf("%d steps: every spelling of the index read the definition again", b.used)
	}
}

// A name that many branches define is marshalled when it is first met, not each time.
func TestAPropertyThatManyBranchesDefineIsMarshalledOnce(t *testing.T) {
	big := make([]string, 0, 50)
	for i := range 50 {
		big = append(big, fmt.Sprintf(`"k%d":{"type":"string","description":"%s"}`, i, strings.Repeat("d", 40)))
	}
	branches := make([]string, 0, 300)
	for range 300 {
		branches = append(branches, `{"properties":{"x":{"type":"object","properties":{`+strings.Join(big, ",")+`}}}}`)
	}
	b := newHoistBudget()
	got := nameArguments(json.RawMessage(`{"anyOf":[`+strings.Join(branches, ",")+`]}`), b)
	if len(got.Props) != 1 {
		t.Fatalf("%d properties named", len(got.Props))
	}
	if b.marshals != 1 {
		t.Errorf("the property was marshalled %d times for 300 definitions", b.marshals)
	}
}

// The definitions of a schema may refer to each other twice, level after level: the work is the
// number of definitions, not the number of paths through them.
func TestSharedDefinitionsCostWhatTheyAre(t *testing.T) {
	var defs []string
	for i := range 40 {
		defs = append(defs, fmt.Sprintf(`"D%d":{"properties":{"p%d":{"type":"string"}},"allOf":[{"$ref":"#/$defs/D%d"},{"$ref":"#/$defs/D%d"}]}`, i, i, i+1, i+1))
	}
	defs = append(defs, `"D40":{"properties":{"last":{"type":"string"}}}`)
	b := newHoistBudget()
	nameArguments(json.RawMessage(`{"$ref":"#/$defs/D0","$defs":{`+strings.Join(defs, ",")+`}}`), b)
	if b.used > 1000 {
		t.Errorf("%d steps for 41 definitions", b.used)
	}
}

// The budget is the request's: functions that are each within what one may spend are refused
// when they are too many together.
func TestTheBudgetIsOfTheWholeRequestNotOfEachFunction(t *testing.T) {
	properties := make([]string, 0, 1000)
	for i := range 1000 {
		properties = append(properties, fmt.Sprintf(`"p%d":{}`, i))
	}
	params := json.RawMessage(`{"properties":{` + strings.Join(properties, ",") + `}}`)
	b := &hoistBudget{left: 3010}
	var spent []bool
	for range 6 {
		spent = append(spent, nameArguments(params, b).Spent)
	}
	if fmt.Sprint(spent) != "[false false false true true true]" {
		t.Errorf("which of six functions of 1,000 properties each ran past a budget of 3,010 steps: %v", spent)
	}
	if b.used > 3010 {
		t.Errorf("%d steps were spent of a budget of 3,010", b.used)
	}
}

// 32 functions with a schema of 57 KiB of references spelled in 900 ways each, in a body of
// 1.85 MiB, took 9.6 s before anything started; and a request of more than the budget's worth
// of properties is refused, whatever it spends them on.
func TestARequestThatIsMostlySchemaCostsLittleAndTooMuchIsRefused(t *testing.T) {
	tools := make([]string, 0, 32)
	for i := range 32 {
		tools = append(tools, fmt.Sprintf(`{"type":"function","function":{"name":"f%d","parameters":%s}}`, i, manySpellings(300, 900)))
	}
	begin := time.Now()
	if err := validateChat(decodeRequest(t, toolBody(`"tools":[`+strings.Join(tools, ",")+`]`, userHi))); err != nil {
		t.Errorf("32 functions of 900 spellings each: %+v", err)
	}
	if took := time.Since(begin); took > 3*time.Second {
		t.Errorf("32 functions of 900 spellings each took %v", took)
	}

	// Dense: 7,000 minimal properties in a function, enough of them to run past the budget.
	properties := make([]string, 0, 7000)
	for i := range 7000 {
		properties = append(properties, fmt.Sprintf(`"%s":{}`, base36(i)))
	}
	dense := `{"type":"object","properties":{` + strings.Join(properties, ",") + `}}`
	if len(dense) > maxToolSchema {
		t.Fatalf("the schema of a function has %d bytes", len(dense))
	}
	denseTools := func(n int) string {
		tools = tools[:0]
		for i := range n {
			tools = append(tools, fmt.Sprintf(`{"type":"function","function":{"name":"g%d","parameters":%s}}`, i, dense))
		}
		return toolBody(`"tools":[`+strings.Join(tools, ",")+`]`, userHi)
	}
	if err := validateChat(decodeRequest(t, denseTools(12))); err != nil { // 84,000 properties
		t.Errorf("12 functions of 7,000 properties are within what is read: %+v", err)
	}
	begin = time.Now()
	err := validateChat(decodeRequest(t, denseTools(16))) // 112,000
	if err == nil || err.Code != "invalid_value" || !strings.HasSuffix(err.Param, ".function.parameters") || !strings.Contains(err.Message, "together") {
		t.Fatalf("16 functions of 7,000 properties: %+v, want 400 invalid_value on a function's parameters that says the functions together are too much", err)
	}
	if strings.Contains(string(err.body()), `"g`) {
		t.Errorf("the refusal names a function: %s", err.body())
	}
	if took := time.Since(begin); took > 3*time.Second {
		t.Errorf("the refusal took %v", took)
	}
}

// What costs no property still costs: schemas visited and references followed are charged as
// well, so that a request of branches that name nothing is bounded like one that names much.
func TestSchemasVisitedAndReferencesFollowedAreChargedToo(t *testing.T) {
	empties := strings.Repeat("{},", 1899) + "{}"
	refs := strings.TrimSuffix(strings.Repeat(`{"$ref":"#/$defs/A"},`, 900), ",")
	for name, c := range map[string]struct {
		params      string
		fits, spent int // functions that are within the budget and functions that are not
	}{
		"schemas that name nothing":       {`{"anyOf":[` + empties + `]}`, 40, 60},
		"references to one definition":    {`{"anyOf":[` + refs + `],"$defs":{"A":{}}}`, 40, 80},
		"allOf of schemas, one reference": {`{"allOf":[` + empties + `,{"$ref":"#/$defs/A"}],"$defs":{"A":{}}}`, 40, 60},
	} {
		tools := func(n int) string {
			var list []string
			for i := range n {
				list = append(list, fmt.Sprintf(`{"type":"function","function":{"name":"f%d","parameters":%s}}`, i, c.params))
			}
			return toolBody(`"tools":[`+strings.Join(list, ",")+`]`, userHi)
		}
		if err := validateChat(decodeRequest(t, tools(c.fits))); err != nil {
			t.Errorf("%s: %d functions: %+v", name, c.fits, err)
		}
		if err := validateChat(decodeRequest(t, tools(c.spent))); err == nil || !strings.Contains(err.Message, "together") {
			t.Errorf("%s: %d functions must be more than the request may spend: %+v", name, c.spent, err)
		}
	}
}

// What clients really send is far from the budget: the most functions, each with a schema of a
// hundred properties and a few branches.
func TestALargeLegitimateRequestIsNotRefused(t *testing.T) {
	properties := make([]string, 0, 100)
	for i := range 100 {
		properties = append(properties, fmt.Sprintf(`"p%d":{"type":"string","description":"A property."}`, i))
	}
	params := `{"type":"object","properties":{` + strings.Join(properties, ",") + `},"required":["p1"],"anyOf":[{"required":["p2"]},{"required":["p3"]}]}`
	var list []string
	for i := range 128 {
		list = append(list, fmt.Sprintf(`{"type":"function","function":{"name":"f%d","parameters":%s}}`, i, params))
	}
	if err := validateChat(decodeRequest(t, toolBody(`"tools":[`+strings.Join(list, ",")+`]`, userHi))); err != nil {
		t.Errorf("128 functions of 100 properties: %+v", err)
	}
}

// Four levels of definitions, as deep as is read, each an anyOf of 40 references to the next
// and an allOf of one, under a root that applies the first and may apply it: 40^4 paths
// through them, and a read of each definition once as optional and once as applied.
func TestAnyOfFanOutOverSharedDefinitionsIsLinear(t *testing.T) {
	refs := func(to string, n int) string {
		return strings.TrimSuffix(strings.Repeat(`{"$ref":"#/$defs/`+to+`"},`, n), ",")
	}
	var defs []string
	for i := range 3 {
		next := fmt.Sprintf("D%d", i+1)
		defs = append(defs, fmt.Sprintf(`"D%d":{"properties":{"p%d":{"type":"string"}},"anyOf":[%s],"allOf":[%s]}`, i, i, refs(next, 40), refs(next, 1)))
	}
	defs = append(defs, `"D3":{"properties":{"last":{"type":"string"}}}`)
	params := `{"anyOf":[` + refs("D0", 40) + `],"allOf":[` + refs("D0", 1) + `],"$defs":{` + strings.Join(defs, ",") + `}}`

	b := newHoistBudget()
	begin := time.Now()
	got := nameArguments(json.RawMessage(params), b)
	if got.Spent || got.TooDeep || got.TooWide || len(got.Props) != 4 {
		t.Fatalf("p0, p1, p2 and last are named: %v, spent %v, overrun %v", keysOf(got.Props), got.Spent, got.TooDeep || got.TooWide)
	}
	// Each definition is read at most twice and a read costs the 41 branches and the 41
	// references they hold: 4 definitions, 2 reads and about 100 steps.
	if b.used > 4*2*100 {
		t.Errorf("%d steps for four definitions", b.used)
	}
	if took := time.Since(begin); took > 2*time.Second {
		t.Errorf("took %v", took)
	}
}

// adversarialParams is a schema of about 15 KiB that is nothing but references: an anyOf of
// branches that are each a pointer to one definition spelled another way, and a definition of
// 20 properties.
func adversarialParams(spellings int) string {
	branches := make([]string, 0, spellings)
	for i := range spellings {
		branches = append(branches, `{"$ref":"`+spelled(i%1024)+`"}`)
	}
	properties := make([]string, 0, 20)
	for i := range 20 {
		properties = append(properties, fmt.Sprintf(`"p%d":{"type":"string"}`, i))
	}
	return `{"anyOf":[` + strings.Join(branches, ",") + `],"$defs":{"AAAAAAAAAA":{"properties":{` + strings.Join(properties, ",") + `}}}}`
}

// The most functions in the largest body, over HTTP: the request is answered at once whatever
// it holds, and what naming its arguments costs is counted in steps that the budget bounds (the
// reviewer's request took 9.6 s of validation before a slot was held).
func TestTheMostFunctionsInTheLargestBodyAreAnsweredAtOnce(t *testing.T) {
	for _, c := range []struct {
		name      string
		spellings int // of the one reference in each of 128 functions: 792 steps for 385
		refused   bool
	}{
		{"within the budget", 360, false},
		{"past the budget", 385, true},
	} {
		params := adversarialParams(c.spellings)
		list := make([]string, 0, maxTools)
		for i := range maxTools {
			list = append(list, fmt.Sprintf(`{"type":"function","function":{"name":"f%d","parameters":%s}}`, i, params))
		}
		body := toolChatBody("claude", `"tools":[`+strings.Join(list, ",")+`]`, userHi)
		if len(body) < 1_800_000 || int64(len(body)) > defaultBodyLimit {
			t.Fatalf("%s: the body has %d bytes: it must be a body the gateway takes, near its limit", c.name, len(body))
		}

		// The work: the budget is never passed, and it is run out exactly when the request is too much.
		b := newHoistBudget()
		spent := 0
		for range maxTools {
			if nameArguments(json.RawMessage(params), b).Spent {
				spent++
			}
		}
		if b.used > maxHoistSteps || (spent > 0) != c.refused {
			t.Errorf("%s: %d functions ran past the budget, %d steps were spent of %d", c.name, spent, b.used, maxHoistSteps)
		}

		h := toolHarness(t, answers("done"))
		secret := h.key(t, "default", "app", false)
		begin := time.Now()
		rec := post(h, anyPolicy, secret, body)
		if took := time.Since(begin); took > 3*time.Second {
			t.Errorf("%s: the request took %v", c.name, took)
		}
		switch {
		case c.refused && (rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), `"invalid_value"`) || !strings.Contains(rec.Body.String(), "together")):
			t.Errorf("%s: status %d, body %.300s", c.name, rec.Code, rec.Body)
		case !c.refused && rec.Code != http.StatusOK:
			t.Errorf("%s: status %d, body %.300s", c.name, rec.Code, rec.Body)
		}
	}
}

func base36(n int) string {
	const digits = "0123456789abcdefghijklmnopqrstuvwxyz"
	return string([]byte{digits[n/1296%36], digits[n/36%36], digits[n%36]})
}
