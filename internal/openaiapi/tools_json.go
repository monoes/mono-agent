package openaiapi

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"strconv"
)

// decodeKeepingNumbers decodes one JSON document into what encoding/json gives an interface
// (maps, lists, strings, booleans, nil) with each number as the json.Number it was written as.
// A float64 holds nothing past 1.8e308 and no more than 17 digits, and a decode into one refuses
// the whole document for a number it cannot hold: 1e400 anywhere in a schema made every property
// of the function unnamed. A json.Number marshals as it was written, so a schema that goes on to
// monomind is not rounded either.
func decodeKeepingNumbers(raw []byte) (any, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, err
	}
	if _, err := dec.Token(); err != io.EOF {
		return nil, errors.New("data after the JSON document")
	}
	return v, nil
}

// decodeFloats is decodeKeepingNumbers with every number as a float64, for the check that
// compares numbers with the bounds of a schema. A number past what a float64 holds is +Inf or
// -Inf, which compares as it should with every bound a schema can say and with another number
// that large as equal: the check is for the log, and does not tell such numbers apart.
func decodeFloats(raw []byte) (any, error) {
	v, err := decodeKeepingNumbers(raw)
	if err != nil {
		return nil, err
	}
	return floatsOf(v), nil
}

func floatsOf(v any) any {
	switch x := v.(type) {
	case json.Number:
		f, _ := strconv.ParseFloat(string(x), 64) // ±Inf, and an error that is not one, for a number too large
		return f
	case map[string]any:
		for k, e := range x {
			x[k] = floatsOf(e)
		}
	case []any:
		for i, e := range x {
			x[i] = floatsOf(e)
		}
	}
	return v
}
