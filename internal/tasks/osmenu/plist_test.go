package osmenu

import (
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"strconv"
	"testing"
)

// parsePlist reads an XML property list into Go values: map[string]any for a
// dict, []any for an array, string, int64 and bool. It fails the test on
// anything that is not a well-formed property list.
func parsePlist(t *testing.T, data []byte) any {
	t.Helper()
	d := xml.NewDecoder(bytes.NewReader(data))
	for {
		tok, err := d.Token()
		if err != nil {
			t.Fatalf("no <plist> element: %v", err)
		}
		if start, ok := tok.(xml.StartElement); ok && start.Name.Local == "plist" {
			v, err := plistNext(d)
			if err != nil {
				t.Fatalf("not a property list: %v", err)
			}
			return v
		}
	}
}

var errEndOfContainer = errors.New("end of container")

// plistNext reads the next value, or errEndOfContainer at a closing tag.
func plistNext(d *xml.Decoder) (any, error) {
	for {
		tok, err := d.Token()
		if err != nil {
			return nil, err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			return plistValue(d, t)
		case xml.EndElement:
			return nil, errEndOfContainer
		}
	}
}

type plistKey string

func plistValue(d *xml.Decoder, start xml.StartElement) (any, error) {
	switch start.Name.Local {
	case "dict":
		m := map[string]any{}
		for {
			k, err := plistNext(d)
			if errors.Is(err, errEndOfContainer) {
				return m, nil
			}
			if err != nil {
				return nil, err
			}
			key, ok := k.(plistKey)
			if !ok {
				return nil, fmt.Errorf("a dict entry without a key: %v", k)
			}
			v, err := plistNext(d)
			if err != nil {
				return nil, fmt.Errorf("the value of %q: %w", key, err)
			}
			m[string(key)] = v
		}
	case "array":
		a := []any{}
		for {
			v, err := plistNext(d)
			if errors.Is(err, errEndOfContainer) {
				return a, nil
			}
			if err != nil {
				return nil, err
			}
			a = append(a, v)
		}
	case "key", "string", "integer":
		var s string
		if err := d.DecodeElement(&s, &start); err != nil {
			return nil, err
		}
		switch start.Name.Local {
		case "key":
			return plistKey(s), nil
		case "integer":
			return strconv.ParseInt(s, 10, 64)
		}
		return s, nil
	case "true", "false":
		if err := d.Skip(); err != nil {
			return nil, err
		}
		return start.Name.Local == "true", nil
	}
	return nil, fmt.Errorf("unexpected <%s>", start.Name.Local)
}

// dig walks v by dict keys (strings) and array indexes (ints).
func dig(t *testing.T, v any, path ...any) any {
	t.Helper()
	for _, p := range path {
		switch p := p.(type) {
		case string:
			m, ok := v.(map[string]any)
			if !ok {
				t.Fatalf("%v: not a dict at %q", path, p)
			}
			if v, ok = m[p]; !ok {
				t.Fatalf("%v: no key %q", path, p)
			}
		case int:
			a, ok := v.([]any)
			if !ok || p >= len(a) {
				t.Fatalf("%v: no item %d", path, p)
			}
			v = a[p]
		}
	}
	return v
}
