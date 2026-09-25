package action

// The transform step: declarative reshaping of a list (or one object) with
// the ops documented on TransformOp. Pure Go — no page, no script VM.

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

func (ae *ActionExecutor) stepTransform(_ context.Context, step StepDef) (*StepResult, error) {
	src := ae.extResolve(step.Input)
	if src == nil {
		return extFail(step, "input %q resolved to nothing", step.Input)
	}
	ae.execCtx.mu.Lock()
	vars := make(map[string]interface{}, len(ae.execCtx.Variables))
	for k, v := range ae.execCtx.Variables {
		vars[k] = v
	}
	ae.execCtx.mu.Unlock()

	out, err := runTransform(src, step.Ops, vars, time.Now())
	if err != nil {
		return extFail(step, "%w", err)
	}
	// tree_parent carries its ancestor stack to the next page's transform.
	for _, op := range step.Ops {
		if op.Carry != "" {
			ae.execCtx.SetVariable(op.Carry, vars[op.Carry])
		}
	}
	return ae.extStore(step, out), nil
}

// runTransform applies ops to src. A list stays a list; a single object is
// transformed as a one-item list and returned as an object (nil when a
// filter dropped it). vars are the variables map templates may use besides
// the item's own fields; tree_parent writes its carried stack back into
// vars.
func runTransform(src interface{}, ops []TransformOp, vars map[string]interface{}, now time.Time) (interface{}, error) {
	items, isList := toList(src)
	if !isList {
		if m, ok := src.(map[string]interface{}); ok {
			items = []interface{}{m}
		} else if s, ok := src.(string); ok && strings.HasPrefix(strings.TrimSpace(s), "{") {
			var m map[string]interface{}
			if err := json.Unmarshal([]byte(s), &m); err != nil {
				return nil, fmt.Errorf("input is not valid JSON: %w", err)
			}
			items = []interface{}{m}
		} else {
			return nil, fmt.Errorf("input is %T, expected a list or an object", src)
		}
	}
	if vars == nil {
		vars = map[string]interface{}{}
	}
	// Work on copies so the source variable is never mutated.
	cp := make([]interface{}, len(items))
	for i, it := range items {
		cp[i] = copyItem(it)
	}
	items = cp
	for i, op := range ops {
		var err error
		items, err = applyOp(items, op, vars, now)
		if err != nil {
			return nil, fmt.Errorf("op %d (%s): %w", i+1, op.Op, err)
		}
	}
	if isList {
		return items, nil
	}
	if len(items) == 0 {
		return nil, nil
	}
	return items[0], nil
}

func copyItem(v interface{}) interface{} {
	m, ok := v.(map[string]interface{})
	if !ok {
		return v
	}
	out := make(map[string]interface{}, len(m))
	for k, x := range m {
		out[k] = x
	}
	return out
}

// itemResolver resolves templates against one item: its fields as
// top-level variables, the item itself as "item", and vars underneath.
func itemResolver(item interface{}, vars map[string]interface{}) *VariableResolver {
	ec := NewExecutionContext()
	for k, v := range vars {
		ec.Variables[k] = v
	}
	if m, ok := item.(map[string]interface{}); ok {
		for k, v := range m {
			ec.Variables[k] = v
		}
	}
	ec.Variables["item"] = item
	return NewVariableResolver(ec)
}

// getField reads field (a dotted path) from item; "" is the item itself.
func getField(item interface{}, field string) interface{} {
	if field == "" {
		return item
	}
	v, err := jsonPath(item, field)
	if err != nil {
		return nil
	}
	return v
}

// setField stores v at to (or field when to is empty) on a map item; with
// both empty, v replaces the item.
func setField(item interface{}, field, to string, v interface{}) interface{} {
	if to == "" {
		to = field
	}
	if to == "" {
		return v
	}
	m, ok := item.(map[string]interface{})
	if !ok {
		m = map[string]interface{}{}
	}
	m[to] = v
	return m
}

