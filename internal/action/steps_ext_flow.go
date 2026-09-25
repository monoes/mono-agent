package action

// Composition and control-flow steps: call_fragment, call_action, for_each,
// wait_for (and the waitUntil helper behind a step's "until"), assert.

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"
)

// maxNestingDepth bounds call_fragment / call_action nesting, so a fragment
// or action that (indirectly) calls itself fails instead of overflowing.
const maxNestingDepth = 8

// extDepthKey holds the current nesting depth in ExecutionContext.Data. The
// NUL byte keeps it out of reach of any {{template}}.
const extDepthKey = "\x00ext_nesting_depth"

// enterNested increments the nesting depth, failing past maxNestingDepth.
// The returned func restores it.
func (ae *ActionExecutor) enterNested() (func(), error) {
	v, _ := ae.execCtx.GetData(extDepthKey)
	depth, _ := v.(int)
	if depth >= maxNestingDepth {
		return nil, fmt.Errorf("nesting deeper than %d (recursive fragment or action?)", maxNestingDepth)
	}
	ae.execCtx.SetData(extDepthKey, depth+1)
	return func() { ae.execCtx.SetData(extDepthKey, depth) }, nil
}

// ---------------------------------------------------------------------------
// call_fragment
// ---------------------------------------------------------------------------

func (ae *ActionExecutor) stepCallFragment(ctx context.Context, step StepDef) (*StepResult, error) {
	if ae.pkg == nil {
		return extFail(step, "call_fragment needs an automation package")
	}
	name := ae.resolver.Resolve(step.Fragment)
	if name == "" {
		return extFail(step, "no fragment named")
	}
	frag, err := ae.pkg.Fragment(name)
	if err != nil {
		return extFail(step, "fragment %q: %w", name, err)
	}
	if frag == nil {
		return extFail(step, "fragment %q not found", name)
	}
	leave, err := ae.enterNested()
	if err != nil {
		return extFail(step, "fragment %q: %w", name, err)
	}
	defer leave()

	restore := ae.bindVars(ae.extResolveInputs(step.Inputs))
	defer restore()
	if err := ae.validateRequiredInputs(&ActionDef{Inputs: frag.Inputs}); err != nil {
		return extFail(step, "fragment %q: %w", name, err)
	}
	return ae.bodyResult(step, ae.executeSteps(ctx, frag.Steps), nil)
}

// ---------------------------------------------------------------------------
// call_action
// ---------------------------------------------------------------------------

// nestedStorage is the storage a called action runs against: it keeps the
// daily counters and extracted-data saves, but never overwrites the calling
// action's state or resume index.
type nestedStorage struct{ StorageInterface }

func (nestedStorage) UpdateActionState(string, string) error     { return nil }
func (nestedStorage) UpdateActionReachedIndex(string, int) error { return nil }

