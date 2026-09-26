package action

// Input steps: select_option and press_key.

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/go-rod/rod/lib/input"
)

// ---------------------------------------------------------------------------
// select_option
// ---------------------------------------------------------------------------

// selectOptionJS picks an option of the focused <select> by value, then by
// visible label, through the native value setter (so React-controlled
// selects notice) and fires input + change.
const selectOptionJS = `(() => {
  const el = document.activeElement, want = %s;
  if (!el || el.tagName !== 'SELECT') return {__error: 'element is not a <select>'};
  const opts = Array.from(el.options);
  let o = opts.find(o => o.value === want);
  if (!o) o = opts.find(o => o.text.trim() === want.trim() || o.label.trim() === want.trim());
  if (!o) return {__error: 'no option with value or label ' + JSON.stringify(want)};
  const set = Object.getOwnPropertyDescriptor(HTMLSelectElement.prototype, 'value').set;
  set.call(el, o.value);
  el.dispatchEvent(new Event('input', {bubbles: true}));
  el.dispatchEvent(new Event('change', {bubbles: true}));
  return {value: o.value, label: o.text.trim()};
})()`

func (ae *ActionExecutor) stepSelectOption(ctx context.Context, step StepDef) (*StepResult, error) {
	defer ae.releaseJevMarker()
	want := step.Text
	if want == "" && step.Value != nil {
		want = fmt.Sprintf("%v", step.Value)
	}
	if want == "" {
		return extFail(step, "no option value or label (value/text)")
	}
	elem, resolveErr := ae.resolveElement(step)
	if elem == nil {
		return extFail(step, "%w", noElementError("no <select> element for step %s", step.ID, resolveErr))
	}
	// Focus makes the element reachable from page JS whatever driver found
	// it (element handles carry no JS reference of their own).
	if err := elem.Focus(); err != nil {
		return extFail(step, "focus: %w", err)
	}
	v, err := ae.evalJS(ctx, fmt.Sprintf(selectOptionJS, jsString(want)), stepTimeout(step, 10))
	if err != nil {
		return extFail(step, "%w", err)
	}
	if err := pageError(v); err != nil {
		return extFail(step, "%w", err)
	}
	return ae.extStore(step, v), nil
}

// ---------------------------------------------------------------------------
// press_key
// ---------------------------------------------------------------------------

// keySpec describes one key for CDP Input.dispatchKeyEvent and Rod.
type keySpec struct {
	key     string // KeyboardEvent.key
	code    string // KeyboardEvent.code
	keyCode int    // windowsVirtualKeyCode
	text    string // text the key inserts ("" for none)
	rod     input.Key
}

var namedKeys = map[string]keySpec{
	"enter":      {"Enter", "Enter", 13, "\r", input.Enter},
	"tab":        {"Tab", "Tab", 9, "", input.Tab},
	"escape":     {"Escape", "Escape", 27, "", input.Escape},
	"esc":        {"Escape", "Escape", 27, "", input.Escape},
	"backspace":  {"Backspace", "Backspace", 8, "", input.Backspace},
	"delete":     {"Delete", "Delete", 46, "", input.Delete},
	"space":      {" ", "Space", 32, " ", input.Space},
	"arrowup":    {"ArrowUp", "ArrowUp", 38, "", input.ArrowUp},
	"arrowdown":  {"ArrowDown", "ArrowDown", 40, "", input.ArrowDown},
	"arrowleft":  {"ArrowLeft", "ArrowLeft", 37, "", input.ArrowLeft},
	"arrowright": {"ArrowRight", "ArrowRight", 39, "", input.ArrowRight},
	"home":       {"Home", "Home", 36, "", input.Home},
	"end":        {"End", "End", 35, "", input.End},
	"pageup":     {"PageUp", "PageUp", 33, "", input.PageUp},
	"pagedown":   {"PageDown", "PageDown", 34, "", input.PageDown},
}

// modifier bits for CDP Input.dispatchKeyEvent.
var modifierKeys = map[string]struct {
	bit  int
	spec keySpec
}{
	"alt":     {1, keySpec{"Alt", "AltLeft", 18, "", input.AltLeft}},
	"control": {2, keySpec{"Control", "ControlLeft", 17, "", input.ControlLeft}},
	"ctrl":    {2, keySpec{"Control", "ControlLeft", 17, "", input.ControlLeft}},
	"meta":    {4, keySpec{"Meta", "MetaLeft", 91, "", input.MetaLeft}},
	"cmd":     {4, keySpec{"Meta", "MetaLeft", 91, "", input.MetaLeft}},
	"shift":   {8, keySpec{"Shift", "ShiftLeft", 16, "", input.ShiftLeft}},
}

