package browserjev

import (
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"runtime"
	"time"
)

// snapshotJS reads the page's visible text and executable controls in one
// Runtime.evaluate and keeps live references to the DOM nodes it saw.
//
//go:embed snapshot.js
var snapshotJS string

// markerJS evaluates only the freshness marker of a fresh snapshot.
var markerJS = "(() => { const state=" + snapshotJS + "; return state?.marker ?? null; })()"

// errStale means a decision no longer refers to the page it was made for.
// The loop observes again and re-decides; it never retries a mutation.
var errStale = errors.New("page changed since it was observed")

// cdpPage is the one capability the node needs from a tab: raw CDP.
// *extension.ExtensionPage satisfies it through the extension's relay.
type cdpPage interface {
	CDP(method string, params map[string]interface{}) (map[string]interface{}, error)
}

// action is one executable choice from the snapshot. Node is the code-owned
// DOM identity; the model only ever sees an index that maps back to it.
type action struct {
	ID           string `json:"id"`
	Kind         string `json:"kind"` // click, fill, select, scroll, wait
	Label        string `json:"label"`
	Role         string `json:"role,omitempty"`
	Value        string `json:"value,omitempty"`
	CurrentValue string `json:"current_value,omitempty"`
	Checked      string `json:"checked,omitempty"`
	Selected     string `json:"selected,omitempty"`
	Expanded     string `json:"expanded,omitempty"`
	Node         int    `json:"node,omitempty"`
	Delta        int    `json:"delta,omitempty"`
}

// pageState is one observation.
type pageState struct {
	URL            string                     `json:"url"`
	Title          string                     `json:"title"`
	Text           string                     `json:"text"`
	Scroll         json.RawMessage            `json:"scroll"`
	Actions        []action                   `json:"actions"`
	Marker         json.RawMessage            `json:"marker"`
	PageKey        json.RawMessage            `json:"page_key"`
	Guards         map[string]json.RawMessage `json:"guards"`
	OmittedActions int                        `json:"omitted_actions"`
	Fingerprint    string                     `json:"-"`
}

func (p *pageState) find(id string) *action {
	for i := range p.Actions {
		if p.Actions[i].ID == id {
			return &p.Actions[i]
		}
	}
	return nil
}

// driver is what the agent loop needs from a browser; cdpBrowser is the real
// one, tests substitute a fake.
type driver interface {
	observe(ctx context.Context) (*pageState, error)
	fresh(page *pageState, a *action) (bool, error)
	act(ctx context.Context, page *pageState, a *action, text string) error
}

type cdpBrowser struct {
	page       cdpPage
	afterInput *action
}

// setup gives the owned tab a fixed viewport and keeps rAF running while the
// tab is in the background, so menus and autocompletes still render.
func (b *cdpBrowser) setup(width, height int) error {
	if _, err := b.page.CDP("Emulation.setDeviceMetricsOverride", map[string]interface{}{
		"width": width, "height": height, "deviceScaleFactor": 1, "mobile": false,
	}); err != nil {
		return err
	}
	_, err := b.page.CDP("Emulation.setFocusEmulationEnabled", map[string]interface{}{"enabled": true})
	return err
}

// evaluate runs expression and decodes its value into out (when non-nil).
// A thrown exception means the document changed under us.
func (b *cdpBrowser) evaluate(expression string, await bool, out interface{}) error {
	res, err := b.page.CDP("Runtime.evaluate", map[string]interface{}{
		"expression": expression, "returnByValue": true, "awaitPromise": await,
	})
	if err != nil {
		return err
	}
	if res["exceptionDetails"] != nil {
		return errStale
	}
	if out == nil {
		return nil
	}
	inner, _ := res["result"].(map[string]interface{})
	raw, err := json.Marshal(inner["value"])
	if err != nil {
		return err
	}
	return json.Unmarshal(raw, out)
}

func (b *cdpBrowser) observe(ctx context.Context) (*pageState, error) {
	if a := b.afterInput; a != nil {
		b.afterInput = nil
		// Read-only settle: two animation frames, or up to 200ms for an
		// editable combobox's suggestions to appear. Errors don't matter.
		_ = b.evaluate(settleJS(a), true, nil)
	}
	var lastErr error
	for attempt := 0; attempt < 10; attempt++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		var page *pageState
		err := b.evaluate(snapshotJS, false, &page)
		if err == nil && page == nil {
			err = errStale // document.body missing: navigating
		}
		if err == nil {
			page.Fingerprint = fingerprint(page)
			return page, nil
		}
		if !errors.Is(err, errStale) {
			return nil, err
		}
		lastErr = err
		time.Sleep(20 * time.Millisecond)
	}
	return nil, fmt.Errorf("page did not settle: %w", lastErr)
}

func (b *cdpBrowser) fresh(page *pageState, a *action) (bool, error) {
	if a != nil && (a.Kind == "click" || a.Kind == "select") {
		// Scoped guard: the document, form state and the target's own
		// neighbourhood must be unchanged; unrelated content may move.
		var current []json.RawMessage
		err := b.evaluate(fmt.Sprintf(
			"(() => { const c=window.__jevFast; return c ? [c.pageKey(),c.guard(c.nodes.get(%d))] : null; })()",
			a.Node), false, &current)
		if errors.Is(err, errStale) {
			return false, nil
		}
		if err != nil || len(current) != 2 {
			return false, err
		}
		return sameJSON(current[0], page.PageKey) && sameJSON(current[1], page.Guards[fmt.Sprint(a.Node)]), nil
	}
	var marker json.RawMessage
	err := b.evaluate(markerJS, false, &marker)
	if errors.Is(err, errStale) {
		return false, nil
	}
	return err == nil && sameJSON(marker, page.Marker), err
}

