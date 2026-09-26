package schemagen

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/monoes/mono-agent/data"
)

type genInner struct {
	Label string `json:"label"`
}

type genBase struct {
	Base string `json:"base,omitempty"`
}

type genNode struct {
	genBase
	Name     string            `json:"name"`
	Kind     string            `json:"kind,omitempty"`
	Count    int               `json:"count,omitempty"`
	Ratio    float64           `json:"ratio,omitempty"`
	On       bool              `json:"on"`
	Tags     []string          `json:"tags,omitempty"`
	Inner    *genInner         `json:"inner,omitempty"`
	Children []genNode         `json:"children,omitempty"`
	Attrs    map[string]string `json:"attrs,omitempty"`
	Any      interface{}       `json:"any,omitempty"`
	Raw      json.RawMessage   `json:"raw,omitempty"`
	Skipped  string            `json:"-"`
	hidden   string
}

func genDoc(t *testing.T, spec Spec) map[string]any {
	t.Helper()
	b, err := Generate(spec)
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(b, &doc); err != nil {
		t.Fatal(err)
	}
	return doc
}

func TestGenerateStruct(t *testing.T) {
	_ = genNode{}.hidden
	doc := genDoc(t, Spec{
		ID:        "x",
		Root:      reflect.TypeOf(genNode{}),
		Enums:     map[string][]string{"genNode.kind": {"a", "b"}},
		Docs:      map[string]string{"genNode": "A node.", "genNode.Name": "The name."},
		FieldDocs: map[string]string{"genNode.on": "Switch.", "genNode.name": "ignored: Docs wins"},
		Optional:  map[string]bool{"genNode.on": true},
	})
	if doc["$schema"] != DraftURI || doc["$ref"] != "#/$defs/genNode" || doc["$id"] != "x" {
		t.Fatalf("root = %v", doc)
	}
	defs := doc["$defs"].(map[string]any)
	node := defs["genNode"].(map[string]any)
	if node["description"] != "A node." {
		t.Errorf("type description = %v", node["description"])
	}
	if got := node["required"]; !reflect.DeepEqual(got, []any{"name"}) {
		t.Errorf("required = %v, want [name] (omitempty and Optional excluded)", got)
	}
	props := node["properties"].(map[string]any)
	want := map[string]string{
		"base":     `{"type":"string"}`,
		"name":     `{"description":"The name.","type":"string"}`,
		"kind":     `{"enum":["a","b"],"type":"string"}`,
		"count":    `{"type":"integer"}`,
		"ratio":    `{"type":"number"}`,
		"on":       `{"description":"Switch.","type":"boolean"}`,
		"tags":     `{"items":{"type":"string"},"type":"array"}`,
		"inner":    `{"$ref":"#/$defs/genInner"}`,
		"children": `{"items":{"$ref":"#/$defs/genNode"},"type":"array"}`,
		"attrs":    `{"additionalProperties":{"type":"string"},"type":"object"}`,
		"any":      `{}`,
		"raw":      `{}`,
	}
	for k, w := range want {
		b, _ := json.Marshal(props[k])
		if string(b) != w {
			t.Errorf("%s = %s, want %s", k, b, w)
		}
	}
	if len(props) != len(want) {
		t.Errorf("properties = %v", props)
	}
	if _, ok := defs["genInner"]; !ok {
		t.Error("genInner not in $defs")
	}
}

func TestGenerateMapRootAndOverride(t *testing.T) {
	doc := genDoc(t, Spec{
		Root:      reflect.TypeOf(map[string]genInner{}),
		Overrides: map[string]map[string]any{"genInner.label": {"type": "string", "minLength": 1}},
	})
	if doc["type"] != "object" {
		t.Fatalf("root = %v", doc)
	}
	label := doc["$defs"].(map[string]any)["genInner"].(map[string]any)["properties"].(map[string]any)["label"].(map[string]any)
	if label["minLength"] != float64(1) {
		t.Errorf("override not applied: %v", label)
	}
}

func TestGenerateDeterministic(t *testing.T) {
	spec := Spec{Root: reflect.TypeOf(genNode{})}
	a, _ := Generate(spec)
	b, _ := Generate(spec)
	if !bytes.Equal(a, b) {
		t.Fatal("output differs between runs")
	}
}

func TestParseDocs(t *testing.T) {
	root, err := ModuleRoot(".")
	if err != nil {
		t.Fatal(err)
	}
	docs, err := ParseDocs(filepath.Join(root, "internal/action"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(docs["StepDef.SideEffect"], "SideEffect marks a step") {
		t.Errorf("StepDef.SideEffect doc = %q", docs["StepDef.SideEffect"])
	}
	if docs["SelectorEntry"] == "" {
		t.Error("SelectorEntry type doc missing")
	}
}

// TestSchemasUpToDate fails when data/schemas differs from what the Go
// structs generate. Fix: go generate ./internal/schemagen
func TestSchemasUpToDate(t *testing.T) {
	root, err := ModuleRoot(".")
	if err != nil {
		t.Fatal(err)
	}
	files, err := GenerateAll(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 4 {
		t.Fatalf("generated %d schemas, want 4", len(files))
	}
	for name, want := range files {
		got, err := os.ReadFile(filepath.Join(root, OutDir, name))
		if err != nil {
			t.Errorf("%s: %v (run: go generate ./internal/schemagen)", name, err)
			continue
		}
		if !bytes.Equal(got, want) {
			t.Errorf("%s is stale; run: go generate ./internal/schemagen", name)
		}
		embedded, err := data.SchemasFS.ReadFile("schemas/" + name)
		if err != nil || !bytes.Equal(embedded, got) {
			t.Errorf("%s: embedded copy differs from the file on disk", name)
		}
	}
}

// TestFieldDocsKeysExist keeps fieldDocs free of entries for renamed or
// removed fields.
func TestFieldDocsKeysExist(t *testing.T) {
	root, _ := ModuleRoot(".")
	files, err := GenerateAll(root)
	if err != nil {
		t.Fatal(err)
	}
	props := map[string]bool{}
	for _, b := range files {
		var doc struct {
			Defs map[string]struct {
				Properties map[string]any `json:"properties"`
			} `json:"$defs"`
		}
		if err := json.Unmarshal(b, &doc); err != nil {
			t.Fatal(err)
		}
		for typ, d := range doc.Defs {
			for p := range d.Properties {
				props[typ+"."+p] = true
			}
		}
	}
	for k := range fieldDocs {
		if !props[k] {
			t.Errorf("fieldDocs[%q]: no such property", k)
		}
	}
	for k := range actionEnums {
		if !props[k] {
			t.Errorf("actionEnums[%q]: no such property", k)
		}
	}
}

func TestTransformOpsFromDocTable(t *testing.T) {
	root, _ := ModuleRoot(".")
	ops, err := ParseDocTable(filepath.Join(root, "internal/action"), "TransformOp")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"map", "filter", "flag", "dedupe", "regex_extract", "parse_date", "parse_number",
		"lower", "replace", "join", "split", "pick", "limit", "sort", "tree_parent"} {
		if !slices.Contains(ops, want) {
			t.Errorf("op %q missing from %v", want, ops)
		}
	}
}
