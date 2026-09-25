package action

// Secrets (spec §6.2 item 5, security contract §8 M2/M3/L5).
//
// {{secret:<name>}} resolves to the value of a declared "type": "secret"
// input <name> of the running action, else to the vault secret <name> of
// the CURRENT automation (the lookup receives its id; inside call_action
// that is the called package). A plain variable never shadows it: only a
// declared secret input is read from the variables.
//
// Every secret value that is resolved (and every declared secret input's
// value) is remembered and redacted — replaced by "***" — from step
// results and errors, events, failed items, the execution result, saved
// data and the executor's own logs of step outcomes. Resolved text is never
// resolved a second time, so page text containing "{{secret:x}}" cannot
// pull a secret in.

import (
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"sync"
)

// secretPrefix marks a template path as a secret: {{secret:<name>}}.
const secretPrefix = "secret:"

// minRedactLen: shorter values are not redacted (they would mangle
// unrelated text); secrets that short are not secrets.
const minRedactLen = 3

// secretSet is the set of secret values seen in a run.
type secretSet struct {
	mu   sync.RWMutex
	vals map[string]bool
}

func (s *secretSet) add(v string) {
	if len(v) < minRedactLen {
		return
	}
	s.mu.Lock()
	if s.vals == nil {
		s.vals = map[string]bool{}
	}
	s.vals[v] = true
	s.mu.Unlock()
}

// redact replaces every known secret in text, longest first.
func (s *secretSet) redact(text string) string {
	if s == nil || text == "" {
		return text
	}
	s.mu.RLock()
	vals := make([]string, 0, len(s.vals))
	for v := range s.vals {
		vals = append(vals, v)
	}
	s.mu.RUnlock()
	if len(vals) == 0 {
		return text
	}
	sort.Slice(vals, func(i, j int) bool { return len(vals[i]) > len(vals[j]) })
	for _, v := range vals {
		text = strings.ReplaceAll(text, v, "***")
	}
	return text
}

// SetSecretLookup installs the vault lookup used by {{secret:<name>}}; it
// receives the current automation id and the secret name.
func (vr *VariableResolver) SetSecretLookup(fn func(automationID, name string) (string, bool)) {
	vr.secrets = fn
}

// resolveSecret resolves {{secret:<name>}} (see the file comment).
func (vr *VariableResolver) resolveSecret(name string) interface{} {
	if vr.maskSecrets {
		return "***"
	}
	name = strings.TrimSpace(name)
	if vr.declaredSecret != nil && vr.declaredSecret(name) {
		if v, ok := vr.context.GetVariable(name); ok && v != nil {
			if s, isStr := v.(string); isStr {
				vr.secretVals.add(s)
			}
			return v
		}
	}
	if vr.secrets != nil {
		scope := ""
		if vr.scope != nil {
			scope = vr.scope()
		}
		if v, ok := vr.secrets(scope, name); ok {
			vr.secretVals.add(v)
			return v
		}
	}
	return nil
}

// SetSecretLookup lets {{secret:<name>}} fall back to the vault secret of
// the current automation (spec §6.2). The runtime wires it to the profile's
// vault; values are redacted everywhere the executor reports them.
func (ae *ActionExecutor) SetSecretLookup(fn func(automationID, name string) (string, bool)) {
	ae.resolver.SetSecretLookup(fn)
}

// secretScope is the automation id secrets are looked up under.
func (ae *ActionExecutor) secretScope() string {
	if ae.pkg != nil {
		return ae.pkg.ID()
	}
	if ae.action != nil {
		return strings.ToLower(ae.action.TargetPlatform)
	}
	return ""
}

// secretInputNames returns the running action's inputs declared "secret".
func (ae *ActionExecutor) secretInputNames() map[string]bool {
	out := map[string]bool{}
	if ae.actionDef == nil || ae.actionDef.Inputs == nil {
		return out
	}
	for _, raw := range append(append([]json.RawMessage{}, ae.actionDef.Inputs.Required...), ae.actionDef.Inputs.Optional...) {
		if name, typ := inputNameType(raw); name != "" && strings.EqualFold(typ, "secret") {
			out[name] = true
		}
	}
	return out
}

// registerSecretInputs remembers the values of declared secret inputs, so
// they are redacted even when used as a plain {{name}}.
func (ae *ActionExecutor) registerSecretInputs() {
	for name := range ae.secretInputNames() {
		if v, ok := ae.execCtx.GetVariable(name); ok {
			if s, isStr := v.(string); isStr {
				ae.resolver.secretVals.add(s)
			}
		}
	}
}

// redact removes known secret values from text.
func (ae *ActionExecutor) redact(text string) string { return ae.resolver.secretVals.redact(text) }

// redactedError carries a redacted message but unwraps to the original, so
// errors.Is(err, ErrAbort / ErrOffDomain / ...) keeps working.
type redactedError struct {
	msg string
	err error
}

func (e *redactedError) Error() string { return e.msg }
func (e *redactedError) Unwrap() error { return e.err }

// redactErr returns err with known secrets removed from its message.
func (ae *ActionExecutor) redactErr(err error) error {
	if err == nil {
		return nil
	}
	msg := err.Error()
	red := ae.redact(msg)
	if red == msg {
		return err
	}
	var already *redactedError
	if errors.As(err, &already) && already == err {
		return &redactedError{msg: red, err: already.err}
	}
	return &redactedError{msg: red, err: err}
}

// redactValue returns v with known secrets removed from every string,
// recursing into maps and slices (copies; v is not modified).
func (ae *ActionExecutor) redactValue(v interface{}) interface{} {
	switch t := v.(type) {
	case string:
		return ae.redact(t)
	case map[string]interface{}:
		out := make(map[string]interface{}, len(t))
		for k, x := range t {
			out[k] = ae.redactValue(x)
		}
		return out
	case []interface{}:
		out := make([]interface{}, len(t))
		for i, x := range t {
			out[i] = ae.redactValue(x)
		}
		return out
	case []map[string]interface{}:
		return ae.redactItems(t)
	case []string:
		out := make([]string, len(t))
		for i, x := range t {
			out[i] = ae.redact(x)
		}
		return out
	}
	return v
}

// redactItems redacts a list of records.
func (ae *ActionExecutor) redactItems(items []map[string]interface{}) []map[string]interface{} {
	if items == nil {
		return nil
	}
	out := make([]map[string]interface{}, len(items))
	for i, m := range items {
		out[i], _ = ae.redactValue(m).(map[string]interface{})
	}
	return out
}

// redactStep redacts a step's outcome before it is recorded or reported.
func (ae *ActionExecutor) redactStep(result *StepResult, err error) (*StepResult, error) {
	if result != nil {
		result.Data = ae.redactValue(result.Data)
		result.Error = ae.redactErr(result.Error)
	}
	return result, ae.redactErr(err)
}

// inputNameType reads an input declaration's name and type (the legacy
// string form has no type).
func inputNameType(raw json.RawMessage) (name, typ string) {
	var obj struct {
		Name string `json:"name"`
		Type string `json:"type"`
	}
	if json.Unmarshal(raw, &obj) == nil {
		return strings.TrimSpace(obj.Name), obj.Type
	}
	return requiredInputName(raw), ""
}
