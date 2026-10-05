package orgdesign

import (
	"bytes"
	"encoding/json"
	"reflect"
	"strings"
)

// Save writes a Doc back over the file it was loaded from. Re-encoding a Doc
// from scratch reorders keys (modeled fields first, Extra sorted), turns
// every "<" into "<" and rewrites numbers, so a one-role edit would
// rewrite most of the file — and, since an operator signature covers the
// definition, make the diff impossible to review. reflowLike instead renders
// the new value in the layout of the bytes it was loaded from: wherever the
// new and the loaded value are the same JSON value, the loaded bytes are
// copied verbatim, and where an object changed, its keys keep their loaded
// order (new keys are appended in the encoder's order). The result is
// byte-for-byte identical outside the edit.

// reflowLike renders next (JSON) as 2-space indented JSON laid out like
// orig (JSON, or nil for "no layout to follow"). It ends without a newline.
func reflowLike(orig, next []byte) ([]byte, error) {
	var buf bytes.Buffer
	if err := emitLike(&buf, bytes.TrimSpace(orig), bytes.TrimSpace(next), 0); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func emitLike(buf *bytes.Buffer, orig, next json.RawMessage, depth int) error {
	if len(orig) > 0 && sameJSON(orig, next) {
		buf.Write(orig)
		return nil
	}
	switch {
	case isObjectJSON(next):
		nk, nv, err := objectEntries(next)
		if err != nil {
			return err
		}
		var ok []string
		var ov map[string]json.RawMessage
		if isObjectJSON(orig) {
			if ok, ov, err = objectEntries(orig); err != nil {
				return err
			}
		}
		var order []string
		for _, k := range ok {
			if _, in := nv[k]; in {
				order = append(order, k)
			}
		}
		for _, k := range nk {
			if _, in := ov[k]; !in {
				order = append(order, k)
			}
		}
		if len(order) == 0 {
			buf.WriteString("{}")
			return nil
		}
		buf.WriteString("{\n")
		for i, k := range order {
			buf.WriteString(strings.Repeat("  ", depth+1))
			kb, err := encodeNoHTML(k)
			if err != nil {
				return err
			}
			buf.Write(kb)
			buf.WriteString(": ")
			if err := emitLike(buf, ov[k], nv[k], depth+1); err != nil {
				return err
			}
			if i < len(order)-1 {
				buf.WriteByte(',')
			}
			buf.WriteByte('\n')
		}
		buf.WriteString(strings.Repeat("  ", depth) + "}")
		return nil
	case isArrayJSON(next):
		var na, oa []json.RawMessage
		if err := json.Unmarshal(next, &na); err != nil {
			return err
		}
		if isArrayJSON(orig) {
			if err := json.Unmarshal(orig, &oa); err != nil {
				return err
			}
		}
		if len(na) == 0 {
			buf.WriteString("[]")
			return nil
		}
		buf.WriteString("[\n")
		for i, e := range na {
			buf.WriteString(strings.Repeat("  ", depth+1))
			var o json.RawMessage
			if i < len(oa) {
				o = oa[i]
			}
			if err := emitLike(buf, o, e, depth+1); err != nil {
				return err
			}
			if i < len(na)-1 {
				buf.WriteByte(',')
			}
			buf.WriteByte('\n')
		}
		buf.WriteString(strings.Repeat("  ", depth) + "]")
		return nil
	case len(next) > 0 && next[0] == '"':
		var s string
		if err := json.Unmarshal(next, &s); err != nil {
			return err
		}
		b, err := encodeNoHTML(s)
		if err != nil {
			return err
		}
		buf.Write(b)
		return nil
	}
	buf.Write(next)
	return nil
}

func sameJSON(a, b []byte) bool {
	var x, y any
	if json.Unmarshal(a, &x) != nil || json.Unmarshal(b, &y) != nil {
		return false
	}
	return reflect.DeepEqual(x, y)
}

func isObjectJSON(b []byte) bool { return len(b) > 0 && b[0] == '{' }
func isArrayJSON(b []byte) bool  { return len(b) > 0 && b[0] == '[' }

// encodeNoHTML marshals v without escaping <, > and &, as JSON.stringify does.
func encodeNoHTML(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return bytes.TrimRight(buf.Bytes(), "\n"), nil
}
