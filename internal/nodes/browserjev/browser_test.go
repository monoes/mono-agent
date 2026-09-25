package browserjev

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

// fakeCDP answers Runtime.evaluate by expression shape and records input.
type fakeCDP struct {
	calls   []string
	target  interface{} // value returned by the resolve-target script
	guard   string      // JSON [pageKey, guard] returned by the freshness probe
	inserts []string
}

func (f *fakeCDP) CDP(method string, params map[string]interface{}) (map[string]interface{}, error) {
	f.calls = append(f.calls, method)
	switch method {
	case "Runtime.evaluate":
		expr := params["expression"].(string)
		var value interface{}
		switch {
		case strings.Contains(expr, "c.pageKey(),c.guard"):
			_ = json.Unmarshal([]byte(f.guard), &value)
		case strings.Contains(expr, "state?.marker"):
			value = []interface{}{"m"}
		case strings.Contains(expr, "elementFromPoint"):
			value = f.target
		}
		return map[string]interface{}{"result": map[string]interface{}{"value": value}}, nil
	case "Input.insertText":
		f.inserts = append(f.inserts, params["text"].(string))
	case "Input.dispatchMouseEvent":
		f.calls[len(f.calls)-1] += ":" + params["type"].(string)
	}
	return map[string]interface{}{}, nil
}

func guardedPage() *pageState {
	return &pageState{Marker: json.RawMessage(`["m"]`), PageKey: json.RawMessage(`["k"]`), Guards: map[string]json.RawMessage{"7": json.RawMessage(`["g"]`)}}
}

func TestActFillClicksSelectsAllAndInserts(t *testing.T) {
	f := &fakeCDP{guard: `[["k"],["g"]]`, target: map[string]interface{}{"x": 10.0, "y": 20.0}}
	b := &cdpBrowser{page: f}
	err := b.act(context.Background(), guardedPage(), &action{ID: "e1", Kind: "fill", Node: 7}, "Zürich")
	if err != nil {
		t.Fatal(err)
	}
	got := strings.Join(f.calls, ",")
	for _, want := range []string{"Input.dispatchMouseEvent:mousePressed", "Input.dispatchMouseEvent:mouseReleased", "Input.dispatchKeyEvent"} {
		if !strings.Contains(got, want) {
			t.Errorf("calls %s missing %s", got, want)
		}
	}
	if len(f.inserts) != 1 || f.inserts[0] != "Zürich" {
		t.Errorf("inserts = %v", f.inserts)
	}
}

func TestActRefusesChangedTarget(t *testing.T) {
	f := &fakeCDP{guard: `[["k"],["something else"]]`}
	b := &cdpBrowser{page: f}
	err := b.act(context.Background(), guardedPage(), &action{ID: "e1", Kind: "click", Node: 7}, "")
	if !errors.Is(err, errStale) {
		t.Fatalf("err = %v, want errStale", err)
	}
	for _, c := range f.calls {
		if strings.HasPrefix(c, "Input.") {
			t.Fatalf("input dispatched to a changed target: %v", f.calls)
		}
	}
}

func TestActCoveredTargetIsStaleButCoveredSelectIsAnError(t *testing.T) {
	f := &fakeCDP{guard: `[["k"],["g"]]`, target: nil}
	b := &cdpBrowser{page: f}
	if err := b.act(context.Background(), guardedPage(), &action{Kind: "click", Node: 7}, ""); !errors.Is(err, errStale) {
		t.Errorf("click: err = %v, want errStale", err)
	}
	err := b.act(context.Background(), guardedPage(), &action{Kind: "select", Node: 7, Value: "x"}, "")
	if err == nil || errors.Is(err, errStale) {
		t.Errorf("select: err = %v, want a non-retryable error", err)
	}
}