func (ae *ActionExecutor) stepCallAction(ctx context.Context, step StepDef) (*StepResult, error) {
	if ae.pkg == nil {
		return extFail(step, "call_action needs an automation package")
	}
	// The reference must be literal; cross-package calls must be declared
	// in permissions.callActions, and untrusted callers may not reach
	// built-in or social packages (contract §8).
	ref := strings.TrimSpace(step.Action)
	if ref == "" {
		return extFail(step, "no action named")
	}
	def, pctx, err := CheckCallAction(ae.pkg, ref)
	if err != nil {
		return extFail(step, "%w", err)
	}
	if ae.safeMode && atLeastWrite(def.SideEffects) {
		return ae.extSafeStop(step)
	}
	if !ae.safeMode && atLeastWrite(def.SideEffects) && PackageTrust(pctx) == "imported" {
		if g, ok := pctx.(LiveRunGate); !ok || !g.LiveRunConfirmed() {
			return extFail(step, "action %q of imported automation %q writes to the site and has not been confirmed for live runs (monoagentcli automation trust %s --live)", ref, pctx.ID(), pctx.ID())
		}
	}
	if issues := Validate(def, pctx); HasErrors(issues) {
		return extFail(step, "action %q does not validate: %s", ref, issueSummary(issues))
	}
	leave, err := ae.enterNested()
	if err != nil {
		return extFail(step, "action %q: %w", ref, err)
	}
	defer leave()

	restoreVars := ae.bindVars(ae.extResolveInputs(step.Inputs))
	defer restoreVars()
	if err := ae.validateRequiredInputs(def); err != nil {
		return extFail(step, "action %q: %w", ref, err)
	}

	// Switch the executor to the called action for the duration: its
	// package (domains, selectors, fragments), its steps (condition
	// branches look them up in actionDef) and fresh loop progress.
	prevPkg, prevDef, prevAction, prevDB, prevReached := ae.pkg, ae.actionDef, ae.action, ae.db, ae.reachedIndexByLoop
	defer func() {
		ae.pkg, ae.actionDef, ae.action, ae.db, ae.reachedIndexByLoop = prevPkg, prevDef, prevAction, prevDB, prevReached
	}()
	ae.pkg, ae.actionDef, ae.reachedIndexByLoop = pctx, def, make(map[string]int)
	if prevDB != nil {
		ae.db = nestedStorage{prevDB}
	}
	nested := StorageAction{Type: def.ActionType, TargetPlatform: pctx.ID()}
	if prevAction != nil {
		nested = *prevAction
		nested.Type, nested.TargetPlatform, nested.ReachedIndex = def.ActionType, pctx.ID(), 0
	}
	ae.action = &nested

	return ae.bodyResult(step, ae.runActionBody(ctx, def), nil)
}

// runActionBody runs a called action's steps the way Execute does: the
// steps that belong to no loop and no condition branch, then each loop.
func (ae *ActionExecutor) runActionBody(ctx context.Context, def *ActionDef) error {
	skip := make(map[string]bool)
	for _, loop := range def.Loops {
		for _, id := range ae.getAllReferencedIDs(loop, def.Steps) {
			skip[id] = true
		}
	}
	for _, s := range def.Steps {
		for _, id := range s.Then {
			skip[id] = true
		}
		for _, id := range s.Else {
			skip[id] = true
		}
	}
	var initial []StepDef
	for _, s := range def.Steps {
		if !skip[s.ID] {
			initial = append(initial, s)
		}
	}
	if err := ae.executeSteps(ctx, initial); err != nil {
		return err
	}
	for _, loop := range def.Loops {
		if ae.safeStop != nil {
			return nil
		}
		if err := ae.executeLoop(ctx, loop, def.Steps); err != nil {
			if isHalt(err) || isContextError(err) {
				return err
			}
			ae.logger.Warn().Err(err).Str("loopID", loop.ID).Msg("called action loop completed with errors")
		}
	}
	return nil
}

func issueSummary(issues []Issue) string {
	var parts []string
	for _, i := range issues {
		if i.Severity == "error" {
			parts = append(parts, i.Code+": "+i.Message)
		}
	}
	return strings.Join(parts, "; ")
}

// ---------------------------------------------------------------------------
// for_each
// ---------------------------------------------------------------------------

// maxForEachItems caps one for_each run when the step sets no batchSize.
const maxForEachItems = 10000

func (ae *ActionExecutor) stepForEach(ctx context.Context, step StepDef) (*StepResult, error) {
	raw := ae.extResolve(step.Items)
	if raw == nil {
		ae.logger.Info().Str("stepID", step.ID).Str("items", step.Items).Msg("for_each items resolved to nothing")
		return &StepResult{Success: true, StepID: step.ID, Data: 0}, nil
	}
	items, ok := toList(raw)
	if !ok {
		return extFail(step, "items %q resolved to %T (%v), which cannot be iterated — expected an array", step.Items, raw, truncateForError(raw))
	}
	limit := maxForEachItems
	if step.BatchSize > 0 {
		limit = step.BatchSize
	}
	if len(items) > limit {
		ae.logger.Warn().Str("stepID", step.ID).Int("items", len(items)).Int("cap", limit).Msg("for_each items capped")
		items = items[:limit]
	}
	as := step.As
	if as == "" {
		as = "item"
	}

	ae.execCtx.enterLoop()
	defer ae.execCtx.leaveLoop()
	restore := ae.bindVars(map[string]interface{}{as: nil, as + "_index": 0, "loopIndex": 0})
	defer restore()

	done := 0
	for i, it := range items {
		if err := ctx.Err(); err != nil {
			return extFail(step, "%w", err)
		}
		ae.execCtx.SetVariable(as, it)
		ae.execCtx.SetVariable(as+"_index", i)
		ae.execCtx.SetVariable("loopIndex", i)
		if err := ae.executeSteps(ctx, step.Steps); err != nil || ae.safeStop != nil {
			return ae.bodyResult(step, err, done)
		}
		done++
	}
	return &StepResult{Success: true, StepID: step.ID, Data: done}, nil
}

