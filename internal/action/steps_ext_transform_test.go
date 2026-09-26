package action

import (
	"reflect"
	"strings"
	"testing"
	"time"
)

var transformNow = time.Date(2026, 9, 25, 12, 0, 0, 0, time.UTC)

func txItems(ms ...map[string]interface{}) []interface{} {
	out := make([]interface{}, len(ms))
	for i, m := range ms {
		out[i] = m
	}
	return out
}

type txM = map[string]interface{}

func TestRunTransformOps(t *testing.T) {
	vars := txM{"base": "https://hn.test"}
	cases := []struct {
		name string
		src  interface{}
		ops  []TransformOp
		want interface{}
		err  string
	}{
		{
			name: "map adds fields from item and outer vars",
			src:  txItems(txM{"id": 1.0, "t": "A"}),
			ops:  []TransformOp{{Op: "map", Map: map[string]string{"url": "{{base}}/item?id={{id}}", "title": "{{item.t}}", "same": "{{id}}"}}},
			want: txItems(txM{"id": 1.0, "t": "A", "url": "https://hn.test/item?id=1", "title": "A", "same": 1.0}),
		},
		{
			name: "filter by condition",
			src:  txItems(txM{"score": "12 points"}, txM{"score": "300 points"}, txM{"other": 1.0}),
			ops:  []TransformOp{{Op: "filter", Where: &ConditionDef{Variable: "score", Operator: "greater_than", Value: 100}}},
			want: txItems(txM{"score": "300 points"}),
		},
		{
			name: "filter contains / exists / matches",
			src:  txItems(txM{"t": "Show HN: x"}, txM{"t": "Ask HN: y"}, txM{}),
			ops: []TransformOp{
				{Op: "filter", Where: &ConditionDef{Variable: "t", Operator: "exists"}},
				{Op: "filter", Where: &ConditionDef{Variable: "t", Operator: "matches", Value: "^Show"}},
			},
			want: txItems(txM{"t": "Show HN: x"}),
		},
		{
			name: "dedupe by field and whole item",
			src:  txItems(txM{"u": "a", "n": 1.0}, txM{"u": "a", "n": 2.0}, txM{"u": "b", "n": 1.0}, txM{"u": "b", "n": 1.0}),
			ops:  []TransformOp{{Op: "dedupe"}, {Op: "dedupe", Field: "u"}},
			want: txItems(txM{"u": "a", "n": 1.0}, txM{"u": "b", "n": 1.0}),
		},
		{
			name: "regex_extract with and without group",
			src:  txItems(txM{"href": "item?id=4242", "s": "abc123"}),
			ops: []TransformOp{
				{Op: "regex_extract", Field: "href", Pattern: `id=(\d+)`, To: "id"},
				{Op: "regex_extract", Field: "s", Pattern: `\d+`},
				{Op: "regex_extract", Field: "s", Pattern: `zzz`, To: "none"},
			},
			want: txItems(txM{"href": "item?id=4242", "s": "123", "id": "4242", "none": nil}),
		},
		{
			name: "parse_date auto, layout and relative",
			src:  txItems(txM{"a": "2026-01-02", "b": "02/01/2026 10:00", "c": "3 hours ago", "d": "garbage"}),
			ops: []TransformOp{
				{Op: "parse_date", Field: "a", Layout: "auto"},
				{Op: "parse_date", Field: "b", Layout: "02/01/2006 15:04"},
				{Op: "parse_date", Field: "c"},
				{Op: "parse_date", Field: "d", To: "d2"},
			},
			want: txItems(txM{"a": "2026-01-02T00:00:00Z", "b": "2026-01-02T10:00:00Z", "c": "2026-09-25T09:00:00Z", "d": "garbage", "d2": nil}),
		},
		{
			name: "parse_number",
			src:  txItems(txM{"a": "1.2k", "b": "3,400", "c": "12 points", "d": "5 minutes", "e": "n/a", "f": "2.5M"}),
			ops: []TransformOp{
				{Op: "parse_number", Field: "a"}, {Op: "parse_number", Field: "b"}, {Op: "parse_number", Field: "c"},
				{Op: "parse_number", Field: "d"}, {Op: "parse_number", Field: "e"}, {Op: "parse_number", Field: "f", To: "g"},
			},
			want: txItems(txM{"a": 1200.0, "b": 3400.0, "c": 12.0, "d": 5.0, "e": nil, "f": "2.5M", "g": 2500000.0}),
		},
		{
			name: "split then join",
			src:  txItems(txM{"tags": "go, rust,, zig"}),
			ops:  []TransformOp{{Op: "split", Field: "tags", To: "list"}, {Op: "join", Field: "list", Sep: "|", To: "joined"}},
			want: txItems(txM{"tags": "go, rust,, zig", "list": []interface{}{"go", "rust", "zig"}, "joined": "go|rust|zig"}),
		},
		{
			name: "pick and limit",
			src:  txItems(txM{"a": 1.0, "b": 2.0}, txM{"a": 3.0}, txM{"a": 4.0}),
			ops:  []TransformOp{{Op: "pick", Fields: []string{"a"}}, {Op: "limit", Count: 2}},
			want: txItems(txM{"a": 1.0}, txM{"a": 3.0}),
		},
		{
			name: "list of strings (empty field is the item)",
			src:  []interface{}{"id=1", "id=2", "id=1"},
			ops:  []TransformOp{{Op: "regex_extract", Pattern: `id=(\d)`}, {Op: "dedupe"}, {Op: "parse_number"}},
			want: []interface{}{1.0, 2.0},
		},
		{
			name: "single object in, object out",
			src:  txM{"x": "7"},
			ops:  []TransformOp{{Op: "parse_number", Field: "x"}},
			want: txM{"x": 7.0},
		},
		{
			name: "object filtered away is nil",
			src:  txM{"x": "7"},
			ops:  []TransformOp{{Op: "filter", Where: &ConditionDef{Variable: "x", Operator: "equals", Value: "8"}}},
			want: nil,
		},
		{name: "unknown op", src: txItems(txM{}), ops: []TransformOp{{Op: "explode"}}, err: `unknown op "explode"`},
		{name: "bad pattern", src: txItems(txM{}), ops: []TransformOp{{Op: "regex_extract", Pattern: "("}}, err: "pattern"},
		{name: "filter without where", src: txItems(txM{}), ops: []TransformOp{{Op: "filter"}}, err: "needs where"},
		{name: "map without map", src: txItems(txM{}), ops: []TransformOp{{Op: "map"}}, err: "needs a map"},
		{name: "pick without fields", src: txItems(txM{}), ops: []TransformOp{{Op: "pick"}}, err: "needs fields"},
		{name: "negative limit", src: txItems(txM{}), ops: []TransformOp{{Op: "limit", Count: -1}}, err: "limit"},
		{name: "bad operator", src: txItems(txM{"a": 1.0}), ops: []TransformOp{{Op: "filter", Where: &ConditionDef{Variable: "a", Operator: "near"}}}, err: "unknown operator"},
		{name: "scalar input", src: 42.0, err: "expected a list or an object"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := runTransform(c.src, c.ops, vars, transformNow)
			if c.err != "" {
				if err == nil || !strings.Contains(err.Error(), c.err) {
					t.Fatalf("err = %v, want %q", err, c.err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, c.want) {
				t.Fatalf("got  %#v\nwant %#v", got, c.want)
			}
		})
	}
}

func TestRunTransformDoesNotMutateSource(t *testing.T) {
	src := txItems(txM{"n": "5"})
	if _, err := runTransform(src, []TransformOp{{Op: "parse_number", Field: "n"}}, nil, transformNow); err != nil {
		t.Fatal(err)
	}
	if src[0].(txM)["n"] != "5" {
		t.Fatal("source item was mutated")
	}
}

func TestTransformStep(t *testing.T) {
	ae := newExtExecutor(t, &extPage{})
	ae.SetVariable("rows", txItems(txM{"p": "1.5k"}, txM{"p": "20"}))
	res := runExt(t, ae, StepDef{ID: "tr", Type: "transform", Input: "{{rows}}", VariableName: "out",
		Ops: []TransformOp{{Op: "parse_number", Field: "p"}, {Op: "filter", Where: &ConditionDef{Variable: "p", Operator: "greater_than", Value: "{{min}}"}}}})
	wantOK(t, res)
	// min is unset → the comparison has no number → nothing passes.
	if got := getVar(ae, "out").([]interface{}); len(got) != 0 {
		t.Fatalf("out = %v", got)
	}
	ae.SetVariable("min", 100)
	res = runExt(t, ae, StepDef{ID: "tr", Type: "transform", Input: "rows", VariableName: "out",
		Ops: []TransformOp{{Op: "parse_number", Field: "p"}, {Op: "filter", Where: &ConditionDef{Variable: "p", Operator: "greater_than", Value: "{{min}}"}}}})
	wantOK(t, res)
	if got := getVar(ae, "out").([]interface{}); len(got) != 1 || got[0].(txM)["p"] != 1500.0 {
		t.Fatalf("out = %v", got)
	}
	ae.SetVariable("jsonObj", `{"a":"1"}`)
	wantOK(t, runExt(t, ae, StepDef{ID: "tr", Type: "transform", Input: "{{jsonObj}}", Ops: []TransformOp{{Op: "parse_number", Field: "a"}}}))
	wantFail(t, runExt(t, ae, StepDef{ID: "tr", Type: "transform", Input: "{{missing}}"}), "resolved to nothing")
	wantFail(t, runExt(t, ae, StepDef{ID: "tr", Type: "transform", Input: "rows", Ops: []TransformOp{{Op: "nope"}}}), "unknown op")
}

func TestParseDateVariants(t *testing.T) {
	cases := map[string]string{
		"yesterday":                 "2026-09-24T12:00:00Z",
		"an hour ago":               "2026-09-25T11:00:00Z",
		"2 days ago":                "2026-09-23T12:00:00Z",
		"1758801600":                "2025-09-25T12:00:00Z",
		"Sep 3, 2026":               "2026-09-03T00:00:00Z",
		"2026-09-25T10:00:00+02:00": "2026-09-25T08:00:00Z",
	}
	for in, want := range cases {
		got, ok := parseDate(in, "", transformNow)
		if !ok || got.UTC().Format(time.RFC3339) != want {
			t.Errorf("%q: got %v ok=%v, want %s", in, got.UTC(), ok, want)
		}
	}
	if _, ok := parseDate("2026-13-40", "2006-01-02", transformNow); ok {
		t.Error("invalid date with explicit layout must fail")
	}
}

func TestTransformLowerAndReplace(t *testing.T) {
	src := txItems(txM{"typed": "Hello *World*", "shown": "hello   world"}, txM{"n": 5.0})
	got, err := runTransform(src, []TransformOp{
		{Op: "replace", Field: "typed", Pattern: `[*\s]`, To: "a"},
		{Op: "lower", Field: "a"},
		{Op: "replace", Field: "shown", Pattern: `\s+`, To: "b"},
		{Op: "replace", Field: "shown", Pattern: `(\w+)\s+(\w+)`, With: "$2 $1", To: "swapped"},
		{Op: "lower", Field: "missing", To: "m"},
	}, nil, transformNow)
	if err != nil {
		t.Fatal(err)
	}
	row := got.([]interface{})[0].(txM)
	if row["a"] != "helloworld" || row["b"] != "helloworld" || row["swapped"] != "world hello" || row["m"] != nil {
		t.Fatalf("row = %v", row)
	}
	if _, err := runTransform(src, []TransformOp{{Op: "replace", Pattern: "("}}, nil, transformNow); err == nil {
		t.Fatal("bad pattern must fail")
	}
}

func TestTransformTreeParent(t *testing.T) {
	rows := func(pairs ...interface{}) []interface{} {
		var out []interface{}
		for i := 0; i < len(pairs); i += 2 {
			out = append(out, txM{"id": pairs[i], "depth": pairs[i+1]})
		}
		return out
	}
	op := TransformOp{Op: "tree_parent", Field: "depth", ID: "id", Root: "{{itemID}}", Carry: "stack"}
	vars := map[string]interface{}{"itemID": "S"}

	// Page 1: a(0) b(1) c(2) d(1) e(0) f(1)
	got, err := runTransform(rows("a", "0", "b", "1", "c", "2", "d", "1", "e", "0", "f", "1"), []TransformOp{op}, vars, transformNow)
	if err != nil {
		t.Fatal(err)
	}
	var parents []interface{}
	for _, r := range got.([]interface{}) {
		parents = append(parents, r.(txM)["parentId"])
	}
	if want := []interface{}{"S", "a", "b", "a", "S", "e"}; !reflect.DeepEqual(parents, want) {
		t.Fatalf("parents = %v, want %v", parents, want)
	}
	// Page 2 continues the stack: g(2) is f's child, h(3) skips no level, i(5) jumps two levels.
	got, err = runTransform(rows("g", 2.0, "h", 3.0, "i", 5.0, "j", 1.0), []TransformOp{op}, vars, transformNow)
	if err != nil {
		t.Fatal(err)
	}
	parents = nil
	for _, r := range got.([]interface{}) {
		parents = append(parents, r.(txM)["parentId"])
	}
	if want := []interface{}{"f", "g", "h", "e"}; !reflect.DeepEqual(parents, want) {
		t.Fatalf("page 2 parents = %v, want %v", parents, want)
	}

	for name, bad := range map[string][]TransformOp{
		"no id":     {{Op: "tree_parent", Field: "depth"}},
		"bad depth": {{Op: "tree_parent", Field: "x", ID: "id"}},
	} {
		if _, err := runTransform(rows("a", "0"), bad, nil, transformNow); err == nil {
			t.Errorf("%s: want error", name)
		}
	}
}

func TestTransformStepCarriesTreeStack(t *testing.T) {
	ae := newExtExecutor(t, &extPage{})
	ae.SetVariable("itemID", 42.0)
	step := StepDef{ID: "tp", Type: "transform", Input: "page", VariableName: "out",
		Ops: []TransformOp{{Op: "tree_parent", Field: "indent", ID: "id", To: "parent", Root: "{{itemID}}", Carry: "hnStack"}}}
	ae.SetVariable("page", txItems(txM{"id": "1", "indent": "0"}))
	wantOK(t, runExt(t, ae, step))
	ae.SetVariable("page", txItems(txM{"id": "2", "indent": "1"}))
	wantOK(t, runExt(t, ae, step))
	out := getVar(ae, "out").([]interface{})
	if out[0].(txM)["parent"] != "1" {
		t.Fatalf("second page parent = %v, want 1 (carried)", out[0].(txM)["parent"])
	}
	if st, _ := getVar(ae, "hnStack").([]interface{}); len(st) != 2 {
		t.Fatalf("carried stack = %v", getVar(ae, "hnStack"))
	}
}

func TestTransformFlag(t *testing.T) {
	src := txItems(txM{"id": "1", "text": "hi"}, txM{"id": "2"}, txM{"id": "3", "text": ""})
	got, err := runTransform(src, []TransformOp{
		{Op: "flag", Where: &ConditionDef{Variable: "text", Operator: "not_exists"}, To: "deleted"},
		{Op: "flag", Where: &ConditionDef{Variable: "id", Operator: "equals", Value: "{{want}}"}, To: "match"},
	}, map[string]interface{}{"want": "3"}, transformNow)
	if err != nil {
		t.Fatal(err)
	}
	var deleted, match []interface{}
	for _, r := range got.([]interface{}) {
		deleted = append(deleted, r.(txM)["deleted"])
		match = append(match, r.(txM)["match"])
	}
	if !reflect.DeepEqual(deleted, []interface{}{false, true, false}) || !reflect.DeepEqual(match, []interface{}{false, false, true}) {
		t.Fatalf("deleted=%v match=%v", deleted, match)
	}
	for _, bad := range []TransformOp{{Op: "flag", To: "x"}, {Op: "flag", Where: &ConditionDef{Variable: "id", Operator: "exists"}}} {
		if _, err := runTransform(src, []TransformOp{bad}, nil, transformNow); err == nil {
			t.Errorf("%+v: want error", bad)
		}
	}
}

func TestTransformSort(t *testing.T) {
	src := func() []interface{} {
		return txItems(
			txM{"id": "9", "k": "a"}, txM{"id": 10.0, "k": "b"}, txM{"k": "none1"},
			txM{"id": "100", "k": "c"}, txM{"id": "9", "k": "d"}, txM{"k": "none2"}, txM{"id": "x", "k": "e"},
		)
	}
	keys := func(v interface{}) string {
		var out []string
		for _, r := range v.([]interface{}) {
			out = append(out, r.(txM)["k"].(string))
		}
		return strings.Join(out, ",")
	}
	// Numbers numerically ("9" < 10 < "100"), "x" vs numbers by string,
	// equal keys keep input order (a before d), missing last in both orders.
	got, err := runTransform(src(), []TransformOp{{Op: "sort", Field: "id"}}, nil, transformNow)
	if err != nil {
		t.Fatal(err)
	}
	if k := keys(got); k != "a,d,b,c,e,none1,none2" {
		t.Fatalf("asc = %s", k)
	}
	got, err = runTransform(src(), []TransformOp{{Op: "sort", Field: "id", Order: "desc"}}, nil, transformNow)
	if err != nil {
		t.Fatal(err)
	}
	if k := keys(got); k != "e,c,b,a,d,none1,none2" {
		t.Fatalf("desc = %s", k)
	}
	// Highest matching id: sort desc, limit 1.
	got, err = runTransform(txItems(txM{"id": "41"}, txM{"id": "402"}, txM{"id": "7"}),
		[]TransformOp{{Op: "sort", Field: "id", Order: "desc"}, {Op: "limit", Count: 1}}, nil, transformNow)
	if err != nil || got.([]interface{})[0].(txM)["id"] != "402" {
		t.Fatalf("highest = %v, %v", got, err)
	}
	// Plain values (empty field) sort too.
	got, _ = runTransform([]interface{}{"b", "a", "c"}, []TransformOp{{Op: "sort"}}, nil, transformNow)
	if !reflect.DeepEqual(got, []interface{}{"a", "b", "c"}) {
		t.Fatalf("strings = %v", got)
	}
	if _, err := runTransform(src(), []TransformOp{{Op: "sort", Field: "id", Order: "up"}}, nil, transformNow); err == nil {
		t.Fatal("bad order must fail")
	}
}

func TestTransformArithmetic(t *testing.T) {
	src := txItems(txM{"px": "80"}, txM{"px": "100px"}, txM{"px": 20.0}, txM{"px": "n/a"}, txM{})
	got, err := runTransform(src, []TransformOp{
		{Op: "divide", Field: "px", By: 40, Round: true, To: "depth"},
		{Op: "divide", Field: "px", By: 40, To: "raw"},
		{Op: "multiply", Field: "px", By: 0.5, To: "half"},
		{Op: "add", Field: "px", By: 1, To: "plus"},
		{Op: "subtract", Field: "px", By: 30, To: "minus"},
	}, nil, transformNow)
	if err != nil {
		t.Fatal(err)
	}
	rows := got.([]interface{})
	want := []txM{
		{"depth": 2.0, "raw": 2.0, "half": 40.0, "plus": 81.0, "minus": 50.0},
		{"depth": 3.0, "raw": 2.5, "half": 50.0, "plus": 101.0, "minus": 70.0},
		{"depth": 1.0, "raw": 0.5, "half": 10.0, "plus": 21.0, "minus": -10.0},
		{"depth": nil, "raw": nil, "half": nil, "plus": nil, "minus": nil},
		{"depth": nil, "raw": nil, "half": nil, "plus": nil, "minus": nil},
	}
	for i, w := range want {
		for k, v := range w {
			if rows[i].(txM)[k] != v {
				t.Errorf("row %d %s = %v, want %v", i, k, rows[i].(txM)[k], v)
			}
		}
	}
	// Coalescing fallback: indent when present, else the computed depth.
	got, _ = runTransform(txItems(txM{"indent": "0", "px": "80"}, txM{"px": "80"}), []TransformOp{
		{Op: "divide", Field: "px", By: 40, Round: true, To: "depthPx"},
		{Op: "map", Map: map[string]string{"depth": "{{indent or depthPx}}"}},
	}, nil, transformNow)
	if d0, d1 := got.([]interface{})[0].(txM)["depth"], got.([]interface{})[1].(txM)["depth"]; d0 != "0" || d1 != 2.0 {
		t.Fatalf("coalesced depths = %v, %v", d0, d1)
	}
	if _, err := runTransform(src, []TransformOp{{Op: "divide", Field: "px"}}, nil, transformNow); err == nil {
		t.Fatal("divide without by must fail")
	}
}
