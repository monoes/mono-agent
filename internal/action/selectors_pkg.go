package action

// Package selectors (spec §4.4): a step's configKey resolves first through
// the package's selectors.json candidates, in order — css, xpath, aria (role
// + accessible name) or text — before the legacy ConfigInterface lookup.
//
// aria and text candidates are matched by an in-page script (there is no
// selector syntax for them). Like the Jev fallback (jevfallback.go), the
// script bridges the match to the page driver through a DOM marker
// attribute: it tags the element with [data-monoagent-sel='<token>'] and the
// driver then selects it by CSS. Markers are removed when the step ends
// (releaseSelectorMarkers, called from afterStep).
//
// Every lookup of a package selector reports its outcome to the selector
// observer (selector health, spec §8.7): healed means a non-first candidate,
// or a fallback after all candidates missed, found the element.

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/monoes/mono-agent/internal/browser"
)

// selMarkAttr is the attribute aria/text candidate matches are tagged with.
const selMarkAttr = "data-monoagent-sel"

// defaultPkgSelectorTimeout bounds a package-selector lookup whose caller
// has no step timeout.
const defaultPkgSelectorTimeout = 10 * time.Second

// pkgSelectorEntry returns the package selector for key, if the executor has
// a package and the package defines key.
func (ae *ActionExecutor) pkgSelectorEntry(key string) (*SelectorEntry, bool) {
	if ae.pkg == nil || key == "" || ae.page == nil {
		return nil, false
	}
	e, ok := ae.pkg.Selector(key)
	if !ok || e == nil || len(e.Candidates) == 0 {
		return nil, false
	}
	return e, true
}

// observeSelector reports a package-selector outcome to the observer.
func (ae *ActionExecutor) observeSelector(key string, idx int, ok, healed bool) {
	if ae.selObs == nil || ae.pkg == nil {
		return
	}
	ae.selObs.ObserveSelector(ae.pkg.ID(), key, idx, ok, healed)
}

// findPkgCandidate polls entry's candidates in order until one matches or
// timeout passes. It returns the matching index, a CSS/XPath selector that
// selects the element, and the element.
func (ae *ActionExecutor) findPkgCandidate(entry *SelectorEntry, timeout time.Duration) (int, string, browser.ElementHandle, error) {
	deadline := time.Now().Add(timeout)
	var firstErr error
	for {
		for i, c := range entry.Candidates {
			probe := raceProbeTimeout
			if rem := time.Until(deadline); rem < probe {
				probe = rem
			}
			if probe <= 0 {
				probe = time.Millisecond
			}
			sel, err := ae.candidateSelector(c)
			if err == nil && sel != "" {
				var el browser.ElementHandle
				el, err = findElement(ae.page, sel, probe)
				if err == nil && el != nil {
					return i, sel, el, nil
				}
			}
			if err != nil && firstErr == nil {
				firstErr = err
			}
		}
		if time.Until(deadline) <= 0 {
			break
		}
		time.Sleep(minDuration(racePollInterval, time.Until(deadline)))
		if time.Until(deadline) <= 0 {
			break
		}
	}
	if firstErr == nil {
		firstErr = fmt.Errorf("no candidate matched")
	}
	return -1, "", nil, fmt.Errorf("none of %d selector candidates matched within %v: %w", len(entry.Candidates), timeout, firstErr)
}

// candidateSelector turns one candidate into a CSS or XPath selector string.
// aria/text candidates are matched in the page and marked; "" with a nil
// error means the candidate matched nothing right now.
func (ae *ActionExecutor) candidateSelector(c SelectorCandidate) (string, error) {
	switch {
	case c.CSS != "":
		return c.CSS, nil
	case c.XPath != "":
		return c.XPath, nil
	case c.Aria != nil && (c.Aria.Role != "" || c.Aria.Name != ""):
		return ae.markInPage(map[string]string{"role": c.Aria.Role, "name": c.Aria.Name})
	case strings.TrimSpace(c.Text) != "":
		return ae.markInPage(map[string]string{"text": c.Text})
	}
	return "", fmt.Errorf("empty selector candidate")
}

// markInPage runs the aria/text matcher; on a match it returns the marker
// selector and remembers the token for cleanup.
func (ae *ActionExecutor) markInPage(want map[string]string) (string, error) {
	token, err := randomToken()
	if err != nil {
		return "", err
	}
	wantJSON, _ := json.Marshal(want)
	matched, err := evalPageBool(ae.page, fmt.Sprintf(ariaMatcherJS, string(wantJSON), selMarkAttr, token))
	if err != nil {
		return "", fmt.Errorf("aria/text matcher: %w", err)
	}
	if !matched {
		return "", nil
	}
	ae.selMarkers = append(ae.selMarkers, token)
	return fmt.Sprintf(`[%s="%s"]`, selMarkAttr, token), nil
}

