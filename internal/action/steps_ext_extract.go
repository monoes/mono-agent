package action

// Extraction steps: extract_table and extract_json.

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// ---------------------------------------------------------------------------
// extract_table
// ---------------------------------------------------------------------------

// extractTableJS reads rows from the element matched by the selector.
// Without fields it needs a <table> and names columns by header cell. With
// fields each row/item maps field → sub-selector: "" is the item's text,
// "@attr" an attribute of the item, "sel" a descendant's text and
// "sel@attr" a descendant's attribute. Items are rowSel matches, a table's
// data rows, or the container's children.
const extractTableJS = `(() => {
  const sel = %s, isX = %t, fields = %s, rowSel = %s;
  const root = isX ? document.evaluate(sel, document, null, XPathResult.FIRST_ORDERED_NODE_TYPE, null).singleNodeValue : document.querySelector(sel);
  if (!root) return {__error: 'element not found: ' + sel};
  const txt = n => ((n.innerText !== undefined ? n.innerText : n.textContent) || '').trim();
  const names = Object.keys(fields);
  if (names.length === 0) {
    if (root.tagName !== 'TABLE') return {__error: 'fields are required unless the element is a <table>'};
    const rows = Array.from(root.rows);
    const hdr = (root.tHead && root.tHead.rows[0]) || rows.find(r => r.querySelector('th'));
    const heads = hdr ? Array.from(hdr.cells).map((c, i) => txt(c) || 'col' + (i + 1)) : [];
    return {rows: rows.filter(r => r !== hdr && r.querySelector('td')).map(r => {
      const o = {}; Array.from(r.cells).forEach((c, i) => { o[heads[i] || 'col' + (i + 1)] = txt(c); }); return o;
    })};
  }
  let items;
  if (rowSel) items = Array.from(root.querySelectorAll(rowSel));
  else if (root.tagName === 'TABLE') items = Array.from(root.rows).filter(r => r.querySelector('td'));
  else items = Array.from(root.children);
  const get = (item, spec) => {
    let sub = spec.trim(), attr = '';
    const at = sub.lastIndexOf('@');
    if (at >= 0 && /^[\w:-]+$/.test(sub.slice(at + 1))) { attr = sub.slice(at + 1); sub = sub.slice(0, at).trim(); }
    const el = sub === '' ? item : item.querySelector(sub);
    if (!el) return null;
    return attr ? el.getAttribute(attr) : txt(el);
  };
  return {rows: items.map(it => { const o = {}; for (const n of names) o[n] = get(it, fields[n]); return o; })};
})()`

func (ae *ActionExecutor) stepExtractTable(ctx context.Context, step StepDef) (*StepResult, error) {
	sel, err := ae.extWaitSelector(ctx, step)
	if err != nil {
		return extFail(step, "%w", err)
	}
	fields := make(map[string]string, len(step.Fields))
	for k, v := range step.Fields {
		fields[k] = ae.resolver.Resolve(v)
	}
	fieldsJS, _ := jsJSON(fields)
	rowSel, record, err := tableOptions(step.Value)
	if err != nil {
		return extFail(step, "%w", err)
	}
	v, err := ae.evalJS(ctx, fmt.Sprintf(extractTableJS, jsString(sel), isXPath(sel), fieldsJS, jsString(rowSel)), stepTimeout(step, 15))
	if err != nil {
		return extFail(step, "%w", err)
	}
	if err := pageError(v); err != nil {
		return extFail(step, "%w", err)
	}
	m, _ := v.(map[string]interface{})
	rawRows, _ := m["rows"].([]interface{})
	if step.BatchSize > 0 && len(rawRows) > step.BatchSize {
		rawRows = rawRows[:step.BatchSize]
	}
	rows := make([]map[string]interface{}, 0, len(rawRows))
	for _, r := range rawRows {
		row, ok := r.(map[string]interface{})
		if !ok {
			continue
		}
		for name, spec := range fields {
			s, ok := row[name].(string)
			if ok && (strings.HasSuffix(spec, "@href") || strings.HasSuffix(spec, "@src")) {
				row[name] = ae.resolveRelativeURL(s)
			}
		}
		rows = append(rows, row)
		if record {
			ae.execCtx.AddRecord(row)
		}
	}
	return ae.extStore(step, rows), nil
}

// tableOptions reads extract_table's value: a row selector string, or an
// object {"rows": "<row selector>", "record": false}. record (default true)
// adds each row to the node output; false keeps the rows only in the
// step's variable (an intermediate read that a later step reshapes).
func tableOptions(v interface{}) (rowSel string, record bool, err error) {
	switch o := v.(type) {
	case nil:
		return "", true, nil
	case string:
		return o, true, nil
	case map[string]interface{}:
		record = true
		for k, x := range o {
			switch k {
			case "rows":
				s, ok := x.(string)
				if !ok {
					return "", false, fmt.Errorf("value.rows must be a string selector")
				}
				rowSel = s
			case "record":
				b, ok := x.(bool)
				if !ok {
					return "", false, fmt.Errorf("value.record must be true or false")
				}
				record = b
			default:
				return "", false, fmt.Errorf("unknown value option %q (want rows, record)", k)
			}
		}
		return rowSel, record, nil
	}
	return "", false, fmt.Errorf("value must be a row selector or {rows, record}, got %T", v)
}