func applyOp(items []interface{}, op TransformOp, vars map[string]interface{}, now time.Time) ([]interface{}, error) {
	switch op.Op {
	case "map":
		if len(op.Map) == 0 {
			return nil, fmt.Errorf("map needs a map of field templates")
		}
		for i, it := range items {
			r := itemResolver(it, vars)
			m, ok := it.(map[string]interface{})
			if !ok {
				m = map[string]interface{}{}
			}
			for k, tmpl := range op.Map {
				m[k] = r.ResolveValue(tmpl)
			}
			items[i] = m
		}
		return items, nil

	case "filter":
		if op.Where == nil {
			return nil, fmt.Errorf("filter needs where")
		}
		out := items[:0:0]
		for _, it := range items {
			ok, err := matchWhere(it, op.Where, vars)
			if err != nil {
				return nil, err
			}
			if ok {
				out = append(out, it)
			}
		}
		return out, nil

	case "flag":
		if op.Where == nil || op.To == "" {
			return nil, fmt.Errorf("flag needs where and to")
		}
		for i, it := range items {
			ok, err := matchWhere(it, op.Where, vars)
			if err != nil {
				return nil, err
			}
			items[i] = setField(it, "", op.To, ok)
		}
		return items, nil

	case "sort":
		return sortItems(items, op)

	case "dedupe":
		seen := map[string]bool{}
		out := items[:0:0]
		for _, it := range items {
			k, _ := json.Marshal(getField(it, op.Field))
			if !seen[string(k)] {
				seen[string(k)] = true
				out = append(out, it)
			}
		}
		return out, nil

	case "regex_extract":
		re, err := regexp.Compile(op.Pattern)
		if err != nil {
			return nil, fmt.Errorf("pattern: %w", err)
		}
		for i, it := range items {
			var val interface{}
			if m := re.FindStringSubmatch(fieldString(getField(it, op.Field))); m != nil {
				val = m[0]
				if len(m) > 1 {
					val = m[1]
				}
			}
			items[i] = setField(it, op.Field, op.To, val)
		}
		return items, nil

	case "parse_date":
		for i, it := range items {
			var val interface{}
			if t, ok := parseDate(fieldString(getField(it, op.Field)), op.Layout, now); ok {
				val = t.UTC().Format(time.RFC3339)
			}
			items[i] = setField(it, op.Field, op.To, val)
		}
		return items, nil

	case "parse_number":
		for i, it := range items {
			var val interface{}
			if f, ok := parseHumanNumber(getField(it, op.Field)); ok {
				val = f
			}
			items[i] = setField(it, op.Field, op.To, val)
		}
		return items, nil

	case "join":
		sep := op.Sep
		if sep == "" {
			sep = ", "
		}
		for i, it := range items {
			list, _ := toList(getField(it, op.Field))
			parts := make([]string, 0, len(list))
			for _, x := range list {
				parts = append(parts, fieldString(x))
			}
			items[i] = setField(it, op.Field, op.To, strings.Join(parts, sep))
		}
		return items, nil

	case "split":
		sep := op.Sep
		if sep == "" {
			sep = ","
		}
		for i, it := range items {
			parts := []interface{}{}
			for _, p := range strings.Split(fieldString(getField(it, op.Field)), sep) {
				if p = strings.TrimSpace(p); p != "" {
					parts = append(parts, p)
				}
			}
			items[i] = setField(it, op.Field, op.To, parts)
		}
		return items, nil

	case "lower":
		for i, it := range items {
			v := getField(it, op.Field)
			if v != nil {
				v = strings.ToLower(fieldString(v))
			}
			items[i] = setField(it, op.Field, op.To, v)
		}
		return items, nil

	case "replace":
		re, err := regexp.Compile(op.Pattern)
		if err != nil {
			return nil, fmt.Errorf("pattern: %w", err)
		}
		for i, it := range items {
			v := getField(it, op.Field)
			if v != nil {
				v = re.ReplaceAllString(fieldString(v), op.With)
			}
			items[i] = setField(it, op.Field, op.To, v)
		}
		return items, nil

	case "tree_parent":
		return treeParent(items, op, vars)

	case "pick":
		if len(op.Fields) == 0 {
			return nil, fmt.Errorf("pick needs fields")
		}
		for i, it := range items {
			m, ok := it.(map[string]interface{})
			if !ok {
				continue
			}
			out := make(map[string]interface{}, len(op.Fields))
			for _, f := range op.Fields {
				if v, ok := m[f]; ok {
					out[f] = v
				}
			}
			items[i] = out
		}
		return items, nil

	case "limit":
		if op.Count < 0 {
			return nil, fmt.Errorf("limit count must be ≥ 0")
		}
		if len(items) > op.Count {
			items = items[:op.Count]
		}
		return items, nil
	}
	return nil, fmt.Errorf("unknown op %q", op.Op)
}

