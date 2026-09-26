// Package schemagen generates the JSON Schemas (draft 2020-12) for browser
// automation packages from the Go structs that read them, so the schema an
// editor validates against and the code that runs a package cannot drift.
//
// The shipped schemas live in data/schemas and are regenerated with
//
//	go generate ./internal/schemagen
//
// TestSchemasUpToDate fails when the committed files differ from what the
// structs produce. See spec §4.5.
package schemagen

//go:generate go run ./cmd/gen

import (
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"time"
)

// DraftURI is the "$schema" of every generated schema.
const DraftURI = "https://json-schema.org/draft/2020-12/schema"

// Spec describes one schema to generate.
type Spec struct {
	// File is the output file name, e.g. "action.v1.schema.json".
	File string
	// ID is the schema "$id".
	ID string
	// Title and Description annotate the root.
	Title       string
	Description string
	// Root is the Go type the document decodes into (a struct, or a map of
	// structs).
	Root reflect.Type
	// Enums restricts string fields: "TypeName.jsonName" → allowed values.
	Enums map[string][]string
	// Overrides replace the generated schema of a field: "TypeName.jsonName"
	// → schema. A description from Docs is still added when missing.
	Overrides map[string]map[string]any
	// Optional marks fields optional even though their json tag has no
	// omitempty (legacy fields a newer alternative replaces).
	Optional map[string]bool
	// Docs maps "TypeName" and "TypeName.GoField" to descriptions (usually
	// parsed from struct comments, see ParseDocs).
	Docs map[string]string
	// FieldDocs fills descriptions the struct comments do not give:
	// "TypeName.jsonName" → description.
	FieldDocs map[string]string
}

// generator carries per-run state: the $defs collected so far.
type generator struct {
	spec  Spec
	defs  map[string]map[string]any
	names map[reflect.Type]string
}

var (
	rawMessageType = reflect.TypeOf(json.RawMessage{})
	timeType       = reflect.TypeOf(time.Time{})
)

// Generate returns the schema document for spec as indented JSON with a
// trailing newline. Output is deterministic (encoding/json sorts map keys).
func Generate(spec Spec) ([]byte, error) {
	g := &generator{spec: spec, defs: map[string]map[string]any{}, names: map[reflect.Type]string{}}
	root, err := g.schemaFor(spec.Root, "")
	if err != nil {
		return nil, err
	}
	doc := map[string]any{"$schema": DraftURI}
	for k, v := range root {
		doc[k] = v
	}
	if spec.ID != "" {
		doc["$id"] = spec.ID
	}
	if spec.Title != "" {
		doc["title"] = spec.Title
	}
	if spec.Description != "" {
		doc["description"] = spec.Description
	}
	if len(g.defs) > 0 {
		doc["$defs"] = g.defs
	}
	out, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(out, '\n'), nil
}

// schemaFor returns the schema of t. field is "TypeName.jsonName" when t is
// a struct field's type (used for enums), else "".
func (g *generator) schemaFor(t reflect.Type, field string) (map[string]any, error) {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	switch {
	case t == rawMessageType:
		return map[string]any{}, nil
	case t == timeType:
		return map[string]any{"type": "string", "format": "date-time"}, nil
	}
	switch t.Kind() {
	case reflect.Interface:
		return map[string]any{}, nil
	case reflect.String:
		s := map[string]any{"type": "string"}
		if vals, ok := g.spec.Enums[field]; ok {
			s["enum"] = vals
		}
		return s, nil
	case reflect.Bool:
		return map[string]any{"type": "boolean"}, nil
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return map[string]any{"type": "integer"}, nil
	case reflect.Float32, reflect.Float64:
		return map[string]any{"type": "number"}, nil
	case reflect.Slice, reflect.Array:
		items, err := g.schemaFor(t.Elem(), field)
		if err != nil {
			return nil, err
		}
		return map[string]any{"type": "array", "items": items}, nil
	case reflect.Map:
		if t.Key().Kind() != reflect.String {
			return nil, fmt.Errorf("schemagen: %s: map key must be a string", t)
		}
		vals, err := g.schemaFor(t.Elem(), "")
		if err != nil {
			return nil, err
		}
		return map[string]any{"type": "object", "additionalProperties": vals}, nil
	case reflect.Struct:
		name, err := g.define(t)
		if err != nil {
			return nil, err
		}
		return map[string]any{"$ref": "#/$defs/" + name}, nil
	}
	return nil, fmt.Errorf("schemagen: unsupported type %s", t)
}

// define adds struct t to $defs (once) and returns its name.
func (g *generator) define(t reflect.Type) (string, error) {
	if name, ok := g.names[t]; ok {
		return name, nil
	}
	name := t.Name()
	if name == "" {
		return "", fmt.Errorf("schemagen: anonymous struct types are not supported")
	}
	for other, n := range g.names {
		if n == name {
			return "", fmt.Errorf("schemagen: $defs name %q used by both %s and %s", name, other, t)
		}
	}
	g.names[t] = name
	def := map[string]any{"type": "object"}
	g.defs[name] = def // before recursing, so self-references resolve

	props := map[string]any{}
	var required []string
	if err := g.addFields(t, name, props, &required); err != nil {
		return "", err
	}
	def["properties"] = props
	if len(required) > 0 {
		sort.Strings(required)
		def["required"] = required
	}
	if d := g.spec.Docs[name]; d != "" {
		def["description"] = d
	}
	return name, nil
}

// addFields collects t's JSON properties, flattening embedded structs the
// way encoding/json does.
func (g *generator) addFields(t reflect.Type, typeName string, props map[string]any, required *[]string) error {
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		tag := f.Tag.Get("json")
		if tag == "-" {
			continue
		}
		jsonName, opts, _ := strings.Cut(tag, ",")
		if f.Anonymous && jsonName == "" {
			ft := f.Type
			if ft.Kind() == reflect.Pointer {
				ft = ft.Elem()
			}
			if ft.Kind() == reflect.Struct {
				if err := g.addFields(ft, typeName, props, required); err != nil {
					return err
				}
				continue
			}
		}
		if !f.IsExported() {
			continue
		}
		if jsonName == "" {
			jsonName = f.Name
		}
		key := typeName + "." + jsonName
		var s map[string]any
		if o, ok := g.spec.Overrides[key]; ok {
			s = copyMap(o)
		} else {
			var err error
			if s, err = g.schemaFor(f.Type, key); err != nil {
				return fmt.Errorf("%s.%s: %w", typeName, f.Name, err)
			}
		}
		d := g.spec.Docs[typeName+"."+f.Name]
		if d == "" {
			d = g.spec.FieldDocs[key]
		}
		if d != "" && s["description"] == nil {
			s["description"] = d // 2020-12 allows annotations next to $ref
		}
		props[jsonName] = s
		if !strings.Contains(","+opts+",", ",omitempty,") && !g.spec.Optional[key] {
			*required = append(*required, jsonName)
		}
	}
	return nil
}

func copyMap(m map[string]any) map[string]any {
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}
