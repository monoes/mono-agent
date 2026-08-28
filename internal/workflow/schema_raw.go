package workflow

import (
	"encoding/json"
	"strings"
)

// ReadEmbeddedSchema returns the raw embedded schema JSON for nodeType,
// applying the same resolution rules as LoadDefaultSchema (the action-suffix
// and browser.generic fallbacks for browser platform nodes). The second
// return value reports whether a schema file exists for the type — unlike
// LoadDefaultSchema, which returns an empty schema for unknown types.
func ReadEmbeddedSchema(nodeType string) ([]byte, bool) {
	fileName := "schemas/" + nodeType + ".json"
	data, err := embeddedSchemas.ReadFile(fileName)
	if err != nil {
		if dot := strings.Index(nodeType, "."); dot > 0 {
			if browserPlatforms[nodeType[:dot]] {
				suffix := nodeType[dot+1:]
				data, err = embeddedSchemas.ReadFile("schemas/action." + suffix + ".json")
				if err != nil {
					data, err = embeddedSchemas.ReadFile("schemas/browser.generic.json")
				}
			}
		}
	}
	if err != nil {
		return nil, false
	}
	return data, true
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
