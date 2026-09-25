package schemagen

import (
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strings"
)

// miniValidator checks a JSON value against the subset of JSON Schema the
// generator emits ($ref, oneOf, type, enum, properties, required,
// additionalProperties, items). It lets the tests prove the shipped files
// satisfy the shipped schemas without a schema library dependency.
type miniValidator struct {
	root map[string]any
	defs map[string]any
}

func newMiniValidator(schema []byte) (*miniValidator, error) {
	var root map[string]any
	if err := json.Unmarshal(schema, &root); err != nil {
		return nil, err
	}
	defs, _ := root["$defs"].(map[string]any)
	return &miniValidator{root: root, defs: defs}, nil
}

// Validate returns one message per violation (empty when valid).
func (v *miniValidator) Validate(doc []byte) []string {
	var val any
	if err := json.Unmarshal(doc, &val); err != nil {
		return []string{err.Error()}
	}
	var errs []string
	v.check(v.root, val, "$", &errs)
	return errs
}

func (v *miniValidator) check(s map[string]any, val any, path string, errs *[]string) {
	if ref, ok := s["$ref"].(string); ok {
		target := v.root
		if ref != "#" {
			name := strings.TrimPrefix(ref, "#/$defs/")
			target, _ = v.defs[name].(map[string]any)
			if target == nil {
				*errs = append(*errs, path+": unresolved "+ref)
				return
			}
		}
		v.check(target, val, path, errs)
	}
	if alts, ok := s["oneOf"].([]any); ok {
		n := 0
		for _, a := range alts {
			var sub []string
			v.check(a.(map[string]any), val, path, &sub)
			if len(sub) == 0 {
				n++
			}
		}
		if n != 1 {
			*errs = append(*errs, fmt.Sprintf("%s: matches %d of oneOf", path, n))
		}
	}
	if typ, ok := s["type"].(string); ok && !typeMatches(typ, val) {
		*errs = append(*errs, fmt.Sprintf("%s: want %s, got %T", path, typ, val))
		return
	}
	if enum, ok := s["enum"].([]any); ok {
		found := false
		for _, e := range enum {
			if e == val {
				found = true
			}
		}
		if !found {
			*errs = append(*errs, fmt.Sprintf("%s: %v not in enum %v", path, val, enum))
		}
	}
	switch x := val.(type) {
	case map[string]any:
		props, _ := s["properties"].(map[string]any)
		if req, ok := s["required"].([]any); ok {
			for _, r := range req {
				if _, ok := x[r.(string)]; !ok {
					*errs = append(*errs, fmt.Sprintf("%s: missing required %q", path, r))
				}
			}
		}
		keys := make([]string, 0, len(x))
		for k := range x {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			if ps, ok := props[k].(map[string]any); ok {
				v.check(ps, x[k], path+"."+k, errs)
				continue
			}
			switch ap := s["additionalProperties"].(type) {
			case map[string]any:
				v.check(ap, x[k], path+"."+k, errs)
			case bool:
				if !ap {
					*errs = append(*errs, fmt.Sprintf("%s: unknown property %q", path, k))
				}
			}
		}
	case []any:
		if items, ok := s["items"].(map[string]any); ok {
			for i, it := range x {
				v.check(items, it, fmt.Sprintf("%s[%d]", path, i), errs)
			}
		}
	}
}

func typeMatches(typ string, val any) bool {
	switch typ {
	case "object":
		_, ok := val.(map[string]any)
		return ok
	case "array":
		_, ok := val.([]any)
		return ok
	case "string":
		_, ok := val.(string)
		return ok
	case "boolean":
		_, ok := val.(bool)
		return ok
	case "number":
		_, ok := val.(float64)
		return ok
	case "integer":
		f, ok := val.(float64)
		return ok && f == math.Trunc(f)
	}
	return true
}
