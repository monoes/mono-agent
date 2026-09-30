package orgsign

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"unicode/utf16"
)

// The signed hash is sha256(JSON.stringify(canonical projection)) as
// monomind computes it in Node (orgrt/org-signature.ts). Reproducing it
// byte for byte means reproducing JSON.parse + JSON.stringify: numbers are
// IEEE doubles printed the ECMAScript way, strings escape only what
// JSON.stringify escapes, and an object's keys come out in property order —
// integer-like keys first in numeric order, then the rest in the order
// canonical() inserted them (sorted by UTF-16 code units). Getting any of
// this wrong only ever makes a signed org look unsigned (fail closed).

// parseJSON decodes org JSON the way JSON.parse does for our purposes:
// numbers kept as their literal text so they can be re-printed as doubles.
func parseJSON(b []byte) (interface{}, error) {
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.UseNumber()
	var v interface{}
	if err := dec.Decode(&v); err != nil {
		return nil, err
	}
	if dec.More() {
		return nil, fmt.Errorf("trailing data after the JSON value")
	}
	return v, nil
}

// stringify is JSON.stringify(v) for a value parseJSON produced (or one
// built from maps, slices, strings and numbers).
func stringify(v interface{}) string {
	var sb strings.Builder
	writeValue(&sb, v)
	return sb.String()
}

func writeValue(sb *strings.Builder, v interface{}) {
	switch t := v.(type) {
	case nil:
		sb.WriteString("null")
	case bool:
		if t {
			sb.WriteString("true")
		} else {
			sb.WriteString("false")
		}
	case json.Number:
		// The decoder already checked the syntax, so an error here is only
		// ErrRange, where f is ±Inf or 0 exactly as JSON.parse makes it.
		f, _ := strconv.ParseFloat(string(t), 64)
		sb.WriteString(jsNumber(f))
	case float64:
		sb.WriteString(jsNumber(t))
	case int:
		sb.WriteString(jsNumber(float64(t)))
	case string:
		writeString(sb, t)
	case []interface{}:
		sb.WriteByte('[')
		for i, e := range t {
			if i > 0 {
				sb.WriteByte(',')
			}
			writeValue(sb, e)
		}
		sb.WriteByte(']')
	case map[string]interface{}:
		sb.WriteByte('{')
		for i, k := range propertyOrder(t) {
			if i > 0 {
				sb.WriteByte(',')
			}
			writeString(sb, k)
			sb.WriteByte(':')
			writeValue(sb, t[k])
		}
		sb.WriteByte('}')
	default:
		// Not produced by parseJSON; never silently hash something else.
		panic(fmt.Sprintf("orgsign: unsupported JSON value %T", v))
	}
}

// propertyOrder is the order JSON.stringify emits a canonical() object's
// keys in: array-index keys ascending numerically, then every other key
// sorted by UTF-16 code units (canonical() inserts them sorted).
func propertyOrder(m map[string]interface{}) []string {
	var idx, rest []string
	for k := range m {
		if isArrayIndex(k) {
			idx = append(idx, k)
		} else {
			rest = append(rest, k)
		}
	}
	sort.Slice(idx, func(i, j int) bool {
		a, _ := strconv.ParseUint(idx[i], 10, 64)
		b, _ := strconv.ParseUint(idx[j], 10, 64)
		return a < b
	})
	sort.Slice(rest, func(i, j int) bool { return utf16Less(rest[i], rest[j]) })
	return append(idx, rest...)
}

// isArrayIndex: a canonical numeric string below 2^32-1, which ECMAScript
// orders before every other own property.
func isArrayIndex(k string) bool {
	if k == "" || len(k) > 10 || (len(k) > 1 && k[0] == '0') {
		return false
	}
	for i := 0; i < len(k); i++ {
		if k[i] < '0' || k[i] > '9' {
			return false
		}
	}
	n, err := strconv.ParseUint(k, 10, 64)
	return err == nil && n < math.MaxUint32
}

// utf16Less is Array.prototype.sort's default string comparison.
func utf16Less(a, b string) bool {
	ua, ub := utf16.Encode([]rune(a)), utf16.Encode([]rune(b))
	for i := 0; i < len(ua) && i < len(ub); i++ {
		if ua[i] != ub[i] {
			return ua[i] < ub[i]
		}
	}
	return len(ua) < len(ub)
}

// writeString is JSON.stringify's QuoteJSONString for a valid UTF-8 string
// (encoding/json already turned invalid bytes into U+FFFD, as Node's utf8
// decoding does, so no lone surrogate can reach here).
func writeString(sb *strings.Builder, s string) {
	const hex = "0123456789abcdef"
	sb.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"':
			sb.WriteString(`\"`)
		case '\\':
			sb.WriteString(`\\`)
		case '\b':
			sb.WriteString(`\b`)
		case '\f':
			sb.WriteString(`\f`)
		case '\n':
			sb.WriteString(`\n`)
		case '\r':
			sb.WriteString(`\r`)
		case '\t':
			sb.WriteString(`\t`)
		default:
			if r < 0x20 {
				sb.WriteString(`\u00`)
				sb.WriteByte(hex[r>>4])
				sb.WriteByte(hex[r&0xf])
			} else {
				sb.WriteRune(r)
			}
		}
	}
	sb.WriteByte('"')
}

// jsNumber is Number.prototype.toString() (ECMA-262 Number::toString) for
// a double: the shortest round-trip digits, in plain notation for
// exponents -7 < e < 21, else as d.ddde±x.
func jsNumber(f float64) string {
	if math.IsNaN(f) || math.IsInf(f, 0) {
		return "null"
	}
	if f == 0 {
		return "0"
	}
	s := strconv.FormatFloat(f, 'e', -1, 64) // e.g. "-1.2345e+21"
	neg := s[0] == '-'
	if neg {
		s = s[1:]
	}
	mant, expPart, _ := strings.Cut(s, "e")
	exp, _ := strconv.Atoi(expPart)
	digits := strings.Replace(mant, ".", "", 1)
	k, n := len(digits), exp+1
	var out string
	switch {
	case k <= n && n <= 21:
		out = digits + strings.Repeat("0", n-k)
	case 0 < n && n <= 21:
		out = digits[:n] + "." + digits[n:]
	case -6 < n && n <= 0:
		out = "0." + strings.Repeat("0", -n) + digits
	default:
		e := n - 1
		sign := "+"
		if e < 0 {
			sign, e = "-", -e
		}
		d := digits[:1]
		if k > 1 {
			d += "." + digits[1:]
		}
		out = d + "e" + sign + strconv.Itoa(e)
	}
	if neg {
		return "-" + out
	}
	return out
}
