package workflow

import (
	"strings"
	"testing"
)

// #241: a missing field rendered as the literal "<no value>", which passed
// required-input checks — an org-granted x.find_by_keyword searched for
// "<no value>" and reported success.
func TestExpressionMissingValueRendersEmpty(t *testing.T) {
	engine := NewExpressionEngine()
	ctx := ExpressionContext{JSON: map[string]interface{}{
		"present": "yes", "null": nil, "limit": 5.0,
		"input": map[string]interface{}{"keywords": "golang"},
	}}
	cases := map[string]string{
		`{{ $json.missing }}`:                      "",
		`{{ $json.null }}`:                         "",
		`{{ .json.missing }}`:                      "",
		`{{ $json.input.missing }}`:                "",
		`{{ $json["missing"] }}`:                   "",
		`a{{ $json.missing }}b{{ $json.present }}`: "ab" + "yes",
		`{{ $json.input.keywords }}`:               "golang",
		`{{ if $json.nolimit }}{{ $json.nolimit }}{{ else }}20{{ end }}`: "20",
		`{{ if $json.limit }}{{ $json.limit }}{{ else }}20{{ end }}`:     "5",
		`{{ with $json.missing }}x{{ else }}none{{ end }}`:               "none",
		`{{ range $json.missing }}x{{ else }}empty{{ end }}`:             "empty",
		`{{ $x := $json.missing }}[{{ $x }}]`:                            "[]",
		`{{ json $json.missing }}`:                                       "null",
		`{{ default "d" $json.missing }}`:                                "d",
	}
	for tmpl, want := range cases {
		got, err := engine.EvaluateString(tmpl, ctx)
		if err != nil {
			t.Errorf("%s: %v", tmpl, err)
			continue
		}
		if got != want {
			t.Errorf("%s = %q, want %q", tmpl, got, want)
		}
		if strings.Contains(got, "<no value>") {
			t.Errorf("%s rendered <no value>", tmpl)
		}
	}
	// ResolveConfig, which node configs go through, agrees.
	cfg, err := engine.ResolveConfig(map[string]interface{}{"keywords": "{{ $json.keywords }}"}, ctx)
	if err != nil {
		t.Fatal(err)
	}
	if cfg["keywords"] != "" {
		t.Fatalf("resolved keywords = %#v, want empty", cfg["keywords"])
	}
}
