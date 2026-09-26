package action

// Value parsing and ordering for transform: human-printed numbers and
// dates, and the sort op.

import (
	"fmt"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

var numberRe = regexp.MustCompile(`[-+]?\d[\d,]*(?:\.\d+)?|[-+]?\.\d+`)

// parseHumanNumber parses numbers as sites print them: "1.2k" → 1200,
// "3,400" → 3400, "2.5M" → 2500000, "12 points" → 12.
func parseHumanNumber(v interface{}) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	case nil:
		return 0, false
	}
	s := strings.TrimSpace(fieldString(v))
	loc := numberRe.FindStringIndex(s)
	if loc == nil {
		return 0, false
	}
	f, err := strconv.ParseFloat(strings.ReplaceAll(s[loc[0]:loc[1]], ",", ""), 64)
	if err != nil {
		return 0, false
	}
	rest := strings.TrimSpace(s[loc[1]:])
	// A lone k/M/B suffix scales ("1.2k"); a word does not ("5 minutes").
	if rest != "" && (len(rest) == 1 || !isASCIILetter(rest[1])) {
		switch rest[0] {
		case 'k', 'K':
			f *= 1e3
		case 'm', 'M':
			f *= 1e6
		case 'b', 'B':
			f *= 1e9
		}
	}
	return math.Round(f*1e6) / 1e6, true
}

func isASCIILetter(b byte) bool { return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') }

var autoLayouts = []string{
	time.RFC3339Nano, time.RFC3339, time.RFC1123Z, time.RFC1123, time.RFC850,
	"2006-01-02T15:04:05", "2006-01-02 15:04:05", "2006-01-02 15:04", "2006-01-02",
	"Jan 2, 2006", "January 2, 2006", "Jan 2 2006", "2 Jan 2006", "02 Jan 2006",
	"2 January 2006", "01/02/2006", "Mon, 2 Jan 2006",
}

var agoRe = regexp.MustCompile(`(?i)^(\d+|an?|one)\s+(second|sec|minute|min|hour|hr|day|week|month|year)s?\s+ago$`)

// parseDate parses s with layout, or with "auto"/"": the common layouts,
// unix seconds, "today"/"yesterday" and "<n> <unit>s ago" relative to now.
func parseDate(s, layout string, now time.Time) (time.Time, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}, false
	}
	if layout != "" && layout != "auto" {
		t, err := time.Parse(layout, s)
		return t, err == nil
	}
	for _, l := range autoLayouts {
		if t, err := time.Parse(l, s); err == nil {
			return t, true
		}
	}
	if n, err := strconv.ParseInt(s, 10, 64); err == nil && n > 1e8 {
		if n > 1e11 { // milliseconds
			return time.UnixMilli(n), true
		}
		return time.Unix(n, 0), true
	}
	switch strings.ToLower(s) {
	case "now", "just now", "today":
		return now, true
	case "yesterday":
		return now.AddDate(0, 0, -1), true
	}
	if m := agoRe.FindStringSubmatch(s); m != nil {
		n, err := strconv.Atoi(m[1])
		if err != nil {
			n = 1 // "a", "an", "one"
		}
		switch strings.ToLower(m[2]) {
		case "second", "sec":
			return now.Add(-time.Duration(n) * time.Second), true
		case "minute", "min":
			return now.Add(-time.Duration(n) * time.Minute), true
		case "hour", "hr":
			return now.Add(-time.Duration(n) * time.Hour), true
		case "day":
			return now.AddDate(0, 0, -n), true
		case "week":
			return now.AddDate(0, 0, -7*n), true
		case "month":
			return now.AddDate(0, -n, 0), true
		case "year":
			return now.AddDate(-n, 0, 0), true
		}
	}
	return time.Time{}, false
}

// sortItems stably sorts items by Field ("" = the item itself), ascending
// or descending by Order. Two values that both parse as numbers compare
// numerically, otherwise as strings; missing (nil) values sort last in
// either order.
func sortItems(items []interface{}, op TransformOp) ([]interface{}, error) {
	desc := false
	switch strings.ToLower(strings.TrimSpace(op.Order)) {
	case "", "asc":
	case "desc":
		desc = true
	default:
		return nil, fmt.Errorf("sort order %q: want asc or desc", op.Order)
	}
	sort.SliceStable(items, func(i, j int) bool {
		a, b := getField(items[i], op.Field), getField(items[j], op.Field)
		if a == nil || b == nil {
			return a != nil && b == nil // present before missing
		}
		c := compareValues(a, b)
		if desc {
			return c > 0
		}
		return c < 0
	})
	return items, nil
}

// compareValues orders two present values: numerically when both are
// numbers, else by their string form.
func compareValues(a, b interface{}) int {
	fa, okA := strictNumber(a)
	fb, okB := strictNumber(b)
	if okA && okB {
		switch {
		case fa < fb:
			return -1
		case fa > fb:
			return 1
		}
		return 0
	}
	return strings.Compare(fieldString(a), fieldString(b))
}

// strictNumber parses v only when it is a number or a plain numeric string.
func strictNumber(v interface{}) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	case string:
		f, err := strconv.ParseFloat(strings.TrimSpace(n), 64)
		return f, err == nil
	}
	return 0, false
}

// arith applies one arithmetic transform op to n, rounding to an integer
// (half away from zero) when round is set.
func arith(op string, n, by float64, round bool) float64 {
	switch op {
	case "add":
		n += by
	case "subtract":
		n -= by
	case "multiply":
		n *= by
	case "divide":
		n /= by
	}
	if round {
		return math.Round(n)
	}
	return math.Round(n*1e9) / 1e9
}
