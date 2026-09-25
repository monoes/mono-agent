package action

import (
	"reflect"
	"strings"
	"testing"
)

func TestExtractTableWithFields(t *testing.T) {
	page := &extPage{url: "https://news.test/list", elems: map[string]*extElem{"#stories": {}},
		eval: func(js string) (interface{}, error) {
			if !strings.Contains(js, `"#stories"`) || !strings.Contains(js, `"a.title@href"`) || !strings.Contains(js, `"tr.athing"`) {
				t.Fatalf("unexpected script: %s", js)
			}
			return jsOut(map[string]interface{}{"rows": []interface{}{
				map[string]interface{}{"title": "One", "link": "/item?id=1"},
				map[string]interface{}{"title": "Two", "link": "https://x.test/2"},
				map[string]interface{}{"title": "Three", "link": "/item?id=3"},
			}}), nil
		}}
	ae := newExtExecutor(t, page)
	res := runExt(t, ae, StepDef{ID: "t", Type: "extract_table", Selector: "#stories", Value: "tr.athing", BatchSize: 2,
		Fields: map[string]string{"title": "a.title", "link": "a.title@href"}, VariableName: "stories"})
	wantOK(t, res)
	rows := res.Data.([]map[string]interface{})
	if len(rows) != 2 {
		t.Fatalf("batchSize cap: got %d rows", len(rows))
	}
	if rows[0]["link"] != "https://news.test/item?id=1" || rows[1]["link"] != "https://x.test/2" {
		t.Fatalf("links not absolutised: %v", rows)
	}
	if len(ae.execCtx.ExtractedItems) != 2 || !ae.execCtx.ListOutput {
		t.Fatalf("rows must be records: %v", ae.execCtx.ExtractedItems)
	}
	if !reflect.DeepEqual(getVar(ae, "stories"), rows) {
		t.Fatal("variable not stored")
	}
}

func TestExtractTableHeadersAndErrors(t *testing.T) {
	page := &extPage{elems: map[string]*extElem{"//table": {}, "#list": {}}, eval: func(js string) (interface{}, error) {
		if strings.Contains(js, `"#list"`) {
			return jsOut(map[string]interface{}{"__error": "fields are required unless the element is a <table>"}), nil
		}
		if !strings.Contains(js, "isX = true") {
			t.Fatalf("xpath selector should be evaluated as XPath: %s", js)
		}
		return jsOut(map[string]interface{}{"rows": []interface{}{map[string]interface{}{"Name": "Ada", "Age": "36"}}}), nil
	}}
	ae := newExtExecutor(t, page)
	res := runExt(t, ae, StepDef{ID: "t", Type: "extract_table", XPath: "//table"})
	wantOK(t, res)
	if rows := res.Data.([]map[string]interface{}); rows[0]["Name"] != "Ada" {
		t.Fatalf("rows = %v", rows)
	}
	wantFail(t, runExt(t, ae, StepDef{ID: "t", Type: "extract_table", Selector: "#list"}), "fields are required")
	wantFail(t, runExt(t, ae, StepDef{ID: "t", Type: "extract_table", Selector: "#absent", Timeout: 0.1}), "element not found")
	wantFail(t, runExt(t, ae, StepDef{ID: "t", Type: "extract_table"}), "no selector")
}

func TestExtractJSON(t *testing.T) {
	page := &extPage{elems: map[string]*extElem{"script#__NEXT_DATA__": {}}, eval: func(js string) (interface{}, error) {
		return jsOut(map[string]interface{}{"text": `{"props":{"items":[{"id":1},{"id":2}]}}`}), nil
	}}
	ae := newExtExecutor(t, page)
	res := runExt(t, ae, StepDef{ID: "j", Type: "extract_json", Selector: "script#__NEXT_DATA__", Path: "props.items[1].id", VariableName: "id"})
	wantOK(t, res)
	if getVar(ae, "id") != 2.0 {
		t.Fatalf("id = %v", getVar(ae, "id"))
	}

	ae.SetVariable("raw", `{"a":{"b":[10,20]}}`)
	res = runExt(t, ae, StepDef{ID: "j", Type: "extract_json", Input: "{{raw}}", Path: "a.b[-1]"})
	wantOK(t, res)
	if res.Data != 20.0 {
		t.Fatalf("data = %v", res.Data)
	}
	ae.SetVariable("obj", map[string]interface{}{"k": "v"})
	res = runExt(t, ae, StepDef{ID: "j", Type: "extract_json", Input: "obj", Path: "k"})
	wantOK(t, res)
	if res.Data != "v" {
		t.Fatalf("data = %v", res.Data)
	}

	ae.SetVariable("bad", "not json")
	wantFail(t, runExt(t, ae, StepDef{ID: "j", Type: "extract_json", Input: "{{bad}}"}), "not JSON")
	wantFail(t, runExt(t, ae, StepDef{ID: "j", Type: "extract_json", Input: "{{raw}}", Path: "a.c"}), `no key "c"`)
	wantFail(t, runExt(t, ae, StepDef{ID: "j", Type: "extract_json", Input: "{{nothing}}"}), "resolved to nothing")
	wantFail(t, runExt(t, ae, StepDef{ID: "j", Type: "extract_json"}), "needs a selector or an input")
}

