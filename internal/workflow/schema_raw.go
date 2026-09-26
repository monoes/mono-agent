package workflow

import "encoding/json"

// ReadEmbeddedSchema returns the raw schema JSON for nodeType, applying the
// same resolution rules as LoadDefaultSchema (schema file, the shared
// action-suffix file for built-in browser automations, then a form generated
// from the action's inputs). The second return value reports whether a
// schema exists for the type — unlike LoadDefaultSchema, which returns an
// empty schema for unknown types.
func ReadEmbeddedSchema(nodeType string) ([]byte, bool) {
	return resolveSchemaJSON(nodeType)
}

// SchemaTitle returns the "title" (or fallback "name") field from a node
// type's embedded schema JSON. Most bundled schemas carry neither, in which
// case the result is "".
func SchemaTitle(nodeType string) string {
	data, ok := ReadEmbeddedSchema(nodeType)
	if !ok {
		return ""
	}
	var m map[string]interface{}
	if err := json.Unmarshal(data, &m); err != nil {
		return ""
	}
	if s, _ := m["title"].(string); s != "" {
		return s
	}
	s, _ := m["name"].(string)
	return s
}