// ---------------------------------------------------------------------------
// assert
// ---------------------------------------------------------------------------

func (ae *ActionExecutor) stepAssert(_ context.Context, step StepDef) (*StepResult, error) {
	if step.Condition == nil {
		return extFail(step, "no condition")
	}
	if ae.evaluateCondition(step) {
		return &StepResult{Success: true, StepID: step.ID, Data: true}, nil
	}
	msg := step.Description
	if msg == "" {
		msg = fmt.Sprintf("condition %v is false", step.Condition)
	}
	return extFail(step, "assertion failed: %s", msg)
}

// ---------------------------------------------------------------------------
// wait_for and waitUntil
// ---------------------------------------------------------------------------

const waitPollInterval = 250 * time.Millisecond

// networkIdleQuiet is how long the resource count must stay unchanged, with
// the document complete, for networkIdle to hold.
const networkIdleQuiet = 500 * time.Millisecond

func (ae *ActionExecutor) stepWaitFor(ctx context.Context, step StepDef) (*StepResult, error) {
	if step.Until == nil {
		return extFail(step, "no condition (until)")
	}
	if err := ae.waitUntil(ctx, step.Until, stepTimeout(step, 30)); err != nil {
		return extFail(step, "%w", err)
	}
	return &StepResult{Success: true, StepID: step.ID}, nil
}

// resolveWaitSpec interpolates templates in every leaf of spec.
func (ae *ActionExecutor) resolveWaitSpec(spec WaitSpec) WaitSpec {
	r := ae.resolver
	out := WaitSpec{
		URLMatches:  r.Resolve(spec.URLMatches),
		Selector:    r.Resolve(spec.Selector),
		Text:        r.Resolve(spec.Text),
		NetworkIdle: spec.NetworkIdle,
		Gone:        r.Resolve(spec.Gone),
	}
	for _, s := range spec.Any {
		out.Any = append(out.Any, ae.resolveWaitSpec(s))
	}
	return out
}

// waitUntil polls the page until spec holds or timeout passes. It is the
// wait_for step, and the post-step outcome check for a click/type step
// with "until" set (called by the core step handlers). Templates in the
// spec are resolved here, once, before polling.
func (ae *ActionExecutor) waitUntil(ctx context.Context, until *WaitSpec, timeout time.Duration) error {
	if until == nil {
		return nil
	}
	resolved := ae.resolveWaitSpec(*until)
	spec := &resolved
	if !waitSpecHasLeaf(spec) {
		return fmt.Errorf("empty wait condition")
	}
	var compiled []*regexp.Regexp
	if err := compileWaitRegexps(spec, &compiled); err != nil {
		return err
	}
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	idle := &idleTracker{}
	deadline := time.Now().Add(timeout)
	var lastErr error
	for {
		ok, err := ae.checkWaitSpec(ctx, spec, idle)
		if ok {
			return nil
		}
		if err != nil {
			lastErr = err
		}
		if time.Now().After(deadline) {
			if lastErr != nil {
				return fmt.Errorf("timed out after %s waiting for %s: %w", timeout, describeWaitSpec(spec), lastErr)
			}
			return fmt.Errorf("timed out after %s waiting for %s", timeout, describeWaitSpec(spec))
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(waitPollInterval):
		}
	}
}

