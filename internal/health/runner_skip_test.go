package health

import (
	"context"
	"testing"
)

// The GUI's background check skips the runtimes group (#146): neither the
// group's checks nor anything depending on them may run.
func TestRunSkipGroups(t *testing.T) {
	ran := map[string]bool{}
	mark := func(id string) func(context.Context, *Env) Result {
		return func(context.Context, *Env) Result { ran[id] = true; return Result{Status: StatusOK} }
	}
	reg := NewRegistry([]Check{
		{ID: "base", Group: "core", Run: mark("base")},
		{ID: "scan", Group: "runtimes", DependsOn: []string{"base"}, Run: mark("scan")},
		{ID: "uses", Group: "x", DependsOn: []string{"scan"}, Run: mark("uses")},
		{ID: "other", Group: "x", DependsOn: []string{"base"}, Run: mark("other")},
	}, nil)
	got := byID(reg.Run(context.Background(), &Env{}, Options{SkipGroups: []string{"runtimes"}}))
	for _, id := range []string{"scan", "uses"} {
		if _, ok := got[id]; ok || ran[id] {
			t.Errorf("%s ran with its group (or its dependency's) skipped", id)
		}
	}
	for _, id := range []string{"base", "other"} {
		if got[id].Status != StatusOK {
			t.Errorf("%s: %q, want ok", id, got[id].Status)
		}
	}
}