func TestJSONPath(t *testing.T) {
	doc := map[string]interface{}{"a": map[string]interface{}{"b": []interface{}{
		map[string]interface{}{"c": "x"}, []interface{}{1.0, 2.0},
	}}}
	cases := []struct {
		path string
		want interface{}
		err  string
	}{
		{"", doc, ""},
		{"$.a.b[0].c", "x", ""},
		{"a.b[1][0]", 1.0, ""},
		{"a.b[5]", nil, "out of range"},
		{"a.b.c", nil, "not an object"},
		{"a[0]", nil, "not an array"},
		{"a.b[x]", nil, "bad index"},
		{"a.b[0", nil, "unclosed"},
	}
	for _, c := range cases {
		got, err := jsonPath(doc, c.path)
		if c.err != "" {
			if err == nil || !strings.Contains(err.Error(), c.err) {
				t.Errorf("%q: err %v, want %q", c.path, err, c.err)
			}
			continue
		}
		if err != nil || !reflect.DeepEqual(got, c.want) {
			t.Errorf("%q: got %v, %v", c.path, got, err)
		}
	}
	arr := []interface{}{map[string]interface{}{"n": 1.0}}
	if got, _ := jsonPath(arr, "[0].n"); got != 1.0 {
		t.Errorf("[0].n = %v", got)
	}
}

func TestExtractStepsResolvePackageSelectors(t *testing.T) {
	page := &extPage{elems: map[string]*extElem{"#stories": {}, "script#data": {}}, eval: func(js string) (interface{}, error) {
		if !strings.Contains(js, "rowSel") {
			if !strings.Contains(js, `"script#data"`) {
				t.Fatalf("extract_json used the wrong selector: %s", js)
			}
			return jsOut(map[string]interface{}{"text": `{"n":3}`}), nil
		}
		if !strings.Contains(js, `"#stories"`) {
			t.Fatalf("extract_table used the wrong selector: %s", js)
		}
		return jsOut(map[string]interface{}{"rows": []interface{}{map[string]interface{}{"t": "A"}}}), nil
	}}
	ae := newExtExecutor(t, page)
	obs := &obsRecorder{}
	ae.SetSelectorObserver(obs)
	ae.SetPackage(&extPkg{id: "news", selectors: map[string]*SelectorEntry{
		"story_list": {Candidates: []SelectorCandidate{{CSS: "#old-stories"}, {CSS: "#stories"}}},
		"state":      {Candidates: []SelectorCandidate{{CSS: "script#data"}}},
		"gone":       {Candidates: []SelectorCandidate{{CSS: "#nope"}}},
	}})

	res := runExt(t, ae, StepDef{ID: "t", Type: "extract_table", ConfigKey: "story_list", Fields: map[string]string{"t": ""}, Timeout: 1})
	wantOK(t, res)
	res = runExt(t, ae, StepDef{ID: "j", Type: "extract_json", ConfigKey: "state", Path: "n", Timeout: 1})
	wantOK(t, res)
	if res.Data != 3.0 {
		t.Fatalf("data = %v", res.Data)
	}
	wantFail(t, runExt(t, ae, StepDef{ID: "t", Type: "extract_table", ConfigKey: "gone", Timeout: 0.2}), "resolved to no selector")

	want := []string{"news/story_list idx=1 ok=true healed=true", "news/state idx=0 ok=true healed=false", "news/gone idx=-1 ok=false healed=false"}
	if !reflect.DeepEqual(obs.got, want) {
		t.Fatalf("observations = %v", obs.got)
	}
}
