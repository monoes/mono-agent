package extension

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

// fakeSender answers commands the way Server.SendCommand does: a response
// with Success=false becomes an "extension error: <msg>" error.
type fakeSender struct {
	resp *Response
	err  error
	cmds []*Command
}

func (f *fakeSender) SendCommand(cmd *Command, _ time.Duration) (*Response, error) {
	f.cmds = append(f.cmds, cmd)
	if f.err != nil {
		return nil, f.err
	}
	if !f.resp.Success {
		return f.resp, fmt.Errorf("extension error: %s", f.resp.Error)
	}
	return f.resp, nil
}

func TestExtensionPageEvalSurfacesExtensionError(t *testing.T) {
	s := &fakeSender{resp: &Response{Success: false, Error: "Eval: Refused to evaluate a string as JavaScript because 'unsafe-eval' is not an allowed source"}}
	ep := NewExtensionPage(s, 7)

	res, err := ep.Eval(`() => document.title`)
	if err == nil || !strings.Contains(err.Error(), "unsafe-eval") {
		t.Fatalf("err = %v, want the extension's CSP message", err)
	}
	// Callers that deliberately ignore the error still get a usable result.
	if res == nil || res.Raw() != nil {
		t.Fatalf("res = %#v, want an empty non-nil result", res)
	}
	if len(s.cmds) != 1 || s.cmds[0].Type != CmdEval || s.cmds[0].TabID != 7 {
		t.Fatalf("cmds = %+v", s.cmds)
	}
}

func TestExtensionPageEvalSurfacesTransportError(t *testing.T) {
	ep := NewExtensionPage(&fakeSender{err: fmt.Errorf("command eval timed out after 30s")}, 1)
	if _, err := ep.Eval(`() => 1`); err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("err = %v", err)
	}
}

func TestExtensionPageEvalReturnsResult(t *testing.T) {
	ep := NewExtensionPage(&fakeSender{resp: &Response{Success: true, Data: map[string]interface{}{"result": "Title"}}}, 1)
	res, err := ep.Eval(`() => document.title`)
	if err != nil || res.Str() != "Title" {
		t.Fatalf("res = %v err = %v", res, err)
	}
}

func TestExtensionPageEvalCDPSurfacesError(t *testing.T) {
	ep := NewExtensionPage(&fakeSender{resp: &Response{Success: false, Error: "eval_cdp: SyntaxError: Unexpected token"}}, 1)
	v, err := ep.EvalCDP(`(`)
	if err == nil || v != nil || !strings.Contains(err.Error(), "SyntaxError") {
		t.Fatalf("v = %v err = %v", v, err)
	}
	ep = NewExtensionPage(&fakeSender{resp: &Response{Success: true, Data: map[string]interface{}{"result": float64(3)}}}, 1)
	if v, err := ep.EvalCDP(`3`); err != nil || v != float64(3) {
		t.Fatalf("v = %v err = %v", v, err)
	}
}

func TestHasReadsExists(t *testing.T) {
	for _, tc := range []struct {
		data map[string]interface{}
		want bool
	}{
		{map[string]interface{}{"exists": true, "elementId": "el_1"}, true},
		{map[string]interface{}{"exists": false}, false},
		{map[string]interface{}{"found": true}, true}, // older extension builds
	} {
		ep := NewExtensionPage(&fakeSender{resp: &Response{Success: true, Data: tc.data}}, 1)
		if got, err := ep.Has("#x"); err != nil || got != tc.want {
			t.Errorf("Has(%v) = %v, %v; want %v", tc.data, got, err, tc.want)
		}
	}
}

// rectSender returns a moving box for the first reads, then a still one.
type rectSender struct{ n int }

func (r *rectSender) SendCommand(cmd *Command, _ time.Duration) (*Response, error) {
	r.n++
	y := 10.0
	if r.n < 3 {
		y = float64(r.n)
	}
	return &Response{Success: true, Data: map[string]interface{}{"x": 1.0, "y": y, "width": 5.0, "height": 5.0}}, nil
}

func TestWaitStablePollsRect(t *testing.T) {
	s := &rectSender{}
	el := &ExtensionElement{server: s, elementID: "el_1"}
	if err := el.WaitStable(2 * time.Second); err != nil {
		t.Fatal(err)
	}
	if s.n < 4 {
		t.Fatalf("reads = %d, want the box to be read until it stops moving", s.n)
	}
}
