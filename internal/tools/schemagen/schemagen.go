// Package schemagen generates internal/workflow/schemas/<node-type>.json
// files from `schema:"..."` struct tags on Go structs, instead of hand
// writing that JSON.
//
// # Why a companion struct, not the node's runtime config
//
// Every node in internal/nodes/**/*.go reads its configuration directly out
// of a raw map[string]interface{} passed into Execute — none of them
// declare a typed Go config struct today. Introducing typed config structs
// and rewiring every Execute method to use them is a much larger, riskier
// change than generating schemas, and is out of scope here (see
// internal/workflow/schemas/README.md for the migration status).
//
// Instead, each generated node type gets a small companion struct — e.g.
// SetNodeSchema next to SetNode in internal/nodes/control/set_schema.go —
// whose only purpose is to describe the schema. It is never constructed or
// used at runtime; Execute keeps reading from the map exactly as before.
// Field order in the struct is the field order in the generated JSON.
//
// # Tag grammar
//
// The `schema:"..."` tag on each struct field is a comma-separated list of
// key or key=value pairs:
//
//	schema:"label=Field,type=text,required,help=The field to set,placeholder={{ $json.x }}"
//
// Recognized keys (all optional unless noted):
//
//	key                override the JSON key (default: the field's `json:"..."`
//	                   tag name, else snake_case of the Go field name)
//	label              -> NodeSchemaField.Label
//	type               -> NodeSchemaField.Type (text, textarea, number, boolean,
//	                   select, code, resource_picker, ...) — REQUIRED
//	required           presence-only flag -> NodeSchemaField.Required = true
//	default            -> NodeSchemaField.Default; parsed as bool when type=boolean,
//	                   as float64 when type=number, else kept as a string
//	placeholder        -> NodeSchemaField.Placeholder
//	help               -> NodeSchemaField.Help
//	options            pipe-separated list -> NodeSchemaField.Options
//	                   (e.g. options=GET|POST|PUT)
//	language           -> NodeSchemaField.Language (code-editor fields)
//	rows               integer -> NodeSchemaField.Rows
//	min / max          float -> NodeSchemaField.Min / Max (*float64)
//	item_type          -> NodeSchemaField.ItemType
//	depends_on_key     sibling field key -> NodeSchemaField.DependsOn.Key
//	depends_on_values  pipe-separated list -> NodeSchemaField.DependsOn.Values
//
// A struct field with no `schema` tag is skipped entirely — not every Go
// field needs to be user-configurable.
//
// Escaping: struct tag values are unquoted by Go's own strconv.Unquote
// before this package ever sees them (that's how reflect.StructTag.Get
// works), so a backslash escape like `\,` either gets rejected as an
// invalid Go escape sequence (silently dropping the whole tag — see
// reflect.StructTag.Lookup) or gets resolved before this package's own
// splitter can see it. A literal comma or pipe inside a value must
// therefore use the full-width lookalike character instead of the ASCII
// one: '，' (U+FF0C) for a comma, '｜' (U+FF5C) for a pipe. Both are
// substituted back to their ASCII form after splitting. This is
// intentionally a minimal, regular tag grammar — not a DSL — so the parser
// below is a small hand-written scanner, not a grammar/parser generator.
//
// # credential_platform
//
// NodeSchema.CredentialPlatform is set from a struct-level doc comment line
// of the exact form:
//
//	// credential_platform: slack
//
// placed directly above the struct declaration. Omit it (the common case)
// for nodes with no credential platform, which leaves CredentialPlatform nil
// (JSON null), matching LoadDefaultSchema's existing default.
package schemagen

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"reflect"
	"strconv"
	"strings"
	"unicode"

	"github.com/monoes/mono-agent/internal/workflow"
)

// GenerateSchema parses goFile (a path relative to the repo root, or
// absolute) for a struct type named structName and builds the NodeSchema it
// describes via `schema:"..."` tags.
func GenerateSchema(goFile, structName string) (*workflow.NodeSchema, error) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, goFile, nil, parser.ParseComments)
	if err != nil {
		return nil, fmt.Errorf("schemagen: parse %s: %w", goFile, err)
	}

	for _, decl := range f.Decls {
		gd, ok := decl.(*ast.GenDecl)
		if !ok || gd.Tok != token.TYPE {
			continue
		}
		for _, spec := range gd.Specs {
			ts, ok := spec.(*ast.TypeSpec)
			if !ok || ts.Name.Name != structName {
				continue
			}
			st, ok := ts.Type.(*ast.StructType)
			if !ok {
				return nil, fmt.Errorf("schemagen: %s.%s is not a struct", goFile, structName)
			}
			return buildSchema(st, docComment(gd, ts))
		}
	}
	return nil, fmt.Errorf("schemagen: struct %s not found in %s", structName, goFile)
}

// docComment prefers the TypeSpec's own doc comment (Go 1.20+, single-spec
// GenDecl) and falls back to the GenDecl's doc comment (grouped `type ( ... )`
// blocks, or older toolchains).
func docComment(gd *ast.GenDecl, ts *ast.TypeSpec) *ast.CommentGroup {
	if ts.Doc != nil {
		return ts.Doc
	}
	return gd.Doc
}

