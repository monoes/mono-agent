package openaiapi

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/monoes/mono-agent/internal/agentroster"
)

// autoPromptRunes is how much of the last user message Jev is sent: the first
// 4,000 characters, which the api_auto surface's egress list says.
const autoPromptRunes = 4000

// AutoStatus says whether the auto model works for a profile, and what is
// missing when it does not.
type AutoStatus struct {
	Available bool
	// Missing names what to set up when it is not available.
	Missing string
}

// AutoFuncs are what the auto model needs from outside the gateway: the zero
// value means auto is never available. Production wires them to Jev
// (DefaultAuto); tests replace them.
type AutoFuncs struct {
	// Status reports whether profileID can use auto. It decrypts nothing: a
	// listing must never trigger a keyring prompt.
	Status func(ctx context.Context, profileID string) AutoStatus
	// Choose asks Jev which of the options (id to description) suits the prompt
	// best, and how sure it is: the top option's probability. It runs only after
	// Status said yes.
	Choose func(ctx context.Context, profileID, prompt string, options map[string]string) (id string, p float64, err error)
	// Threshold is the lowest probability of Jev's pick the profile accepts.
	Threshold func(profileID string) float64
}

// autoStatus is what the profile may do with auto.
func (g *Gateway) autoStatus(ctx context.Context, profileID string) AutoStatus {
	if g.deps.Auto.Status == nil || g.deps.Auto.Choose == nil {
		return AutoStatus{Missing: "automatic model choice, which this server was not set up with"}
	}
	return g.deps.Auto.Status(ctx, profileID)
}

// autoCandidates returns the models auto may pick among for a key whose
// effective policy is eff, or the 404 that says why auto is not available: Jev
// has no key or the surface is off for the profile, or the policy allows no model.
func (g *Gateway) autoCandidates(ctx context.Context, pr Principal, eff Policy) ([]ModelInfo, *apiError) {
	if st := g.autoStatus(ctx, pr.ProfileID); !st.Available {
		return nil, errAutoUnavailable(st.Missing)
	}
	models, err := g.catalog.Visible(ctx, eff)
	if err != nil {
		return nil, catalogError(err)
	}
	if len(models) == 0 {
		return nil, errAutoUnavailable("at least one model the server's confinement policy allows")
	}
	return models, nil
}

// autoObject is auto as a model of the list. Its confinement is the strongest
// class the key's policy allows: what Jev picks is never above it.
func autoObject(eff Policy) modelObject {
	return modelObject{
		ID: autoModelID, Object: "model", OwnedBy: "jev",
		Monoagent: modelMeta{
			Runtime: autoModelID, Model: autoModelID, Label: "Jev picks the model for each request",
			Confinement: eff.Max.String(), Capabilities: []string{"text"},
		},
	}
}

// autoPick is the model auto chose, and by what.
type autoPick struct {
	Model ModelInfo
	// By is "jev" when Jev's answer was used, "rule" when the rule decided.
	By string
}

// pickAuto chooses among candidates (at least one, all of them allowed by the
// key's policy: Jev only picks among what the code lists). It asks Jev unless
// there is nothing to choose between, and uses the rule when Jev does not
// answer, in time, with one of the candidates, sure enough. It never fails: the
// request has a model whatever happens at TypeSafe.
func (g *Gateway) pickAuto(ctx context.Context, profileID, prompt string, candidates []ModelInfo) autoPick {
	if len(candidates) == 1 {
		return autoPick{Model: candidates[0], By: "rule"}
	}
	options := make(map[string]string, len(candidates))
	for _, m := range candidates {
		options[m.ID] = autoDescription(m)
	}
	cctx, cancel := context.WithTimeout(ctx, g.cfg.AutoTimeout)
	defer cancel()
	id, p, err := g.ask(cctx, profileID, clipRunes(prompt, autoPromptRunes), options)
	reason := ""
	switch {
	case err != nil:
		reason = "no answer"
	case g.deps.Auto.Threshold != nil && p < g.deps.Auto.Threshold(profileID):
		reason = "not sure enough"
	default:
		if i := slices.IndexFunc(candidates, func(m ModelInfo) bool { return m.ID == id }); i >= 0 {
			return autoPick{Model: candidates[i], By: "jev"}
		}
		reason = "an answer that is not one of the options"
	}
	g.deps.Logf("auto: Jev did not decide for profile %s (%s): the rule picks", profileID, reason)
	return autoPick{Model: ruleChoice(candidates), By: "rule"}
}

