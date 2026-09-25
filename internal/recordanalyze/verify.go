package recordanalyze

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/rs/zerolog"

	"github.com/monoes/mono-agent/internal/action"
	"github.com/monoes/mono-agent/internal/automation"
	"github.com/monoes/mono-agent/internal/browser"
)

// Step statuses of a verify report (contracts §5).
const (
	StatusPass    = "pass"
	StatusHealed  = "healed"
	StatusFail    = "fail"
	StatusStopped = "stopped_before_side_effect"
	StatusSkipped = "skipped"
)

// StepReport is one row of `record verify --json`.
type StepReport struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Status   string `json:"status"`
	Message  string `json:"message,omitempty"`
	Selector string `json:"selector,omitempty"`
}

// VerifyReport is `record verify --json`.
type VerifyReport struct {
	Steps     []StepReport     `json:"steps"`
	StoppedAt *action.SafeStop `json:"stoppedAt"`
	OK        bool             `json:"ok"`
	Healed    []string         `json:"healed,omitempty"` // selector keys promoted in the draft
	Error     string           `json:"error,omitempty"`
}

// Observation is one package-selector lookup seen during the replay.
type Observation struct {
	Key    string
	Index  int
	OK     bool
	Healed bool
}

type observer struct {
	mu  sync.Mutex
	obs []Observation
}

func (o *observer) ObserveSelector(_, key string, idx int, ok, healed bool) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.obs = append(o.obs, Observation{Key: key, Index: idx, OK: ok, Healed: healed})
}

// RunOutcome is what one replay produced.
type RunOutcome struct {
	Events   []action.ExecutionEvent
	Result   *action.ExecutionResult
	Err      error
	SafeStop *action.SafeStop
}

// ExecFunc replays def; the production one is PageExec.
type ExecFunc func(ctx context.Context, def *action.ActionDef, pkg action.PackageContext, inputs map[string]any, safe bool, obs action.SelectorObserver) RunOutcome

// VerifyOptions controls Verify.
type VerifyOptions struct {
	Full bool // run side-effect steps too
	Exec ExecFunc
	// Inputs override or add to the recorded values (secrets are never
	// recorded, so a step typing one needs it here).
	Inputs map[string]any
	// SecretLookup resolves a secret input from the vault when Inputs does
	// not supply it (Inputs always win). Nil = no vault.
	SecretLookup func(name string) (string, bool)
}

// Verify replays the draft in dir (safe mode unless Full), builds the
// per-step report and promotes healed selectors into the draft's
// selectors.json.
func Verify(ctx context.Context, dir string, opts VerifyOptions) (*VerifyReport, error) {
	d, err := ReadDraft(dir)
	if err != nil {
		return nil, err
	}
	p, err := automation.OpenDir(dir)
	if err != nil {
		return nil, fmt.Errorf("open draft: %w", err)
	}
	def, err := p.Action(d.Action)
	if err != nil {
		return nil, fmt.Errorf("draft action %s: %w", d.Action, err)
	}
	if opts.Exec == nil {
		return nil, fmt.Errorf("browser bridge not connected")
	}
	pkg := p.Context()
	o := &observer{}
	inputs := map[string]any{}
	for k, v := range d.RecordedInputs {
		inputs[k] = v
	}
	for k, v := range opts.Inputs {
		inputs[k] = v
	}
	secrets := map[string]any{}
	for k, v := range opts.Inputs {
		secrets[k] = v
	}
	if opts.SecretLookup != nil {
		for _, in := range flatInputs(def, nil) {
			if _, given := inputs[in.Name]; given || !in.Secret {
				continue
			}
			if v, ok := opts.SecretLookup(in.Name); ok {
				inputs[in.Name], secrets[in.Name] = v, v
			}
		}
	}
	out := opts.Exec(ctx, def, pkg, inputs, !opts.Full, o)
	rep := BuildReport(def, out, o.obs, pkg)
	redact(rep, secrets)
	promoted, err := PromoteHealed(dir, o.obs)
	if err != nil {
		return rep, err
	}
	rep.Healed = promoted
	return rep, nil
}