func waitSpecHasLeaf(s *WaitSpec) bool {
	if s.URLMatches != "" || s.Selector != "" || s.Text != "" || s.NetworkIdle || s.Gone != "" {
		return true
	}
	for i := range s.Any {
		if waitSpecHasLeaf(&s.Any[i]) {
			return true
		}
	}
	return false
}

// compileWaitRegexps validates every urlMatches up front, so a bad pattern
// fails at once instead of after the timeout.
func compileWaitRegexps(s *WaitSpec, out *[]*regexp.Regexp) error {
	if s.URLMatches != "" {
		re, err := regexp.Compile(s.URLMatches)
		if err != nil {
			return fmt.Errorf("urlMatches %q: %w", s.URLMatches, err)
		}
		*out = append(*out, re)
	}
	for i := range s.Any {
		if err := compileWaitRegexps(&s.Any[i], out); err != nil {
			return err
		}
	}
	return nil
}

func describeWaitSpec(s *WaitSpec) string {
	var parts []string
	if s.URLMatches != "" {
		parts = append(parts, "url ~ "+s.URLMatches)
	}
	if s.Selector != "" {
		parts = append(parts, "selector "+s.Selector)
	}
	if s.Gone != "" {
		parts = append(parts, "gone "+s.Gone)
	}
	if s.Text != "" {
		parts = append(parts, fmt.Sprintf("text %q", s.Text))
	}
	if s.NetworkIdle {
		parts = append(parts, "network idle")
	}
	for i := range s.Any {
		parts = append(parts, describeWaitSpec(&s.Any[i]))
	}
	sep := " and "
	if len(s.Any) > 0 {
		sep = " or "
	}
	return "(" + strings.Join(parts, sep) + ")"
}

// idleTracker remembers the page's resource count between polls.
type idleTracker struct {
	count   int
	since   time.Time
	started bool
}

// checkWaitSpec evaluates spec once. Leaf fields set together must all
// hold; Any holds when one of its entries does.
func (ae *ActionExecutor) checkWaitSpec(ctx context.Context, s *WaitSpec, idle *idleTracker) (bool, error) {
	if s.URLMatches != "" {
		cur, err := ae.page.GetURL()
		if err != nil {
			return false, err
		}
		if !regexp.MustCompile(s.URLMatches).MatchString(cur) {
			return false, nil
		}
	}
	if s.Selector != "" {
		has, err := ae.page.Has(s.Selector)
		if err != nil || !has {
			return false, err
		}
	}
	if s.Gone != "" {
		has, err := ae.page.Has(s.Gone)
		if err != nil || has {
			return false, err
		}
	}
	if s.Text != "" {
		v, err := ae.evalJS(ctx, fmt.Sprintf(`!!(document.body && document.body.innerText.includes(%s))`, jsString(s.Text)), 5*time.Second)
		if err != nil || v != true {
			return false, err
		}
	}
	if s.NetworkIdle {
		ok, err := ae.checkNetworkIdle(ctx, idle)
		if err != nil || !ok {
			return false, err
		}
	}
	if len(s.Any) > 0 {
		var firstErr error
		for i := range s.Any {
			ok, err := ae.checkWaitSpec(ctx, &s.Any[i], idle)
			if ok {
				return true, nil
			}
			if err != nil && firstErr == nil {
				firstErr = err
			}
		}
		return false, firstErr
	}
	return true, nil
}

// checkNetworkIdle approximates network idle: the document is complete and
// the number of loaded resources has not changed for networkIdleQuiet.
func (ae *ActionExecutor) checkNetworkIdle(ctx context.Context, t *idleTracker) (bool, error) {
	v, err := ae.evalJS(ctx, `({ready: document.readyState, n: performance.getEntriesByType('resource').length})`, 5*time.Second)
	if err != nil {
		return false, err
	}
	m, _ := v.(map[string]interface{})
	n := toInt(m["n"])
	now := time.Now()
	if !t.started || n != t.count {
		t.started, t.count, t.since = true, n, now
		return false, nil
	}
	return m["ready"] == "complete" && now.Sub(t.since) >= networkIdleQuiet, nil
}
