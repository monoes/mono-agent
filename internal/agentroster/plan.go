package agentroster

import (
	"context"
	"slices"
	"sync"
	"time"

	"github.com/monoes/mono-agent/internal/monomind"
)

// Lister lists a runtime's models; monomind.ListModels in production. A nil
// slice with no error means the runtime cannot list its models.
type Lister func(ctx context.Context, runtimeID, binary string) ([]monomind.RuntimeModel, error)

// PlanFilter narrows a plan.
type PlanFilter struct {
	Runtimes  []string // empty = every installed runtime
	Models    []string // empty = every listed model; ids not listed are added as manual
	StaleOnly bool     // skip models that are ready
	Now       time.Time
	MaxAge    time.Duration
}

// BuildPlan lists the models of the installed runtimes (at once, one
// listing per runtime) and turns them into test targets. A runtime that
// cannot list models, or whose listing fails, gets one "default" target.
// Models the user added by hand (manual rows in previous) are always
// included.
func BuildPlan(ctx context.Context, scan *monomind.ScanResult, list Lister, previous []Result, f PlanFilter) Plan {
	var plan Plan
	var runtimes []monomind.ScanEntry
	for _, want := range f.Runtimes {
		if e := scan.Find(want); e == nil || !e.Installed {
			plan.Skipped = append(plan.Skipped, SkippedRuntime{Runtime: want, Reason: "not installed"})
		}
	}
	for _, a := range scan.Installed() {
		if len(f.Runtimes) == 0 || slices.Contains(f.Runtimes, a.ID) {
			runtimes = append(runtimes, a)
		}
	}

	listed := make([][]monomind.RuntimeModel, len(runtimes))
	listErr := make([]error, len(runtimes))
	var wg sync.WaitGroup
	for i, a := range runtimes {
		wg.Add(1)
		go func(i int, a monomind.ScanEntry) {
			defer wg.Done()
			bin := ""
			if a.Binary != nil {
				bin = *a.Binary
			}
			listed[i], listErr[i] = list(ctx, a.ID, bin)
		}(i, a)
	}
	wg.Wait()

	states := map[string]string{}
	for _, rr := range Build(previous, scan, f.Now, f.MaxAge) {
		for _, e := range rr.Models {
			states[rr.Runtime+"\x00"+e.Model] = e.State
		}
	}
	for i, a := range runtimes {
		version := ""
		if a.Version != nil {
			version = *a.Version
		}
		var targets []Target
		seen := map[string]bool{}
		add := func(t Target) {
			if seen[t.Model] {
				return
			}
			seen[t.Model] = true
			t.Runtime, t.RuntimeVersion = a.ID, version
			targets = append(targets, t)
		}
		if listErr[i] != nil {
			plan.Skipped = append(plan.Skipped, SkippedRuntime{Runtime: a.ID, Reason: "listing models failed: " + clip(listErr[i].Error())})
		}
		if len(listed[i]) == 0 {
			add(Target{Model: DefaultModel, Label: "Default model", Source: SourceListed})
		}
		for _, m := range listed[i] {
			// Another name for a model already listed (monomind 2.21's
			// alias_of): testing it would run, and bill, the same model twice.
			if m.AliasOf != "" {
				continue
			}
			add(Target{Model: m.ID, Label: m.Label, EffortLevels: m.EffortLevels, Source: SourceListed})
		}
		for _, r := range previous {
			if r.Runtime == a.ID && r.Source == SourceManual {
				add(Target{Model: r.Model, Label: r.Label, Source: SourceManual})
			}
		}
		if len(f.Models) > 0 {
			var kept []Target
			for _, want := range f.Models {
				idx := slices.IndexFunc(targets, func(t Target) bool { return t.Model == want })
				if idx >= 0 {
					kept = append(kept, targets[idx])
				} else {
					kept = append(kept, Target{Runtime: a.ID, Model: want, Label: want, Source: SourceManual, RuntimeVersion: version})
				}
			}
			targets = kept
		}
		for _, t := range targets {
			if f.StaleOnly && states[a.ID+"\x00"+t.Model] == StateReady {
				continue
			}
			plan.Targets = append(plan.Targets, t)
		}
	}
	plan.EstimateCost(previous)
	hints := map[string]string{}
	for _, a := range runtimes {
		if a.LoginHint != nil {
			hints[a.ID] = *a.LoginHint
		}
	}
	plan.NoteSignIn(previous, hints)
	return plan
}
