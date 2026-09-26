package schemagen

import (
	"fmt"
	"path/filepath"
	"reflect"

	"github.com/monoes/mono-agent/internal/action"
	"github.com/monoes/mono-agent/internal/automation"
)

// SchemaBaseURI prefixes every generated "$id".
const SchemaBaseURI = "https://raw.githubusercontent.com/monoes/mono-agent/master/data/schemas/"

// OutDir is where the schemas are written, relative to the module root.
const OutDir = "data/schemas"

// StepTypes is every step type the action engine runs: the original
// handlers (executor.go initHandlers) plus the package-era ones
// (steps_ext*.go). It is the enum of StepDef.type in action.v1.
var StepTypes = []string{
	// core
	"navigate", "wait", "refresh", "find_element", "click", "type", "upload",
	"scroll", "hover", "extract_text", "extract_attribute", "extract_multiple",
	"condition", "update_progress", "save_data", "mark_failed", "log",
	"call_bot_method", "set_variable",
	// package era (spec §6.1)
	"call_fragment", "call_action", "for_each", "wait_for", "assert",
	"select_option", "press_key", "extract_table", "extract_json",
	"transform", "page_script", "http_fetch_in_page", "download",
}

// SideEffectLevels are the allowed ActionDef.sideEffects values, weakest
// first.
var SideEffectLevels = []string{"none", "read", "write", "message", "destructive"}

// docDirs are the packages whose struct comments become descriptions.
var docDirs = []string{"internal/action", "internal/automation"}

// actionEnums apply to every schema that contains action structs.
var actionEnums = map[string][]string{
	"StepDef.type":          StepTypes,
	"ActionDef.sideEffects": SideEffectLevels,
	"ActionDef.visibility":  action.VisibilityKinds,
	// less_than, not_contains and matches are transform "where" only.
	"ConditionDef.operator":  {"exists", "not_exists", "equals", "not_equals", "greater_than", "less_than", "contains", "not_contains", "matches"},
	"ErrorHandlerDef.action": {"retry", "try_alternative", "mark_failed", "skip", "continue", "abort"},
	"SuccessAction.action":   {"set_variable", "increment", "save_data", "update_progress"},
}

// inputItem is one entry of inputs.required / inputs.optional: a bare name
// (legacy) or an object describing the input.
var inputItem = map[string]any{
	"oneOf": []any{
		map[string]any{"type": "string", "description": "Input name (legacy form)."},
		map[string]any{
			"type":     "object",
			"required": []string{"name"},
			"properties": map[string]any{
				"name":        map[string]any{"type": "string", "description": "Variable name, used as {{name}} in step templates."},
				"type":        map[string]any{"type": "string", "description": "Value type.", "enum": []string{"string", "number", "boolean", "list", "array", "object", "select", "secret", "file"}},
				"description": map[string]any{"type": "string"},
				"default":     map[string]any{"description": "Value used when the input is not given."},
				"min":         map[string]any{"type": "number"},
				"max":         map[string]any{"type": "number"},
				"options":     map[string]any{"type": "array", "description": "Allowed values for a select input."},
				"enum":        map[string]any{"type": "array", "description": "Allowed values (alias of options)."},
				"format":      map[string]any{"type": "string", "description": "Value format hint, e.g. email, uri."},
				"aliases":     map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Other variable names that also supply this input (config fields older workflows set); the first non-empty one fills it when the input is not given."},
				"ui": map[string]any{
					"type":        "object",
					"description": "Form hints; forms are generated from inputs.",
					"properties": map[string]any{
						"label":       map[string]any{"type": "string"},
						"placeholder": map[string]any{"type": "string"},
						"help":        map[string]any{"type": "string"},
						"rows":        map[string]any{"type": "integer"},
					},
				},
			},
		},
	},
}

var actionOverrides = map[string]map[string]any{
	"InputDef.required":      {"type": "array", "items": inputItem, "description": "Inputs the caller must provide."},
	"InputDef.optional":      {"type": "array", "items": inputItem, "description": "Inputs with defaults."},
	"ActionDef.outputSchema": {"type": "object", "description": "JSON Schema of one output item."},
}

// Specs returns the four package schemas. root is the module root (for
// reading struct comments).
func Specs(root string) ([]Spec, error) {
	dirs := make([]string, len(docDirs))
	for i, d := range docDirs {
		dirs[i] = filepath.Join(root, d)
	}
	docs, err := ParseDocs(dirs...)
	if err != nil {
		return nil, err
	}
	enums := map[string][]string{
		"Manifest.schema": {automation.SchemaV1},
		"Policy.tier":     {"standard", "social"},
	}
	for k, v := range actionEnums {
		enums[k] = v
	}
	// The transform ops come from the table in action.TransformOp's doc
	// comment, so a new op needs no change here.
	ops, err := ParseDocTable(filepath.Join(root, "internal/action"), "TransformOp")
	if err != nil {
		return nil, err
	}
	if len(ops) == 0 {
		return nil, fmt.Errorf("schemagen: no op table in the action.TransformOp doc comment")
	}
	enums["TransformOp.op"] = ops
	enums["TransformOp.order"] = []string{"asc", "desc"}
	return []Spec{
		{
			File:        "automation.v1.schema.json",
			ID:          SchemaBaseURI + "automation.v1.schema.json",
			Title:       "Mono Agent automation package manifest (automation.json)",
			Description: "A browser automation package: site, login, permissions and the actions it ships.",
			Root:        reflect.TypeOf(automation.Manifest{}),
			Enums:       enums,
			Docs:        docs,
			FieldDocs:   fieldDocs,
		},
		{
			File:        "action.v1.schema.json",
			ID:          SchemaBaseURI + "action.v1.schema.json",
			Title:       "Mono Agent action definition (actions/<name>.json)",
			Description: "One action of an automation package: inputs, outputs and the steps the action engine runs.",
			Root:        reflect.TypeOf(action.ActionDef{}),
			Enums:       enums,
			Overrides:   actionOverrides,
			// "automation" replaces the legacy "platform".
			Optional:  map[string]bool{"ActionDef.platform": true},
			Docs:      docs,
			FieldDocs: fieldDocs,
		},
		{
			File:        "fragment.v1.schema.json",
			ID:          SchemaBaseURI + "fragment.v1.schema.json",
			Title:       "Mono Agent step fragment (fragments/<name>.json)",
			Description: "A reusable step sequence, included by a call_fragment step.",
			Root:        reflect.TypeOf(action.FragmentDef{}),
			Enums:       enums,
			Overrides:   actionOverrides,
			Docs:        docs,
			FieldDocs:   fieldDocs,
		},
		{
			File:        "selectors.v1.schema.json",
			ID:          SchemaBaseURI + "selectors.v1.schema.json",
			Title:       "Mono Agent named selectors (selectors.json)",
			Description: "Named selectors referenced by a step's configKey: ranked candidates tried in order.",
			Root:        reflect.TypeOf(map[string]action.SelectorEntry{}),
			Enums:       enums,
			Docs:        docs,
			FieldDocs:   fieldDocs,
		},
	}, nil
}

// GenerateAll returns file name → schema bytes for every Spec.
func GenerateAll(root string) (map[string][]byte, error) {
	specs, err := Specs(root)
	if err != nil {
		return nil, err
	}
	out := map[string][]byte{}
	for _, s := range specs {
		b, err := Generate(s)
		if err != nil {
			return nil, err
		}
		out[s.File] = b
	}
	return out, nil
}