// parseKeyCombo parses "Enter", "a", "Control+a", "Control+Shift+ArrowUp".
func parseKeyCombo(combo string) (mods []keySpec, modBits int, key keySpec, err error) {
	combo = strings.TrimSpace(combo)
	if combo == "" {
		return nil, 0, keySpec{}, fmt.Errorf("no key")
	}
	var parts []string
	switch {
	case combo == "+":
		parts = []string{"+"}
	case strings.HasSuffix(combo, "++"): // "Control++" is Control and the plus key
		parts = append(strings.Split(strings.TrimSuffix(combo, "++"), "+"), "+")
	default:
		parts = strings.Split(combo, "+")
	}
	last := parts[len(parts)-1]
	for _, p := range parts[:len(parts)-1] {
		m, ok := modifierKeys[strings.ToLower(strings.TrimSpace(p))]
		if !ok {
			return nil, 0, keySpec{}, fmt.Errorf("unknown modifier %q in %q", p, combo)
		}
		mods = append(mods, m.spec)
		modBits |= m.bit
	}
	if k, ok := namedKeys[strings.ToLower(last)]; ok {
		return mods, modBits, k, nil
	}
	r := []rune(last)
	if len(r) != 1 || r[0] > 126 || r[0] < 32 {
		return nil, 0, keySpec{}, fmt.Errorf("unknown key %q", last)
	}
	k := keySpec{key: last, text: last, rod: input.Key(r[0])}
	switch c := r[0]; {
	case c >= 'a' && c <= 'z':
		k.code, k.keyCode = "Key"+strings.ToUpper(last), int(c-'a'+'A')
	case c >= 'A' && c <= 'Z':
		k.code, k.keyCode = "Key"+last, int(c)
	case c >= '0' && c <= '9':
		k.code, k.keyCode = "Digit"+last, int(c)
	}
	return mods, modBits, k, nil
}

// cdpRawer is a page that relays raw CDP commands (extension.ExtensionPage).
type cdpRawer interface {
	CDP(method string, params map[string]interface{}) (map[string]interface{}, error)
}

func (ae *ActionExecutor) stepPressKey(ctx context.Context, step StepDef) (*StepResult, error) {
	defer ae.releaseJevMarker()
	combo := ae.resolver.Resolve(step.Key)
	mods, modBits, key, err := parseKeyCombo(combo)
	if err != nil {
		return extFail(step, "%w", err)
	}
	if step.Selector != "" || step.XPath != "" || step.ConfigKey != "" || step.ElementRef != "" {
		elem, resolveErr := ae.resolveElement(step)
		if elem == nil {
			return extFail(step, "%w", noElementError("no element to focus for step %s", step.ID, resolveErr))
		}
		if err := elem.Focus(); err != nil {
			return extFail(step, "focus: %w", err)
		}
	}
	if err := ae.pressKey(mods, modBits, key); err != nil {
		return extFail(step, "press %s: %w", combo, err)
	}
	return &StepResult{Success: true, StepID: step.ID, Data: combo}, nil
}

// pressKey sends a real key press: CDP key events when the page relays CDP,
// Rod key actions on a Rod page, else the driver's KeyboardPress (which has
// no modifiers).
func (ae *ActionExecutor) pressKey(mods []keySpec, modBits int, key keySpec) error {
	if c, ok := ae.page.(cdpRawer); ok {
		send := func(typ string, k keySpec, text string) error {
			p := map[string]interface{}{"type": typ, "key": k.key, "code": k.code, "windowsVirtualKeyCode": k.keyCode, "modifiers": modBits}
			if text != "" {
				p["text"] = text
			}
			_, err := c.CDP("Input.dispatchKeyEvent", p)
			return err
		}
		for _, m := range mods {
			if err := send("rawKeyDown", m, ""); err != nil {
				return err
			}
		}
		text := key.text
		if modBits&(2|4) != 0 { // Control/Meta chords insert nothing
			text = ""
		}
		typ := "rawKeyDown"
		if text != "" {
			typ = "keyDown"
		}
		if err := send(typ, key, text); err != nil {
			return err
		}
		if err := send("keyUp", key, ""); err != nil {
			return err
		}
		for i := len(mods) - 1; i >= 0; i-- {
			if err := send("keyUp", mods[i], ""); err != nil {
				return err
			}
		}
		return nil
	}
	if rp := unwrapRodPage(ae.page); rp != nil {
		ka := rp.Timeout(10 * time.Second).KeyActions()
		for _, m := range mods {
			ka = ka.Press(m.rod)
		}
		return ka.Type(key.rod).Do()
	}
	if len(mods) > 0 {
		return fmt.Errorf("this page driver cannot press modifier keys")
	}
	return ae.page.KeyboardPress(rune(key.rod))
}
