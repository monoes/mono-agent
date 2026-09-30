package agentroster

import (
	"math"
	"testing"
)

func TestTableTestCost(t *testing.T) {
	per := func(in, out float64) float64 { return (testTurnTokensIn*in + testTurnTokensOut*out) / 1e6 }
	cases := []struct {
		runtime, model string
		want           float64
		ok             bool
	}{
		{"claude", "haiku", per(1, 5), true},
		{"claude", "claude-haiku-4-5-20251001", per(1, 5), true},
		{"claude", "opus[1m]", per(5, 25), true},
		{"claude", "claude-opus-4-1", per(15, 75), true},
		{"claude", DefaultModel, per(5, 25), true},        // the runtime's paid default
		{"claude", "some-new-model", per(5, 25), true},    // unknown model: the runtime's default
		{"codex", "gpt-5.5", per(2.5, 10), true},          // longest prefix
		{"codex", "gpt-4o-mini", per(0.15, 0.6), true},    // exact beats the gpt-4o prefix
		{"opencode", "openai/gpt-4o", per(2.5, 10), true}, // provider prefix dropped
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
	p.EstimateCost([]Result{{Runtime: "claude", Model: "opus", HasCost: true, CostUSD: 0.02}})
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