// ask puts the question to Jev and gives up when ctx ends, even when Choose does
// not look at ctx: resolving the profile's Jev key can wait on a keyring, and the
// request holds a slot meanwhile. An abandoned question finishes in its own
// goroutine, which is why a panic in it is turned into an error here: nothing
// else would recover it, and it would take the server down.
func (g *Gateway) ask(ctx context.Context, profileID, prompt string, options map[string]string) (string, float64, error) {
	type answer struct {
		id  string
		p   float64
		err error
	}
	done := make(chan answer, 1)
	go func() {
		defer func() {
			if recover() != nil { // what it says is dropped: it could quote the prompt
				done <- answer{err: errors.New("the question panicked")}
			}
		}()
		id, p, err := g.deps.Auto.Choose(ctx, profileID, prompt, options)
		done <- answer{id, p, err}
	}()
	select {
	case a := <-done:
		return a.id, a.p, a.err
	case <-ctx.Done():
		return "", 0, ctx.Err()
	}
}

// ruleChoice is the model auto uses when Jev does not decide: of the models with
// a recent passing validation, the most confined, then the cheapest, then the
// fastest (a cost or a latency nobody measured sorts last); with none, the default
// model of a runtime; with none of those, the first candidate. The class comes
// first everywhere, so a TypeSafe outage never moves a request to a less confined
// model for being cheaper; within a class, runtimes are taken in the order claude,
// codex, antigravity, then the rest alphabetically. candidates must not be empty.
func ruleChoice(candidates []ModelInfo) ModelInfo {
	byClass := func(a, b ModelInfo) int { return cmp.Compare(a.Class, b.Class) }
	byRuntime := func(a, b ModelInfo) int {
		if c := cmp.Compare(runtimeRank(a.Runtime), runtimeRank(b.Runtime)); c != 0 {
			return c
		}
		return cmp.Or(cmp.Compare(a.Runtime, b.Runtime), cmp.Compare(a.ID, b.ID))
	}
	var validated, defaults []ModelInfo
	for _, m := range candidates {
		if m.Validated {
			validated = append(validated, m)
		}
		if m.Model == agentroster.DefaultModel {
			defaults = append(defaults, m)
		}
	}
	switch {
	case len(validated) > 0:
		slices.SortStableFunc(validated, func(a, b ModelInfo) int {
			return cmp.Or(byClass(a, b),
				compareKnownFirst(a.CostUSD, a.HasCost, b.CostUSD, b.HasCost),
				compareKnownFirst(float64(a.LatencyMs), a.LatencyMs > 0, float64(b.LatencyMs), b.LatencyMs > 0),
				byRuntime(a, b))
		})
		return validated[0]
	case len(defaults) > 0:
		slices.SortFunc(defaults, func(a, b ModelInfo) int { return cmp.Or(byClass(a, b), byRuntime(a, b)) })
		return defaults[0]
	}
	sorted := slices.Clone(candidates)
	slices.SortFunc(sorted, func(a, b ModelInfo) int { return cmp.Or(byClass(a, b), byRuntime(a, b)) })
	return sorted[0]
}

// compareKnownFirst orders by value, with a value nobody measured after every one
// somebody did.
func compareKnownFirst(a float64, aKnown bool, b float64, bKnown bool) int {
	switch {
	case aKnown && bKnown:
		return cmp.Compare(a, b)
	case aKnown:
		return -1
	case bKnown:
		return 1
	}
	return 0
}

// runtimeRank is where a runtime stands in the rule's order: claude, codex and
// antigravity first, in that order, every other runtime after them.
func runtimeRank(runtime string) int {
	switch runtime {
	case "claude":
		return 0
	case "codex":
		return 1
	case "antigravity":
		return 2
	}
	return 3
}

// autoDescription is what Jev is told about a model: its label, who runs it, and
// what the latest validation measured.
func autoDescription(m ModelInfo) string {
	label := m.Label
	if label == "" {
		label = m.ID
	}
	parts := []string{fmt.Sprintf("%s (%s)", label, m.Runtime)}
	if m.Validated {
		if m.HasCost && m.CostUSD > 0 {
			parts = append(parts, fmt.Sprintf("test turn cost $%.4f", m.CostUSD))
		}
		if m.LatencyMs > 0 {
			parts = append(parts, fmt.Sprintf("answers in %.1fs", float64(m.LatencyMs)/1000))
		}
	}
	return strings.Join(parts, ", ")
}
