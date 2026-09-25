package action

import (
	"errors"
	"strings"
	"testing"

	"github.com/go-rod/rod/lib/input"
)

func TestSelectOption(t *testing.T) {
	el := &extElem{}
	page := &extPage{elems: map[string]*extElem{"#country": el}, eval: func(js string) (interface{}, error) {
		if !strings.Contains(js, "document.activeElement") {
			t.Fatalf("select_option must target the focused element: %s", js)
		}
		if strings.Contains(js, `want = "Germany"`) {
			return jsOut(map[string]interface{}{"value": "de", "label": "Germany"}), nil
		}
		return jsOut(map[string]interface{}{"__error": "no option with value or label"}), nil
	}}
	ae := newExtExecutor(t, page)
	ae.SetVariable("c", "Germany")

	res := runExt(t, ae, StepDef{ID: "s", Type: "select_option", Selector: "#country", Value: "{{c}}", VariableName: "picked"})
	wantOK(t, res)
	if el.focused != 1 {
		t.Fatalf("element focused %d times, want 1", el.focused)
	}
	got, _ := getVar(ae, "picked").(map[string]interface{})
	if got["value"] != "de" {
		t.Fatalf("picked = %v", getVar(ae, "picked"))
	}

	wantFail(t, runExt(t, ae, StepDef{ID: "s", Type: "select_option", Selector: "#country", Text: "Mars"}), "no option")
	wantFail(t, runExt(t, ae, StepDef{ID: "s", Type: "select_option", Selector: "#missing", Text: "x"}), "no <select> element")
	wantFail(t, runExt(t, ae, StepDef{ID: "s", Type: "select_option", Selector: "#country"}), "no option value")
	el.focusErr = errors.New("detached")
	wantFail(t, runExt(t, ae, StepDef{ID: "s", Type: "select_option", Selector: "#country", Text: "Germany"}), "detached")
}

func TestParseKeyCombo(t *testing.T) {
	cases := []struct {
		in      string
		mods    int
		key     string
		code    string
		wantErr bool
	}{
		{in: "Enter", key: "Enter", code: "Enter"},
		{in: "escape", key: "Escape", code: "Escape"},
		{in: "a", key: "a", code: "KeyA"},
		{in: "Control+a", mods: 2, key: "a", code: "KeyA"},
		{in: "Control+Shift+ArrowUp", mods: 10, key: "ArrowUp", code: "ArrowUp"},
		{in: "Meta+Enter", mods: 4, key: "Enter", code: "Enter"},
		{in: "Control++", mods: 2, key: "+"},
		{in: "7", key: "7", code: "Digit7"},
		{in: "", wantErr: true},
		{in: "Hyper+a", wantErr: true},
		{in: "F13", wantErr: true},
	}
	for _, c := range cases {
		_, mods, k, err := parseKeyCombo(c.in)
		if c.wantErr {
			if err == nil {
				t.Errorf("%q: want error", c.in)
			}
			continue
		}
		if err != nil || mods != c.mods || k.key != c.key || k.code != c.code {
			t.Errorf("%q: got mods=%d key=%q code=%q err=%v", c.in, mods, k.key, k.code, err)
		}
	}
}

func TestPressKeyViaCDP(t *testing.T) {
	page := &cdpPage{}
	ae := newExtExecutor(t, page)
	wantOK(t, runExt(t, ae, StepDef{ID: "k", Type: "press_key", Key: "Control+a"}))
	var seq []string
	for _, c := range page.calls {
		if c["method"] != "Input.dispatchKeyEvent" {
			t.Fatalf("unexpected CDP method %v", c["method"])
		}
		seq = append(seq, c["type"].(string)+":"+c["key"].(string))
		if c["modifiers"] != 2 {
			t.Fatalf("modifiers = %v, want 2", c["modifiers"])
		}
		if _, hasText := c["text"]; hasText {
			t.Fatal("a Control chord must insert no text")
		}
	}
	want := "rawKeyDown:Control rawKeyDown:a keyUp:a keyUp:Control"
	if strings.Join(seq, " ") != want {
		t.Fatalf("key sequence %q, want %q", strings.Join(seq, " "), want)
	}

	page.calls = nil
	wantOK(t, runExt(t, ae, StepDef{ID: "k", Type: "press_key", Key: "Enter"}))
	if page.calls[0]["type"] != "keyDown" || page.calls[0]["text"] != "\r" {
		t.Fatalf("Enter should be a text keyDown, got %v", page.calls[0])
	}
}

func TestPressKeyFallbackAndErrors(t *testing.T) {
	el := &extElem{}
	page := &extPage{elems: map[string]*extElem{"#q": el}}
	ae := newExtExecutor(t, page)
	ae.SetVariable("k", "Enter")
	wantOK(t, runExt(t, ae, StepDef{ID: "k", Type: "press_key", Key: "{{k}}", Selector: "#q"}))
	if el.focused != 1 || len(page.pressed) != 1 || page.pressed[0] != rune(input.Enter) {
		t.Fatalf("focus=%d pressed=%v", el.focused, page.pressed)
	}
	wantFail(t, runExt(t, ae, StepDef{ID: "k", Type: "press_key", Key: "Control+a"}), "cannot press modifier keys")
	wantFail(t, runExt(t, ae, StepDef{ID: "k", Type: "press_key", Key: "Nope+a"}), "unknown modifier")
	wantFail(t, runExt(t, ae, StepDef{ID: "k", Type: "press_key", Key: "Enter", Selector: "#gone"}), "no element to focus")
}
