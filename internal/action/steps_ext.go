package action

// Extended (package-era) step handlers: call_fragment, call_action,
// for_each, wait_for, assert, select_option, press_key, extract_table,
// extract_json, transform, page_script, http_fetch_in_page, download.
// Owned by the action-steps builder; see the contracts doc.
//
// ResolveStepDef does not resolve the package-era fields (Items, Input,
// Inputs, Key, Script, Fields, Path, Until, Steps); each handler resolves
// what it needs itself, and nested Steps are resolved one at a time when
// executeSteps runs them, so a body sees the variables bound for it.

import (
	"encoding/json"
	"fmt"
	"strings"
)

func (ae *ActionExecutor) initExtendedHandlers() {
	ae.handlers["call_fragment"] = ae.stepCallFragment
	ae.handlers["call_action"] = ae.stepCallAction
	ae.handlers["for_each"] = ae.stepForEach
	ae.handlers["wait_for"] = ae.stepWaitFor
	ae.handlers["assert"] = ae.stepAssert
	ae.handlers["select_option"] = ae.stepSelectOption
	ae.handlers["press_key"] = ae.stepPressKey
	ae.handlers["extract_table"] = ae.stepExtractTable
	ae.handlers["extract_json"] = ae.stepExtractJSON
	ae.handlers["transform"] = ae.stepTransform
	ae.handlers["page_script"] = ae.stepPageScript
	ae.handlers["http_fetch_in_page"] = ae.stepHTTPFetchInPage
	ae.handlers["download"] = ae.stepDownload
}

// extFail is the failed StepResult every extended handler returns: errors
// travel in the result (like the core steps), so onError policies apply.
func extFail(step StepDef, format string, args ...interface{}) (*StepResult, error) {
	cause := fmt.Errorf(format, args...)
	return &StepResult{Success: false, StepID: step.ID, Error: fmt.Errorf("%s step %s: %w", step.Type, step.ID, cause)}, nil
}

// extOutputVar is where a step stores its result: variable_name, else
// variable.
func extOutputVar(step StepDef) string {
	if step.VariableName != "" {
		return step.VariableName
	}
	return step.Variable
}

// extStore saves a step's result under its output variable (if any) and
// returns the successful StepResult carrying it.
func (ae *ActionExecutor) extStore(step StepDef, data interface{}) *StepResult {
	if v := extOutputVar(step); v != "" {
		ae.execCtx.SetVariable(v, data)
	}
	return &StepResult{Success: true, StepID: step.ID, Data: data}
}

// extResolve resolves a package-era template field, preserving the value's
// type. "{{x}}" and a bare path "x.y" both resolve through the variable
// resolver; anything with mixed text is interpolated as a string.
func (ae *ActionExecutor) extResolve(tmpl string) interface{} {
	t := strings.TrimSpace(tmpl)
	if t == "" {
		return nil
	}
	if strings.Contains(t, "{{") {
		v := ae.resolver.ResolveValue(t)
		if s, ok := v.(string); ok && s == "" {
			return nil
		}
		return v
	}
	return ae.resolver.ResolvePath(t)
}

// extResolveInputs resolves every value of a step's Inputs map.
func (ae *ActionExecutor) extResolveInputs(in map[string]interface{}) map[string]interface{} {
	out := make(map[string]interface{}, len(in))
	for k, v := range in {
		out[k] = ae.resolver.ResolveValue(v)
	}
	return out
}

// boundVar remembers a variable's value before a body rebinds it.
type boundVar struct {
	name    string
	value   interface{}
	existed bool
}

// bindVars sets vars in the execution context and returns a restore func
// that puts back the previous values (deleting the ones that did not exist).
func (ae *ActionExecutor) bindVars(vars map[string]interface{}) func() {
	ec := ae.execCtx
	ec.mu.Lock()
	saved := make([]boundVar, 0, len(vars))
	for k, v := range vars {
		old, ok := ec.Variables[k]
		saved = append(saved, boundVar{name: k, value: old, existed: ok})
		ec.Variables[k] = v
	}
	ec.mu.Unlock()
	return func() {
		ec.mu.Lock()
		defer ec.mu.Unlock()
		for _, b := range saved {
			if b.existed {
				ec.Variables[b.name] = b.value
			} else {
				delete(ec.Variables, b.name)
			}
		}
	}
}

// bodyResult turns the error of a nested executeSteps call into the
// enclosing step's result. An abort (or a safe-mode stop) inside the body
// aborts the enclosing step list too; a cancelled context fails the step.
func (ae *ActionExecutor) bodyResult(step StepDef, err error, data interface{}) (*StepResult, error) {
	if err == nil && ae.safeStop == nil {
		return &StepResult{Success: true, StepID: step.ID, Data: data}, nil
	}
	if err == ErrAbort || ae.safeStop != nil {
		if err == nil {
			err = fmt.Errorf("stopped before side-effect step %s", ae.safeStop.StepID)
		}
		return &StepResult{Success: false, Abort: true, StepID: step.ID, Error: err, Data: data}, nil
	}
	return extFail(step, "%w", err)
}

// toList turns a resolved value into a list: slices as they are, a JSON
// array string parsed. ok is false for anything that is not a list.
func toList(v interface{}) ([]interface{}, bool) {
	if s, ok := v.(string); ok {
		t := strings.TrimSpace(s)
		if strings.HasPrefix(t, "[") {
			var arr []interface{}
			if err := json.Unmarshal([]byte(t), &arr); err == nil {
				return arr, true
			}
		}
		return nil, false
	}
	if !isIterable(v) {
		return nil, false
	}
	l := toSlice(v)
	if l == nil {
		l = []interface{}{}
	}
	return l, true
}
