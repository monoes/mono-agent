package action

// Jev element-picker fallback (plan docs/plans/2026-09-25-jev-integration.md,
// WS2). When a step that declares an "intent" fails to find its element by
// selector/xpath, and the profile opted in (`jev enable action_fallback`,
// wired in internal/nodes/browser_adapter.go via SetJevPicker), Jev picks the
// matching element from a snapshot of the page. The pick is bridged to the
// page driver through a DOM marker attribute: jevpick marks the node in the
// page's main world, and the driver selects it by
// [data-monoagent-jev='<marker>'] (extension element handles live in
// content.js's isolated world, so a node reference cannot be shared).
//
// Marker lifetime: the marker stays on the element until the step that
// picked it finishes — every element-resolving step handler defers
// releaseJevMarker, which unmarks it. The marker is only needed to obtain
// the handle; the handle itself does not depend on it afterwards.
//
// Failure semantics: every failure (no page CDP, NONE, below threshold,
// stale page, network, timeout, marked element not found) returns the
// step's ORIGINAL error, wrapped as "<cause> (jev fallback: <why>)", so
// errors.Is(err, cause) holds and onError behaves exactly as without Jev.
// One pick per step attempt; nothing is persisted (the hint cache below is
// in-process only).

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/monoes/mono-agent/internal/browser"
	"github.com/monoes/mono-agent/internal/jevpick"
)

const (
	jevPickTimeout    = 10 * time.Second
	jevElementTimeout = 5 * time.Second
)

// jevHints remembers, per platform/action/stepID, the label of the element
// Jev last chose, and offers it as a hint on the next pick. In-process only.
var jevHints = struct {
	sync.Mutex
	m map[string]string
}{m: map[string]string{}}

func (ae *ActionExecutor) jevHintKey(step StepDef) string {
	platform, actionType := "", ""
	if ae.action != nil {
		platform, actionType = ae.action.TargetPlatform, ae.action.Type
	}
	return platform + "/" + actionType + "/" + step.ID
}

// jevKind maps a step type to the jevpick element kind.
func jevKind(stepType string) string {
	switch stepType {
	case "click":
		return "click"
	case "type", "input":
		return "fill"
	}
	return "any"
}

// jevResolve is the fallback for a step whose selector/xpath lookup failed
// with cause. It returns (nil, cause) unchanged when the fallback does not
// apply (picker not set, no intent, page without CDP), the picked element on
// success, and cause wrapped with the fallback's reason on any failure.
func (ae *ActionExecutor) jevResolve(step StepDef, cause error) (browser.ElementHandle, error) {
	intent := strings.TrimSpace(step.Intent)
	if ae.jevClient == nil || intent == "" || ae.page == nil {
		return nil, cause
	}
	page, ok := ae.page.(jevpick.Page)
	if !ok {
		return nil, cause
	}
	// A previous pick of this step attempt (should not happen: one pick per
	// attempt) must not leave its marker behind.
	ae.releaseJevMarker()

	log := ae.logger.With().Str("stepID", step.ID).Str("intent", intent).Logger()
	fail := func(err error) (browser.ElementHandle, error) {
		log.Info().Err(err).Msg("jev fallback: no element")
		return nil, fmt.Errorf("%w (jev fallback: %v)", cause, err)
	}

	parent := ae.ctx
	if parent == nil {
		parent = context.Background()
	}
	ctx, cancel := context.WithTimeout(parent, jevPickTimeout)
	defer cancel()

	key := ae.jevHintKey(step)
	target := jevpick.Target{Intent: intent, Kind: jevKind(step.Type)}
	jevHints.Lock()
	if last := jevHints.m[key]; last != "" {
		target.Hint = fmt.Sprintf("Last time this step matched the element labelled %q.", last)
	}
	jevHints.Unlock()

	picked, err := jevpick.Pick(ctx, ae.jevClient, page, target, ae.jevMinP)
	if err != nil {
		return fail(err)
	}
	ae.jevMarker = picked.Marker
	el, err := ae.page.Element(fmt.Sprintf("[%s='%s']", jevpick.MarkerAttr, picked.Marker), jevElementTimeout)
	if err == nil && el == nil {
		err = errors.New("marked element not found")
	}
	if err != nil {
		ae.releaseJevMarker()
		return fail(err)
	}

	jevHints.Lock()
	jevHints.m[key] = picked.Label
	jevHints.Unlock()
	log.Info().Str("label", picked.Label).Float64("p", picked.Probability).
		Float64("confidence", picked.Confidence).Msg("jev fallback picked element")
	return el, nil
}

// releaseJevMarker removes the marker of the current step's pick, if any.
// Errors are ignored: the page may have navigated away, taking it along.
func (ae *ActionExecutor) releaseJevMarker() {
	if ae.jevMarker == "" {
		return
	}
	marker := ae.jevMarker
	ae.jevMarker = ""
	if page, ok := ae.page.(jevpick.Page); ok {
		_ = jevpick.Unmark(page, marker)
	}
}