// BuildReport turns a replay outcome into per-step statuses for the
// action's top-level steps.
func BuildReport(def *action.ActionDef, out RunOutcome, obs []Observation, pkg action.PackageContext) *VerifyReport {
	started, done := map[string]bool{}, map[string]bool{}
	for _, ev := range out.Events {
		switch ev.Type {
		case "step_start":
			started[ev.StepID] = true
		case "step_complete":
			done[ev.StepID] = true
		}
	}
	failed := map[string]string{}
	if out.Result != nil {
		for _, f := range out.Result.FailedItems {
			msg := "failed"
			if f.Error != nil {
				msg = f.Error.Error()
			}
			failed[f.StepID] = msg
		}
	}
	type keyState struct {
		healed, fail bool
		idx          int
	}
	keys := map[string]*keyState{}
	for _, o := range obs {
		k := keys[o.Key]
		if k == nil {
			k = &keyState{idx: -1}
			keys[o.Key] = k
		}
		if o.OK {
			k.idx, k.fail = o.Index, false
			k.healed = k.healed || o.Healed
		} else if k.idx < 0 {
			k.fail = true
		}
	}
	rep := &VerifyReport{StoppedAt: out.SafeStop, Steps: []StepReport{}}
	stopped := false
	for _, s := range def.Steps {
		r := StepReport{ID: s.ID, Type: s.Type}
		k := keys[s.ConfigKey]
		if s.ConfigKey != "" && k != nil && k.idx >= 0 && pkg != nil {
			if e, ok := pkg.Selector(s.ConfigKey); ok && k.idx < len(e.Candidates) {
				r.Selector = describeCandidate(e.Candidates[k.idx])
			}
		}
		switch {
		case stopped:
			r.Status = StatusSkipped
		case out.SafeStop != nil && s.ID == out.SafeStop.StepID:
			r.Status, stopped = StatusStopped, true
			r.Message = fmt.Sprintf("would %s %s", s.Type, firstNonEmpty(s.Intent, s.Description, s.ConfigKey))
		case failed[s.ID] != "":
			r.Status, r.Message = StatusFail, failed[s.ID]
		case done[s.ID] && k != nil && k.healed:
			r.Status, r.Message = StatusHealed, "primary selector failed; a fallback found the element"
		case done[s.ID]:
			r.Status = StatusPass
		case started[s.ID]:
			r.Status, r.Message = StatusFail, "did not complete"
			if out.Err != nil {
				r.Message = out.Err.Error()
			}
		default:
			r.Status = StatusSkipped
		}
		rep.Steps = append(rep.Steps, r)
	}
	rep.OK = out.Err == nil
	for _, r := range rep.Steps {
		if r.Status == StatusFail {
			rep.OK = false
		}
	}
	if out.Err != nil {
		rep.Error = out.Err.Error()
	}
	return rep
}

func describeCandidate(c action.SelectorCandidate) string {
	switch {
	case c.CSS != "":
		return c.CSS
	case c.XPath != "":
		return c.XPath
	case c.Aria != nil:
		return fmt.Sprintf("aria:%s[name=%q]", c.Aria.Role, c.Aria.Name)
	}
	return fmt.Sprintf("text:%q", c.Text)
}

// PromoteHealed moves each healed candidate to the front of its entry in
// <dir>/selectors.json and stamps verifiedAt on every key that resolved.
// Returns the promoted keys.
func PromoteHealed(dir string, obs []Observation) ([]string, error) {
	path := filepath.Join(dir, "selectors.json")
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	sels := map[string]action.SelectorEntry{}
	if err := json.Unmarshal(b, &sels); err != nil {
		return nil, fmt.Errorf("selectors.json: %w", err)
	}
	now := time.Now().UTC().Format(time.RFC3339)
	changed := false
	var promoted []string
	done := map[string]bool{}
	for _, o := range obs {
		e, ok := sels[o.Key]
		if !ok || !o.OK || done[o.Key] {
			continue
		}
		done[o.Key] = true
		if o.Healed && o.Index > 0 && o.Index < len(e.Candidates) {
			c := e.Candidates[o.Index]
			rest := append(append([]action.SelectorCandidate{}, e.Candidates[:o.Index]...), e.Candidates[o.Index+1:]...)
			e.Candidates = append([]action.SelectorCandidate{c}, rest...)
			promoted = append(promoted, o.Key)
		}
		e.VerifiedAt = now
		sels[o.Key] = e
		changed = true
	}
	if !changed {
		return nil, nil
	}
	out, err := json.MarshalIndent(sels, "", "  ")
	if err != nil {
		return nil, err
	}
	return promoted, os.WriteFile(path, append(out, '\n'), 0o644)
}

// PageExec replays through the normal ActionExecutor on page, running the
// uninstalled draft definition with ExecuteDef (which validates first).
func PageExec(page browser.PageInterface, logger zerolog.Logger) ExecFunc {
	return PageExecWithSecrets(page, logger, nil)
}

// PageExecWithSecrets is PageExec with a vault lookup for {{secret:x}}
// templates whose value is not an input (inputs are resolved first).
func PageExecWithSecrets(page browser.PageInterface, logger zerolog.Logger, lookup func(string) (string, bool)) ExecFunc {
	return func(ctx context.Context, def *action.ActionDef, pkg action.PackageContext, inputs map[string]any, safe bool, obs action.SelectorObserver) RunOutcome {
		events := make(chan action.ExecutionEvent, 8192)
		ae := action.NewActionExecutor(ctx, page, nil, nil, events, nil, logger)
		ae.SetPackage(pkg)
		ae.SetSafeMode(safe)
		ae.SetSelectorObserver(obs)
		if lookup != nil {
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
		return out
	}
}

// redact masks every supplied input value (they may be secrets) in the
// report's messages, so no value is ever echoed back.
func redact(rep *VerifyReport, inputs map[string]any) {
	var vals []string
	for _, v := range inputs {
		if s := fmt.Sprint(v); len(s) >= 3 {
			vals = append(vals, s)
		}
	}
	if len(vals) == 0 {
		return
	}
	mask := func(s string) string {
		for _, v := range vals {
			s = strings.ReplaceAll(s, v, "***")
		}
		return s
	}
	for i := range rep.Steps {
		rep.Steps[i].Message = mask(rep.Steps[i].Message)
	}
	rep.Error = mask(rep.Error)
}