// extWaitSelector waits for the step's element and returns the selector
// string (CSS or XPath) that found it, for use in page JavaScript. The
// selector comes from core's SelectorString (xpath → selector → configKey
// via the package's selectors.json, then the legacy config manager); the
// step's alternatives are fallbacks for an explicit selector/xpath.
func (ae *ActionExecutor) extWaitSelector(ctx context.Context, step StepDef) (string, error) {
	css, xp, err := ae.SelectorString(ctx, step)
	if err != nil {
		return "", err
	}
	sel := css
	if sel == "" {
		sel = xp
	}
	sels := []string{sel}
	for _, alt := range step.Alternatives {
		if alt != "" && alt != sel {
			sels = append(sels, alt)
		}
	}
	idx, _, err := findFirst(ae.page, sels, stepTimeout(step, 10))
	if err != nil {
		return "", fmt.Errorf("element not found (%s): %w", strings.Join(sels, " | "), err)
	}
	return sels[idx], nil
}

// ---------------------------------------------------------------------------
// extract_json
// ---------------------------------------------------------------------------

const scriptTextJS = `(() => {
  const sel = %s, isX = %t;
  const el = isX ? document.evaluate(sel, document, null, XPathResult.FIRST_ORDERED_NODE_TYPE, null).singleNodeValue : document.querySelector(sel);
  return el ? {text: el.textContent} : {__error: 'element not found: ' + sel};
})()`

func (ae *ActionExecutor) stepExtractJSON(ctx context.Context, step StepDef) (*StepResult, error) {
	var src interface{}
	switch {
	case step.Input != "":
		src = ae.extResolve(step.Input)
		if src == nil {
			return extFail(step, "input %q resolved to nothing", step.Input)
		}
	case step.Selector != "" || step.XPath != "" || step.ConfigKey != "":
		sel, err := ae.extWaitSelector(ctx, step)
		if err != nil {
			return extFail(step, "%w", err)
		}
		v, err := ae.evalJS(ctx, fmt.Sprintf(scriptTextJS, jsString(sel), isXPath(sel)), stepTimeout(step, 15))
		if err != nil {
			return extFail(step, "%w", err)
		}
		if err := pageError(v); err != nil {
			return extFail(step, "%w", err)
		}
		m, _ := v.(map[string]interface{})
		src, _ = m["text"].(string)
	default:
		return extFail(step, "needs a selector or an input")
	}
	if s, ok := src.(string); ok {
		var parsed interface{}
		if err := json.Unmarshal([]byte(strings.TrimSpace(s)), &parsed); err != nil {
			return extFail(step, "not JSON: %w", err)
		}
		src = parsed
	}
	out, err := jsonPath(src, ae.resolver.Resolve(step.Path))
	if err != nil {
		return extFail(step, "path %q: %w", step.Path, err)
	}
	return ae.extStore(step, out), nil
}

// jsonPath walks v by a path such as "a.b[0].c", "[2].name" or "list[0][1]".
// An empty path returns v.
func jsonPath(v interface{}, path string) (interface{}, error) {
	path = strings.TrimPrefix(strings.TrimSpace(path), "$")
	path = strings.TrimPrefix(path, ".")
	if path == "" {
		return v, nil
	}
	cur := v
	for _, seg := range strings.Split(path, ".") {
		name := seg
		var idxs []int
		if b := strings.IndexByte(seg, '['); b >= 0 {
			name = seg[:b]
			rest := seg[b:]
			for rest != "" {
				if rest[0] != '[' {
					return nil, fmt.Errorf("bad segment %q", seg)
				}
				end := strings.IndexByte(rest, ']')
				if end < 0 {
					return nil, fmt.Errorf("unclosed [ in %q", seg)
				}
				n, err := strconv.Atoi(strings.TrimSpace(rest[1:end]))
				if err != nil {
					return nil, fmt.Errorf("bad index in %q", seg)
				}
				idxs = append(idxs, n)
				rest = rest[end+1:]
			}
		}
		if name != "" {
			m, ok := cur.(map[string]interface{})
			if !ok {
				return nil, fmt.Errorf("%q: not an object (%T)", name, cur)
			}
			next, ok := m[name]
			if !ok {
				return nil, fmt.Errorf("no key %q", name)
			}
			cur = next
		}
		for _, i := range idxs {
			arr, ok := cur.([]interface{})
			if !ok {
				return nil, fmt.Errorf("[%d]: not an array (%T)", i, cur)
			}
			if i < 0 {
				i += len(arr)
			}
			if i < 0 || i >= len(arr) {
				return nil, fmt.Errorf("index %d out of range (len %d)", i, len(arr))
			}
			cur = arr[i]
		}
	}
	return cur, nil
}
