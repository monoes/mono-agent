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

// LoadDefaultSchema returns the form schema for a node type:
//
//  1. its schema file (schemas/<type>.json);
//  2. for a built-in browser automation, the shared action-suffix file
//     (e.g. linkedin.find_by_keyword → schemas/action.find_by_keyword.json);
//  3. for any other browser automation action, the package's
//     forms/<action>.json, else a form generated from the action's declared
//     inputs and their ui hints.
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
	if dot := strings.Index(nodeType, "."); dot > 0 && isBuiltinAutomation(nodeType[:dot]) {
		if data, err := embeddedSchemas.ReadFile("schemas/action." + nodeType[dot+1:] + ".json"); err == nil {
			return data, true
		}
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