func (b *cdpBrowser) act(ctx context.Context, page *pageState, a *action, text string) error {
	ok, err := b.fresh(page, a)
	if err != nil {
		return err
	}
	if !ok {
		return errStale
	}
	switch a.Kind {
	case "wait":
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
		return nil
	case "scroll":
		_, err := b.page.CDP("Input.dispatchMouseEvent", map[string]interface{}{
			"type": "mouseWheel", "x": 550, "y": 650, "deltaX": 0, "deltaY": a.Delta,
		})
		b.afterInput = a
		return err
	case "click", "fill", "select":
	default:
		return fmt.Errorf("unknown action kind %q", a.Kind)
	}
	if a.Node <= 0 {
		return fmt.Errorf("action %s has no observed node", a.ID)
	}
	spec, _ := json.Marshal(a)
	var target *struct{ X, Y float64 }
	err = b.evaluate("("+resolveTargetJS+")("+string(spec)+")", false, &target)
	if err != nil && a.Kind == "select" {
		// Its change event may already have fired; don't guess.
		return fmt.Errorf("dropdown execution was interrupted; inspect before retrying: %w", err)
	}
	if err != nil {
		return err
	}
	if target == nil {
		if a.Kind == "select" {
			return errors.New("dropdown execution was not confirmed; inspect before retrying")
		}
		return fmt.Errorf("target changed or is covered: %w", errStale)
	}
	b.afterInput = a
	if a.Kind == "select" {
		return nil // set in-page, with input/change events
	}
	for _, typ := range []string{"mousePressed", "mouseReleased"} {
		if _, err := b.page.CDP("Input.dispatchMouseEvent", map[string]interface{}{
			"type": typ, "x": target.X, "y": target.Y, "button": "left", "clickCount": 1,
		}); err != nil {
			return err
		}
	}
	if a.Kind != "fill" {
		return nil
	}
	// Select-all then insert, so existing contents are replaced.
	modifier := 2 // Ctrl
	if runtime.GOOS == "darwin" {
		modifier = 4 // Meta
	}
	for _, typ := range []string{"keyDown", "keyUp"} {
		params := map[string]interface{}{"type": typ, "key": "a", "code": "KeyA", "modifiers": modifier}
		if typ == "keyDown" {
			params["commands"] = []string{"selectAll"}
		}
		if _, err := b.page.CDP("Input.dispatchKeyEvent", params); err != nil {
			return err
		}
	}
	_, err = b.page.CDP("Input.insertText", map[string]interface{}{"text": text})
	return err
}

// resolveTargetJS re-resolves the observed node, rejects disabled, hidden,
// off-screen or covered targets, performs a native <select> in place, and
// returns the click point for everything else.
const resolveTargetJS = `action => {
  const e=window.__jevFast?.nodes.get(action.node);
  if (!e?.isConnected || e.matches(':disabled') || e.closest('[aria-disabled="true"],[inert]') ||
      !e.checkVisibility({checkOpacity:true,checkVisibilityCSS:true})) return null;
  if (action.kind==='fill' && (e.readOnly || e.getAttribute('aria-readonly')==='true')) return null;
  const r=e.getBoundingClientRect(), x=r.x+r.width/2, y=r.y+r.height/2;
  if (!r.width || !r.height || x<0 || y<0 || x>=innerWidth || y>=innerHeight) return null;
  if (!e.contains(document.elementFromPoint(x,y))) return null;
  if (action.kind==='select') {
    if (e.tagName!=='SELECT' || ![...e.options].some(o=>o.value===action.value &&
        !o.disabled && !o.closest('optgroup[disabled]'))) return null;
    e.value=action.value;
    e.dispatchEvent(new Event('input',{bubbles:true}));
    e.dispatchEvent(new Event('change',{bubbles:true}));
  }
  return {x,y};
}`

func settleJS(a *action) string {
	spec, _ := json.Marshal(a)
	return `(action => new Promise(resolve => {
  const field=window.__jevFast?.nodes.get(action.node);
  const autocomplete=action.kind==='fill' && field?.getAttribute('role')==='combobox';
  let frames=0, stopped=false;
  const finish=()=>{stopped=true;resolve()};
  setTimeout(finish,autocomplete ? 200 : 50);
  const ready=()=>{
    if (stopped) return;
    const ids=(field?.getAttribute('aria-controls')||field?.getAttribute('aria-owns')||'').split(/\s+/).filter(Boolean);
    const roots=ids.length ? ids.map(id=>document.getElementById(id)).filter(Boolean) : [document];
    const options=roots.flatMap(root=>[...root.querySelectorAll('[role="option"]')]);
    if (++frames>=2 && (!autocomplete || options.some(e=>{
      const r=e.getBoundingClientRect();
      return r.width && r.height && r.bottom>0 && r.top<innerHeight &&
        e.checkVisibility({checkOpacity:true,checkVisibilityCSS:true});
    }))) finish();
    else requestAnimationFrame(ready);
  };
  requestAnimationFrame(ready);
}))(` + string(spec) + `)`
}

// fingerprint hashes what the model saw: url, text, actions, scroll.
func fingerprint(p *pageState) string {
	raw, _ := json.Marshal(map[string]interface{}{"url": p.URL, "text": p.Text, "actions": p.Actions, "scroll": p.Scroll})
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func sameJSON(a, b json.RawMessage) bool {
	var x, y interface{}
	if json.Unmarshal(a, &x) != nil || json.Unmarshal(b, &y) != nil {
		return false
	}
	return reflect.DeepEqual(x, y)
}
