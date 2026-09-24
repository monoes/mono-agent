package health

import (
	"context"
	"testing"
	"time"
)

func fixed(st Status) func(context.Context, *Env) Result {
	return func(context.Context, *Env) Result { return Result{Status: st, Summary: string(st)} }
}

func byID(rep *Report) map[string]Result {
	m := map[string]Result{}
	for _, r := range rep.Results {
		m[r.ID] = r
	}
	return m
}

func TestRunSkipsDependentsOfFailedChecks(t *testing.T) {
	reg := NewRegistry([]Check{
		{ID: "a", Group: "g", Required: true, Run: fixed(StatusFail)},
		{ID: "b", Group: "g", DependsOn: []string{"a"}, Run: fixed(StatusOK)},
		{ID: "c", Group: "g", DependsOn: []string{"b"}, Run: fixed(StatusOK)},
		{ID: "d", Group: "g", Run: fixed(StatusWarn)},
		{ID: "e", Group: "g", DependsOn: []string{"d"}, Run: fixed(StatusOK)},
	}, nil)
	rep := reg.Run(context.Background(), &Env{}, Options{})
	got := byID(rep)
	want := map[string]Status{"a": StatusFail, "b": StatusSkip, "c": StatusSkip, "d": StatusWarn, "e": StatusOK}
	for id, st := range want {
		if got[id].Status != st {
			t.Errorf("%s: status %q, want %q", id, got[id].Status, st)
		}
	}
	if rep.RequiredFailures() != 1 {
		t.Errorf("RequiredFailures = %d, want 1", rep.RequiredFailures())
	}
	if rep.Summary[StatusSkip] != 2 || rep.V != SchemaVersion {
		t.Errorf("summary %v / v %d", rep.Summary, rep.V)
	}
	// Registration order is preserved.
	for i, id := range []string{"a", "b", "c", "d", "e"} {
		if rep.Results[i].ID != id {
			t.Fatalf("result %d is %s, want %s", i, rep.Results[i].ID, id)
		}
	}
}

func TestRunSelection(t *testing.T) {
	reg := NewRegistry([]Check{
		{ID: "base", Group: "core", Run: fixed(StatusOK)},
		{ID: "net", Group: "core", Network: true, Run: fixed(StatusOK)},
		{ID: "other", Group: "x", DependsOn: []string{"base"}, Run: fixed(StatusOK)},
	}, nil)

	got := byID(reg.Run(context.Background(), &Env{}, Options{}))
	if _, ok := got["net"]; ok {
		t.Error("network check ran without Deep")
	}
	if got := byID(reg.Run(context.Background(), &Env{}, Options{Deep: true})); got["net"].Status != StatusOK {
		t.Error("network check did not run with Deep")
	}

	// Selecting one check still runs its dependencies.
	got = byID(reg.Run(context.Background(), &Env{}, Options{IDs: []string{"other"}}))
	if len(got) != 2 || got["base"].Status != StatusOK || got["other"].Status != StatusOK {
		t.Errorf("IDs selection: %v", got)
	}
	got = byID(reg.Run(context.Background(), &Env{}, Options{Groups: []string{"x"}}))
	if _, ok := got["other"]; !ok {
		t.Error("group selection missed its check")
	}
}

func TestRunTimeoutAndPanic(t *testing.T) {
	reg := NewRegistry([]Check{
		{ID: "slow", Group: "g", Timeout: 20 * time.Millisecond, Run: func(ctx context.Context, _ *Env) Result {
			<-ctx.Done()
			time.Sleep(50 * time.Millisecond) // ignores cancellation for a while
			return Result{Status: StatusOK}
		}},
		{ID: "boom", Group: "g", Run: func(context.Context, *Env) Result { panic("kaboom") }},
	}, nil)
	got := byID(reg.Run(context.Background(), &Env{}, Options{}))
	if got["slow"].Status != StatusFail {
		t.Errorf("slow: %q, want fail (timeout)", got["slow"].Status)
	}
	if got["boom"].Status != StatusFail || got["boom"].Detail != "kaboom" {
		t.Errorf("boom: %+v", got["boom"])
	}
}

func TestRunResolvesFixOnlyForProblems(t *testing.T) {
	fix := Fix{FixInfo: FixInfo{ID: "f", Label: "Fix it", Safety: SafetyAuto}}
	reg := NewRegistry([]Check{
		{ID: "bad", Group: "g", Run: func(context.Context, *Env) Result { return Result{Status: StatusWarn, FixID: "f"} }},
		{ID: "good", Group: "g", Run: func(context.Context, *Env) Result { return Result{Status: StatusOK, FixID: "f"} }},
	}, []Fix{fix})
	got := byID(reg.Run(context.Background(), &Env{}, Options{}))
	if got["bad"].Fix == nil || got["bad"].Fix.ID != "f" || got["bad"].Fix.Safety != SafetyAuto {
		t.Errorf("bad: fix %+v", got["bad"].Fix)
	}
	if got["good"].Fix != nil {
		t.Error("an ok check must not offer a fix")
	}
	if got["bad"].Source != "monoagent" || got["bad"].Group != "g" {
		t.Errorf("metadata not stamped: %+v", got["bad"])
	}
}

func TestDefaultRegistryIsConsistent(t *testing.T) {
	reg := Default()
	ids := map[string]bool{}
	for _, c := range reg.Checks() {
		ids[c.ID] = true
		if c.Run == nil || c.Group == "" || c.Title == "" {
			t.Errorf("%s: incomplete definition", c.ID)
		}
	}
	for _, c := range reg.Checks() {
		for _, d := range c.DependsOn {
			if !ids[d] {
				t.Errorf("%s depends on unknown check %s", c.ID, d)
			}
		}
	}
	for id, f := range reg.fixes {
		if f.Apply == nil {
			t.Errorf("fix %s has no Apply", id)
		}
		if f.Safety != SafetyAuto && f.Safety != SafetyConfirm && f.Safety != SafetyManual {
			t.Errorf("fix %s: bad safety %q", id, f.Safety)
		}
	}
}