// releaseSelectorMarkers removes this step's aria/text markers. Errors are
// ignored: the page may have navigated away, taking them along.
func (ae *ActionExecutor) releaseSelectorMarkers() {
	if len(ae.selMarkers) == 0 || ae.page == nil {
		ae.selMarkers = nil
		return
	}
	for _, tok := range ae.selMarkers {
		_, _ = evalPageBool(ae.page, fmt.Sprintf(
			`(() => { document.querySelectorAll('[%s="%s"]').forEach(e => e.removeAttribute(%q)); return true; })()`,
			selMarkAttr, tok, selMarkAttr))
	}
	ae.selMarkers = nil
}

// evalPageBool evaluates an expression in the page's main world — through
// CDP when the page supports it (not subject to the page CSP), else Eval —
// and reports whether it returned true.
func evalPageBool(page browser.PageInterface, expr string) (bool, error) {
	if ev, ok := page.(cdpEvaluator); ok {
		v, err := ev.EvalCDP(expr)
		if err != nil {
			return false, err
		}
		b, _ := v.(bool)
		return b, nil
	}
	res, err := page.Eval("() => " + expr)
	if err != nil {
		return false, err
	}
	if res == nil {
		return false, nil
	}
	return res.Bool(), nil
}

// resolvePkgElement finds the element for a step whose configKey is a
// package selector. Order: candidates → legacy ConfigInterface →
// alternatives → Jev (step intent, else the selector's intent). One
// observation is reported per lookup.
func (ae *ActionExecutor) resolvePkgElement(step StepDef, entry *SelectorEntry, timeout time.Duration) (browser.ElementHandle, error) {
	key := step.ConfigKey
	idx, _, el, err := ae.findPkgCandidate(entry, timeout)
	if el != nil {
		ae.observeSelector(key, idx, true, idx > 0)
		return el, nil
	}
	cause := fmt.Errorf("selector %q: %w", key, err)

	if sel := ae.legacyConfigSelector(key); sel != "" {
		if el, lerr := findElement(ae.page, sel, timeout); lerr == nil && el != nil {
			ae.observeSelector(key, -1, true, true)
			return el, nil
		}
	}
	if len(step.Alternatives) > 0 {
		if _, el, aerr := findFirst(ae.page, step.Alternatives, timeout); aerr == nil && el != nil {
			ae.observeSelector(key, -1, true, true)
			return el, nil
		}
	}
	jevStep := step
	if strings.TrimSpace(jevStep.Intent) == "" {
		jevStep.Intent = entry.Intent
	}
	el, err = ae.jevResolve(jevStep, cause)
	if err == nil && el != nil {
		ae.observeSelector(key, -1, true, true)
		return el, nil
	}
	ae.observeSelector(key, -1, false, false)
	return nil, err
}

// resolvePkgSelectorString is the string form used by steps that need a
// selector rather than an element (wait, find_element lists): the matching
// candidate's selector, or "" when none matched (the caller then falls back
// to the legacy lookup).
func (ae *ActionExecutor) resolvePkgSelectorString(key string, entry *SelectorEntry, timeout time.Duration) string {
	idx, sel, _, _ := ae.findPkgCandidate(entry, timeout)
	if sel == "" {
		ae.observeSelector(key, -1, false, false)
		return ""
	}
	ae.observeSelector(key, idx, true, idx > 0)
	return sel
}