func buildSchema(st *ast.StructType, doc *ast.CommentGroup) (*workflow.NodeSchema, error) {
	schema := &workflow.NodeSchema{Fields: []workflow.NodeSchemaField{}}

	if doc != nil {
		for _, c := range doc.List {
			text := strings.TrimSpace(strings.TrimPrefix(c.Text, "//"))
			if platform, ok := strings.CutPrefix(text, "credential_platform:"); ok {
				p := strings.TrimSpace(platform)
				schema.CredentialPlatform = &p
			}
		}
	}

	for _, field := range st.Fields.List {
		if field.Tag == nil {
			continue
		}
		tagVal, err := strconv.Unquote(field.Tag.Value)
		if err != nil {
			return nil, fmt.Errorf("schemagen: bad tag literal %s: %w", field.Tag.Value, err)
		}
		rawSchema := reflect.StructTag(tagVal).Get("schema")
		if rawSchema == "" {
			continue
		}
		jsonKey := reflect.StructTag(tagVal).Get("json")
		if i := strings.Index(jsonKey, ","); i >= 0 {
			jsonKey = jsonKey[:i]
		}

		names := field.Names
		if len(names) == 0 {
			// Embedded field with a tag — not a supported case; skip.
			continue
		}
		for _, name := range names {
			sf, err := parseFieldTag(rawSchema, jsonKey, name.Name)
			if err != nil {
				return nil, fmt.Errorf("schemagen: field %s: %w", name.Name, err)
			}
			schema.Fields = append(schema.Fields, sf)
		}
	}
	return schema, nil
}

func parseFieldTag(rawSchema, jsonKey, goName string) (workflow.NodeSchemaField, error) {
	pairs := strings.Split(rawSchema, ",")

	sf := workflow.NodeSchemaField{Key: jsonKey}
	if sf.Key == "" {
		sf.Key = snakeCase(goName)
	}

	var (
		dependsOnKey    string
		dependsOnValues []string
		haveDependsOn   bool
		defaultRaw      string
		haveDefault     bool
	)

	for _, pair := range pairs {
		key, value, hasValue := strings.Cut(pair, "=")
		key = strings.TrimSpace(key)
		// options/depends_on_values must split on the raw (un-unescaped)
		// value first — unescaping would turn an escaped literal pipe '｜'
		// into a real '|' before the split ever sees it. Every other key is
		// scalar, so it's safe to unescape immediately.
		switch key {
		case "key":
			sf.Key = unescape(value)
		case "label":
			sf.Label = unescape(value)
		case "type":
			sf.Type = unescape(value)
		case "required":
			sf.Required = true
		case "default":
			defaultRaw = unescape(value)
			haveDefault = true
		case "placeholder":
			sf.Placeholder = unescape(value)
		case "help":
			sf.Help = unescape(value)
		case "options":
			sf.Options = splitOptions(value)
		case "language":
			sf.Language = unescape(value)
		case "rows":
			n, err := strconv.Atoi(value)
			if err != nil {
				return sf, fmt.Errorf("rows: %w", err)
			}
			sf.Rows = n
		case "min":
			v, err := strconv.ParseFloat(value, 64)
			if err != nil {
				return sf, fmt.Errorf("min: %w", err)
			}
			sf.Min = &v
		case "max":
			v, err := strconv.ParseFloat(value, 64)
			if err != nil {
				return sf, fmt.Errorf("max: %w", err)
			}
			sf.Max = &v
		case "item_type":
			sf.ItemType = unescape(value)
		case "depends_on_key":
			dependsOnKey = unescape(value)
			haveDependsOn = true
		case "depends_on_values":
			dependsOnValues = splitOptions(value)
			haveDependsOn = true
		default:
			if !hasValue {
				return sf, fmt.Errorf("unrecognized schema tag flag %q", key)
			}
			return sf, fmt.Errorf("unrecognized schema tag key %q", key)
		}
	}

	if sf.Type == "" {
		return sf, fmt.Errorf("missing required \"type=\" in schema tag")
	}
	if haveDefault {
		def, err := coerceDefault(defaultRaw, sf.Type)
		if err != nil {
			return sf, fmt.Errorf("default: %w", err)
		}
		sf.Default = def
	}
	if haveDependsOn {
		sf.DependsOn = &workflow.FieldDependency{Key: dependsOnKey, Values: dependsOnValues}
	}
	return sf, nil
}

// coerceDefault converts the raw string default value into the Go type that
// json.Marshal will render matching how hand-written schema JSON already
// encodes defaults for boolean/number fields (native JSON bool/number, not a
// quoted string).
func coerceDefault(raw, fieldType string) (interface{}, error) {
	switch fieldType {
	case "boolean":
		b, err := strconv.ParseBool(raw)
		if err != nil {
			return nil, err
		}
		return b, nil
	case "number":
		f, err := strconv.ParseFloat(raw, 64)
		if err != nil {
			return nil, err
		}
		return f, nil
	default:
		return raw, nil
	}
}

// splitOptions splits a pipe-separated list value (options=, depends_on_values=)
// on ASCII '|', then unescapes each piece.
func splitOptions(s string) []string {
	if s == "" {
		return nil
	}
	parts := strings.Split(s, "|")
	for i, p := range parts {
		parts[i] = unescape(p)
	}
	return parts
}

// unescape resolves the full-width lookalikes ('，' U+FF0C, '｜' U+FF5C) that
// stand in for a literal comma/pipe inside a tag value — see the package doc
// comment for why an ASCII backslash escape doesn't work here.
func unescape(s string) string {
	s = strings.ReplaceAll(s, "，", ",")
	s = strings.ReplaceAll(s, "｜", "|")
	return s
}

// snakeCase converts a Go exported field name (e.g. "IncludeInput") to
// snake_case ("include_input"). Only used as a fallback when a field has no
// `json:"..."` tag.
func snakeCase(s string) string {
	var b strings.Builder
	for i, r := range s {
		if unicode.IsUpper(r) {
			if i > 0 {
				b.WriteByte('_')
			}
			b.WriteRune(unicode.ToLower(r))
		} else {
			b.WriteRune(r)
		}
	}
	return b.String()
}
