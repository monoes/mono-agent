package agentroster

import (
	"math"
	"testing"
)

func TestTableTestCost(t *testing.T) {
	per := func(p price) float64 { return (testTurnTokensIn*p.cw + testTurnTokensOut*p.out) / 1e6 }
	cases := []struct {
		runtime, model string
		want           float64
		ok             bool
	}{
		{"claude", "haiku", per(price{1, 5, 1.25}), true},
		{"claude", "claude-haiku-4-5-20251001", per(price{1, 5, 1.25}), true},
		{"claude", "claude-haiku-4-5@20251001", per(price{1, 5, 1.25}), true},
		{"claude", "opus[1m]", per(price{5, 25, 6.25}), true},
		{"claude", "claude-opus-4-1", per(price{15, 75, 18.75}), true},
		{"claude", DefaultModel, per(price{5, 25, 6.25}), true}, // the runtime's paid default
		{"codex", DefaultModel, per(price{2.5, 10, 2.5}), true},
		{"claude", "some-new-model", 0, false},                  // unknown, not the runtime's default
		{"codex", "gpt-5.5", per(price{2.5, 10, 2.5}), true},    // longest prefix
		{"codex", "gpt-5-mini", per(price{2.5, 10, 2.5}), true}, // priced as gpt-5: high, not low
		{"codex", "gpt-4o-mini", per(price{0.15, 0.6, 0.15}), true},
		{"codex", "gpt-5-pro", 0, false}, // a dearer variant is not priced as gpt-5
		{"codex", "gpt-5.5-pro", 0, false},
		{"codex", "gpt-5-max", 0, false},
		{"codex", "o3-pro", 0, false},
		{"codex", "o4-mini", 0, false},
		{"opencode", "openai/gpt-4o", per(price{2.5, 10, 2.5}), true}, // provider prefix dropped
		{"opencode", "zai/glm-4.6", 0, false},
		{"crush", DefaultModel, 0, false},
	}
	for _, c := range cases {
		got, ok := TableTestCost(c.runtime, c.model)
		if ok != c.ok || math.Abs(got-c.want) > 1e-12 {
			t.Errorf("TableTestCost(%s, %s) = %v, %v; want %v, %v", c.runtime, c.model, got, ok, c.want, c.ok)
		}
	}
}

// A stored cost wins over the table; the table fills in the rest.
func TestEstimateCostPrefersStoredCost(t *testing.T) {
	p := Plan{Targets: []Target{{Runtime: "claude", Model: "opus"}, {Runtime: "claude", Model: "haiku"}, {Runtime: "crush", Model: "x"}}}
	p.EstimateCost([]Result{{Runtime: "claude", Model: "opus", Status: StatusOK, HasCost: true, CostUSD: 0.02}})
	haiku, _ := TableTestCost("claude", "haiku")
	if p.Calls != 3 || math.Abs(p.EstCostUSD-(0.02+haiku)) > 1e-12 || p.TableEstimated != 1 || p.UnknownCost != 1 {
		t.Errorf("plan = %+v", p)
	}
}

func TestNoteSignIn(t *testing.T) {
	p := Plan{Targets: []Target{{Runtime: "claude", Model: "a"}, {Runtime: "claude", Model: "b"}, {Runtime: "codex", Model: "c"}, {Runtime: "grok", Model: "d"}}}
	p.NoteSignIn([]Result{
		{Runtime: "claude", Model: "a", Status: StatusAuth},
		{Runtime: "codex", Model: "c", Status: StatusAuth, LoginHint: "codex login"},
		{Runtime: "grok", Model: "d", Status: StatusAuth},
		{Runtime: "grok", Model: "e", Status: StatusOK}, // signed in after all
		{Runtime: "pi", Model: "f", Status: StatusAuth}, // not planned
	}, map[string]string{"claude": "claude /login", "codex": "scan hint"})
	want := []SignInNote{{Runtime: "claude", LoginHint: "claude /login"}, {Runtime: "codex", LoginHint: "codex login"}}
	if len(p.SignIn) != len(want) || p.SignIn[0] != want[0] || p.SignIn[1] != want[1] {
		t.Errorf("sign-in notes = %+v, want %+v", p.SignIn, want)
	}
}

// A rate-limited call that reported $0 doesn't price the model at $0, and
// it shows the runtime is signed in.
func TestRateLimitedResultIsNotAPriceOrASignInFailure(t *testing.T) {
	p := Plan{Targets: []Target{{Runtime: "claude", Model: "haiku"}, {Runtime: "claude", Model: "opus"}}}
	previous := []Result{
		{Runtime: "claude", Model: "haiku", Status: StatusRateLimited, HasCost: true, CostUSD: 0},
		{Runtime: "claude", Model: "opus", Status: StatusAuth, LoginHint: "claude /login"},
	}
	p.EstimateCost(previous)
	if p.TableEstimated != 2 || p.EstCostUSD <= 0 {
		t.Errorf("plan = %+v, want both models priced from the table", p)
	}
	p.NoteSignIn(previous, nil)
	if len(p.SignIn) != 0 {
		t.Errorf("sign-in notes = %+v, want none: a rate-limited call got past sign-in", p.SignIn)
	}
}