// ariaMatcherJS finds the first element (visible preferred) matching
// %[1]s = {role, name} (ARIA role and accessible name) or {text} (exact
// visible text), tags it with %[2]s=%[3]s and returns true.
const ariaMatcherJS = `(() => {
  const want = %[1]s;
  const norm = s => (s || '').replace(/\s+/g, ' ').trim();
  const eq = (a, b) => norm(a).toLowerCase() === norm(b).toLowerCase();
  const implicitRoles = el => {
    const t = el.tagName.toLowerCase();
    if (/^h[1-6]$/.test(t)) return ['heading'];
    switch (t) {
      case 'button': return ['button'];
      case 'a': case 'area': return el.hasAttribute('href') ? ['link'] : [];
      case 'textarea': return ['textbox'];
      case 'select': return (el.multiple || el.size > 1) ? ['listbox'] : ['combobox'];
      case 'img': return el.getAttribute('alt') === '' ? ['presentation'] : ['img'];
      case 'nav': return ['navigation'];
      case 'main': return ['main'];
      case 'dialog': return ['dialog'];
      case 'option': return ['option'];
      case 'li': return ['listitem'];
      case 'ul': case 'ol': return ['list'];
      case 'form': return ['form'];
      case 'table': return ['table'];
      case 'tr': return ['row'];
      case 'td': return ['cell'];
      case 'th': return ['columnheader'];
      case 'input': {
        const ty = (el.getAttribute('type') || 'text').toLowerCase();
        if (['button', 'submit', 'reset', 'image'].includes(ty)) return ['button'];
        if (ty === 'checkbox') return ['checkbox'];
        if (ty === 'radio') return ['radio'];
        if (ty === 'range') return ['slider'];
        if (ty === 'number') return ['spinbutton', 'textbox'];
        if (ty === 'search') return ['searchbox', 'textbox'];
        if (ty === 'hidden' || ty === 'file' || ty === 'color') return [];
        return ['textbox'];
      }
    }
    if (el.isContentEditable && el.parentElement && !el.parentElement.isContentEditable) return ['textbox'];
    return [];
  };
  const roles = el => {
    const r = (el.getAttribute('role') || '').trim().toLowerCase();
    return r ? r.split(/\s+/) : implicitRoles(el);
  };
  const textOf = e => e ? (e.innerText || e.textContent || '') : '';
  const accName = el => {
    const al = el.getAttribute('aria-label');
    if (norm(al)) return norm(al);
    const lb = el.getAttribute('aria-labelledby');
    if (lb) {
      const s = lb.split(/\s+/).map(id => document.getElementById(id)).filter(Boolean).map(textOf).join(' ');
      if (norm(s)) return norm(s);
    }
    if (el.id) {
      try {
        const l = document.querySelector('label[for="' + CSS.escape(el.id) + '"]');
        if (l && norm(textOf(l))) return norm(textOf(l));
      } catch (e) {}
    }
    const wl = el.closest('label');
    if (wl && norm(textOf(wl))) return norm(textOf(wl));
    for (const a of ['alt', 'title', 'placeholder']) {
      const v = el.getAttribute(a);
      if (norm(v)) return norm(v);
    }
    const t = el.tagName.toLowerCase();
    if (t === 'input' && ['button', 'submit', 'reset'].includes((el.getAttribute('type') || '').toLowerCase())) {
      return norm(el.value);
    }
    if (t === 'input' || t === 'textarea' || t === 'select') return '';
    return norm(textOf(el));
  };
  const visible = el => el.getClientRects().length > 0;
  const all = Array.from(document.querySelectorAll('body *'));
  let matches;
  if (want.text !== undefined) {
    matches = all.filter(el => eq(textOf(el), want.text));
    matches = matches.filter(m => !matches.some(o => o !== m && m.contains(o)));
  } else {
    const role = (want.role || '').toLowerCase();
    matches = all.filter(el => (!role || roles(el).includes(role)) && (!want.name || eq(accName(el), want.name)));
  }
  const pick = matches.find(visible) || matches[0];
  if (!pick) return false;
  pick.setAttribute(%[2]q, %[3]q);
  return true;
})()`

// storeFoundElement records a find_element result under the step id and its
// variable names, as stepFindElement does for selector lists.
func (ae *ActionExecutor) storeFoundElement(step StepDef, elem browser.ElementHandle) {
	ae.execCtx.SetElement(step.ID, elem)
	if step.Variable != "" {
		ae.execCtx.SetElement(step.Variable, elem)
	}
	if step.VariableName != "" {
		ae.execCtx.SetElement(step.VariableName, elem)
		ae.execCtx.SetVariable(step.VariableName, true)
	}
}

func firstNonEmptyStr(xs ...string) string {
	for _, x := range xs {
		if x != "" {
			return x
		}
	}
	return ""
}

// SelectorString resolves a step's target to a selector string for handlers
// that query the page themselves (extract_table, extract_json) rather than
// taking an element handle. Exactly one of css/xpath is set on success.
// Order: xpath → selector → configKey via the package's selectors.json
// (first candidate matching at least one element; aria/text candidates come
// back as a [data-monoagent-sel] marker selector, removed when the step
// ends) → the legacy ConfigInterface.
func (ae *ActionExecutor) SelectorString(ctx context.Context, step StepDef) (css string, xpath string, err error) {
	if ctx != nil {
		if err := ctx.Err(); err != nil {
			return "", "", err
		}
	}
	split := func(sel string) (string, string) {
		if isXPath(sel) {
			return "", sel
		}
		return sel, ""
	}
	switch {
	case step.XPath != "":
		return "", step.XPath, nil
	case step.Selector != "":
		css, xpath = split(step.Selector)
		return css, xpath, nil
	case step.ConfigKey == "":
		return "", "", fmt.Errorf("step %s: no selector, xpath or configKey", step.ID)
	}
	if entry, ok := ae.pkgSelectorEntry(step.ConfigKey); ok {
		if sel := ae.resolvePkgSelectorString(step.ConfigKey, entry, stepTimeout(step, 10)); sel != "" {
			css, xpath = split(sel)
			return css, xpath, nil
		}
	}
	if sel := ae.legacyConfigSelector(step.ConfigKey); sel != "" {
		css, xpath = split(sel)
		return css, xpath, nil
	}
	return "", "", fmt.Errorf("step %s: config key %q resolved to no selector", step.ID, step.ConfigKey)
}
