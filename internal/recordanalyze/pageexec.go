package recordanalyze

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/rs/zerolog"

	"github.com/monoes/mono-agent/internal/action"
	"github.com/monoes/mono-agent/internal/browser"
)

// PageExecOptions configures the production replay.
type PageExecOptions struct {
	// Secrets resolves {{secret:x}} templates whose value is not an input
	// (inputs are resolved first).
	Secrets func(name string) (string, bool)
	// KeepOpen leaves the tab open after the run. Otherwise the tab is
	// closed, except when safe mode stopped and the element was outlined.
	KeepOpen bool
}

// PageExec replays through the normal ActionExecutor on page, running the
// uninstalled draft definition with ExecuteDef (which validates first).
func PageExec(page browser.PageInterface, logger zerolog.Logger) ExecFunc {
	return PageExecWith(page, logger, PageExecOptions{})
}

// PageExecWithSecrets is PageExec with a vault lookup.
func PageExecWithSecrets(page browser.PageInterface, logger zerolog.Logger, lookup func(string) (string, bool)) ExecFunc {
	return PageExecWith(page, logger, PageExecOptions{Secrets: lookup})
}

// PageExecWith is PageExec with options.
func PageExecWith(page browser.PageInterface, logger zerolog.Logger, o PageExecOptions) ExecFunc {
	return func(ctx context.Context, def *action.ActionDef, pkg action.PackageContext, inputs map[string]any, safe bool, obs action.SelectorObserver) RunOutcome {
		events := make(chan action.ExecutionEvent, 8192)
		ae := action.NewActionExecutor(ctx, page, nil, nil, events, nil, logger)
		ae.SetPackage(pkg)
		ae.SetSafeMode(safe)
		ae.SetSelectorObserver(obs)
		if lookup := o.Secrets; lookup != nil {
			ae.SetSecretLookup(func(_, name string) (string, bool) { return lookup(name) })
		}
		params := map[string]interface{}{}
		for k, v := range inputs {
			params[k] = v
			ae.SetVariable(k, v)
		}
		res, err := ae.ExecuteDef(&action.StorageAction{
			ID: "verify-" + time.Now().UTC().Format("20060102T150405"), Type: def.ActionType,
			TargetPlatform: pkg.ID(), Params: params,
		}, def)
		close(events)
		out := RunOutcome{Result: res, Err: err, SafeStop: ae.SafeStopped()}
		for ev := range events {
			out.Events = append(out.Events, ev)
		}
		if out.SafeStop != nil {
			if st := findStep(def, out.SafeStop.StepID); st != nil {
				out.Highlighted = highlight(ctx, ae, page, *st)
			}
		}
		if out.Highlighted || o.KeepOpen {
			out.TabLeftOpen = true
		} else if err := page.Close(); err != nil {
			out.TabLeftOpen = true
			logger.Debug().Err(err).Msg("close verify tab")
		}
		return out
	}
}

// findStep finds a step by id anywhere in def (nested bodies included).
func findStep(def *action.ActionDef, id string) *action.StepDef {
	var found *action.StepDef
	walkSteps(def.Steps, func(s *action.StepDef) {
		if found == nil && s.ID == id {
			found = s
		}
	})
	return found
}

// highlightJS outlines and scrolls to the element matched by css or xpath.
const highlightJS = `() => {
  const css = %s, xp = %s;
  const el = xp ? document.evaluate(xp, document, null, XPathResult.FIRST_ORDERED_NODE_TYPE, null).singleNodeValue
                : document.querySelector(css);
  if (!el) return false;
  el.scrollIntoView({block: 'center', inline: 'center'});
  el.style.outline = '3px solid #e11d48';
  el.style.outlineOffset = '2px';
  el.setAttribute('title', 'monoagent verify stopped here (side effect not executed)');
  return true;
}`

// highlight outlines the element step st targets; false when it cannot
// be resolved or the page cannot evaluate script.
func highlight(ctx context.Context, ae *action.ActionExecutor, page browser.PageInterface, st action.StepDef) (ok bool) {
	defer func() {
		if recover() != nil {
			ok = false
		}
	}()
	css, xp, err := ae.SelectorString(ctx, st)
	if err != nil || (css == "" && xp == "") {
		return false
	}
	cssJS, _ := json.Marshal(css)
	xpJS, _ := json.Marshal(xp)
	res, err := page.Eval(fmt.Sprintf(highlightJS, string(cssJS), string(xpJS)))
	if err != nil || res == nil {
		return false
	}
	return res.Bool()
}
