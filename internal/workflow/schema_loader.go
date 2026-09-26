package workflow

import (
	"embed"
	"encoding/json"
	"fmt"
	"strings"
)

//go:embed schemas/*.json
var embeddedSchemas embed.FS

// NodeSchemaField represents one configurable field in a node's schema.
type NodeSchemaField struct {
	Key         string      `json:"key"`
	Label       string      `json:"label"`
	Type        string      `json:"type"`
	Required    bool        `json:"required"`
	Default     interface{} `json:"default,omitempty"`
	Placeholder string      `json:"placeholder,omitempty"`
	// Examples are ready-made values the editor offers to insert. A field
	// whose shape is not obvious from its label — a list of image prompts,
	// say — is otherwise an empty box the user has to guess at.
	Examples  []string              `json:"examples,omitempty"`
	Help      string                `json:"help,omitempty"`
	Options   []string              `json:"options,omitempty"`
	Language  string                `json:"language,omitempty"`
	Rows      int                   `json:"rows,omitempty"`
	Min       *float64              `json:"min,omitempty"`
	Max       *float64              `json:"max,omitempty"`
	ItemType  string                `json:"item_type,omitempty"`
	Resource  *ResourcePickerConfig `json:"resource,omitempty"`
	DependsOn *FieldDependency      `json:"depends_on,omitempty"`
}

// ResourcePickerConfig configures a resource_picker field.
type ResourcePickerConfig struct {
	Type        string `json:"type"`
	CreateLabel string `json:"create_label,omitempty"`
	ParamField  string `json:"param_field,omitempty"`
}

// FieldDependency hides a field unless another field has one of the given values.
// Uses "key" (not "field") to reference the sibling field's key.
type FieldDependency struct {
	Key    string   `json:"key"`
	Values []string `json:"values"`
}

// NodeSchema is the schema embedded in each workflow node.
type NodeSchema struct {
	CredentialPlatform *string           `json:"credential_platform"`
	Fields             []NodeSchemaField `json:"fields"`
}

// legacyFormPlatforms are the platforms whose nodes without a schema file
// fall back to the shared action.<name>.json form, then browser.generic.json.
// This is the pre-package resolution, kept so built-in (and legacy
// local-<platform>) nodes keep exactly the forms they had.
var legacyFormPlatforms = map[string]bool{
	"instagram": true,
	"linkedin":  true,
	"x":         true,
	"tiktok":    true,
}

// LoadDefaultSchema returns the form schema for a node type:
//
//  1. its schema file (schemas/<type>.json);
//  2. for instagram/linkedin/x/tiktok nodes (built-in or legacy local-*),
//     the shared action-suffix file (e.g. linkedin.find_by_keyword →
//     schemas/action.find_by_keyword.json); for a legacy local-* action,
//     then browser.generic.json;
//  3. for any other automation action, the package's forms/<action>.json
//     (non-built-in packages), else a form generated from the action's
//     declared inputs and their ui hints.
//
// Returns an empty schema (no fields) when none of these applies.
func LoadDefaultSchema(nodeType string) (*NodeSchema, error) {
	data, ok := schemaFile(nodeType)
	if !ok {
		if gen, ok := generateActionSchema(nodeType); ok {
			return gen, nil
		}
		return &NodeSchema{Fields: []NodeSchemaField{}}, nil
	}
	var schema NodeSchema
	if err := json.Unmarshal(data, &schema); err != nil {
		return nil, fmt.Errorf("schema_loader: parse schemas/%s.json: %w", nodeType, err)
	}
	if schema.Fields == nil {
		schema.Fields = []NodeSchemaField{}
	}
	return &schema, nil
}

// schemaFile returns the explicit schema file for nodeType (steps 1 and 2
// of LoadDefaultSchema).
func schemaFile(nodeType string) ([]byte, bool) {
	if data, err := embeddedSchemas.ReadFile("schemas/" + nodeType + ".json"); err == nil {
		return data, true
	}
	dot := strings.Index(nodeType, ".")
	if dot <= 0 || !legacyFormPlatforms[strings.TrimPrefix(nodeType[:dot], "local-")] {
		return nil, false
	}
	if data, err := embeddedSchemas.ReadFile("schemas/action." + nodeType[dot+1:] + ".json"); err == nil {
		return data, true
	}
	// A built-in action is described by its own inputs (the generated form);
	// the catch-all generic form is only for legacy local-* actions.
	if !strings.HasPrefix(nodeType, "local-") {
		return nil, false
	}
	if data, err := embeddedSchemas.ReadFile("schemas/browser.generic.json"); err == nil {
		return data, true
	}
	return nil, false
}

// ListEmbeddedSchemas returns all node type names that have an embedded schema.
func ListEmbeddedSchemas() []string {
	entries, err := embeddedSchemas.ReadDir("schemas")
	if err != nil {
		return nil
	}
	types := make([]string, 0, len(entries))
	for _, e := range entries {
		name := e.Name()
		if strings.HasSuffix(name, ".json") {
			types = append(types, strings.TrimSuffix(name, ".json"))
		}
	}
	return types
}