func fieldString(v interface{}) string {
	switch s := v.(type) {
	case nil:
		return ""
	case string:
		return s
	case float64:
		return strconv.FormatFloat(s, 'f', -1, 64)
	}
	return fmt.Sprintf("%v", v)
}

// matchWhere evaluates a condition against one item. Variable names a
// field of the item (dotted path, or "item.x"); Value may be a template.
// Operators: exists, not_exists, equals, not_equals, greater_than,
// less_than, contains, not_contains, matches (Go regexp).
func matchWhere(item interface{}, c *ConditionDef, vars map[string]interface{}) (bool, error) {
	r := itemResolver(item, vars)
	val := r.ResolvePath(c.Variable)
	want := c.Value
	if s, ok := want.(string); ok {
		want = r.ResolveValue(s)
	}
	exists := val != nil
	switch c.Operator {
	case "exists":
		return exists, nil
	case "not_exists":
		return !exists, nil
	case "equals", "":
		return exists && fieldString(val) == fieldString(want), nil
	case "not_equals":
		return !exists || fieldString(val) != fieldString(want), nil
	case "greater_than", "less_than":
		a, ok1 := parseHumanNumber(val)
		b, ok2 := parseHumanNumber(want)
		if !ok1 || !ok2 {
			return false, nil
		}
		if c.Operator == "greater_than" {
			return a > b, nil
		}
		return a < b, nil
	case "contains":
		return exists && strings.Contains(fieldString(val), fieldString(want)), nil
	case "not_contains":
		return !exists || !strings.Contains(fieldString(val), fieldString(want)), nil
	case "matches":
		re, err := regexp.Compile(fieldString(want))
		if err != nil {
			return false, fmt.Errorf("where pattern: %w", err)
		}
		return exists && re.MatchString(fieldString(val)), nil
	}
	return false, fmt.Errorf("unknown operator %q", c.Operator)
}

// treeParent gives each row of a flat, depth-annotated tree (a comment
// page) the id of its closest previous row one level up: rows at depth 0
// get Root. The open-ancestor stack is read from and written back to
// vars[Carry], so a tree split over pages continues where it left off.
func treeParent(items []interface{}, op TransformOp, vars map[string]interface{}) ([]interface{}, error) {
	if op.Field == "" || op.ID == "" {
		return nil, fmt.Errorf("tree_parent needs field (depth) and id")
	}
	to := op.To
	if to == "" {
		to = "parentId"
	}
	var root interface{}
	if op.Root != "" {
		root = itemResolver(nil, vars).ResolveValue(op.Root)
		if s, ok := root.(string); ok && s == "" {
			root = nil
		}
	}
	var stack []interface{} // stack[d] = id of the open row at depth d
	if op.Carry != "" {
		if prev, ok := toList(vars[op.Carry]); ok {
			stack = append(stack, prev...)
		}
	}
	for i, it := range items {
		m, ok := it.(map[string]interface{})
		if !ok {
			return nil, fmt.Errorf("row %d is not an object", i)
		}
		df, ok := parseHumanNumber(getField(m, op.Field))
		if !ok || df < 0 {
			return nil, fmt.Errorf("row %d: depth %v is not a non-negative number", i, getField(m, op.Field))
		}
		d := int(df)
		parent := root
		for j := minInt(d, len(stack)) - 1; j >= 0; j-- {
			if stack[j] != nil {
				parent = stack[j]
				break
			}
		}
		m[to] = parent
		if d < len(stack) {
			stack = stack[:d]
		}
		for len(stack) < d {
			stack = append(stack, nil)
		}
		stack = append(stack, getField(m, op.ID))
	}
	if op.Carry != "" {
		vars[op.Carry] = stack
	}
	return items, nil
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
