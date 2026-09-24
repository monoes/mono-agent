package health

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

// Registry holds the known checks and fixes.
type Registry struct {
	checks []Check
	fixes  map[string]Fix
}

// NewRegistry builds a registry; duplicate IDs panic (a programming error).
func NewRegistry(checks []Check, fixes []Fix) *Registry {
	r := &Registry{fixes: map[string]Fix{}}
	seen := map[string]bool{}
	for _, c := range checks {
		if seen[c.ID] {
			panic("health: duplicate check " + c.ID)
		}
		seen[c.ID] = true
		r.checks = append(r.checks, c)
	}
	for _, f := range fixes {
		if _, dup := r.fixes[f.ID]; dup {
			panic("health: duplicate fix " + f.ID)
		}
		r.fixes[f.ID] = f
	}
	return r
}

// Checks returns the registered checks in registration order.
func (r *Registry) Checks() []Check { return r.checks }

// Fix returns a registered fix, resolving "<id>:<arg>" for parameterized
// ones.
func (r *Registry) Fix(id string) (Fix, bool) {
	if f, ok := r.fixes[id]; ok && f.ApplyArg == nil {
		return f, true
	}
	base, arg, ok := strings.Cut(id, ":")
	if !ok || arg == "" {
		return Fix{}, false
	}
	f, ok := r.fixes[base]
	if !ok || f.ApplyArg == nil {
		return Fix{}, false
	}
	info := f.FixInfo
	info.ID = id
	info.Label = strings.ReplaceAll(info.Label, "{arg}", arg)
	info.Command = strings.ReplaceAll(info.Command, "{arg}", arg)
	applyArg := f.ApplyArg
	return Fix{FixInfo: info, Apply: func(ctx context.Context, env *Env, progress func(string)) error {
		return applyArg(ctx, env, arg, progress)
	}}, true
}

// Options selects which checks run.
type Options struct {
	Deep   bool     // include Network checks
	Groups []string // empty = all
	IDs    []string // empty = all
}

func (o Options) selects(c Check) bool {
	if c.Network && !o.Deep && len(o.IDs) == 0 {
		return false
	}
	if len(o.Groups) > 0 && !contains(o.Groups, c.Group) {
		return false
	}
	if len(o.IDs) > 0 && !contains(o.IDs, c.ID) {
		return false
	}
	return true
}

// Run executes the selected checks. Checks run in dependency levels, in
// parallel within a level; a check whose dependency failed or was skipped
// is reported as skip without running. Dependencies outside the selection
// run too (their results are included) so a single `--check` is still
// judged in context.
func (r *Registry) Run(ctx context.Context, env *Env, opts Options) *Report {
	byID := map[string]Check{}
	for _, c := range r.checks {
		byID[c.ID] = c
	}
	want := map[string]bool{}
	var add func(id string)
	add = func(id string) {
		c, ok := byID[id]
		if !ok || want[id] {
			return
		}
		want[id] = true
		for _, d := range c.DependsOn {
			add(d)
		}
	}
	for _, c := range r.checks {
		if opts.selects(c) {
			add(c.ID)
		}
	}

	results := map[string]Result{}
	done := map[string]bool{}
	for len(done) < len(want) {
		var level []Check
		for _, c := range r.checks {
			if !want[c.ID] || done[c.ID] {
				continue
			}
			ready := true
			for _, d := range c.DependsOn {
				if want[d] && !done[d] {
					ready = false
				}
			}
			if ready {
				level = append(level, c)
			}
		}
		if len(level) == 0 { // dependency cycle — report the rest as skipped
			for _, c := range r.checks {
				if want[c.ID] && !done[c.ID] {
					results[c.ID] = r.finish(c, Result{Status: StatusSkip, Summary: "dependency cycle"}, 0)
					done[c.ID] = true
				}
			}
			break
		}
		// Skips are decided against earlier levels only; this level's
		// results land in levelRes and are merged after the wait, so the
		// shared map is never touched concurrently.
		levelRes := make([]Result, len(level))
		var wg sync.WaitGroup
		for i, c := range level {
			done[c.ID] = true
			if blocked := blockedBy(c, results); blocked != "" {
				levelRes[i] = r.finish(c, Result{Status: StatusSkip, Summary: "waiting on " + blocked}, 0)
				continue
			}
			wg.Add(1)
			go func(i int, c Check) {
				defer wg.Done()
				levelRes[i] = r.runOne(ctx, env, c)
			}(i, c)
		}
		wg.Wait()
		for i, c := range level {
			results[c.ID] = levelRes[i]
		}
	}

	rep := &Report{
		V:                SchemaVersion,
		GeneratedAt:      time.Now().UTC(),
		MonoagentVersion: env.Version,
		ProfileID:        env.ProfileID,
		Deep:             opts.Deep,
		Summary:          map[Status]int{},
	}
	for _, c := range r.checks { // registration order
		res, ok := results[c.ID]
		if !ok {
			continue
		}
		children := res.Children
		res.Children = nil
		rep.Results = append(rep.Results, res)
		rep.Summary[res.Status]++
		for _, ch := range children {
			ch.Group = c.Group
			if ch.Source == "" {
				ch.Source = res.Source
			}
			if ch.FixID != "" && ch.Status != StatusOK && ch.Status != StatusSkip {
				if f, ok := r.Fix(ch.FixID); ok {
					info := f.FixInfo
					if ch.FixCommand != "" {
						info.Command = ch.FixCommand
					}
					ch.Fix = &info
				}
			}
			rep.Results = append(rep.Results, ch)
			rep.Summary[ch.Status]++
		}
	}
	return rep
}

func blockedBy(c Check, results map[string]Result) string {
	deps := append([]string(nil), c.DependsOn...)
	sort.Strings(deps)
	for _, d := range deps {
		if res, ok := results[d]; ok && (res.Status == StatusFail || res.Status == StatusSkip) {
			if res.Title != "" {
				return res.Title
			}
			return d
		}
	}
	return ""
}

func (r *Registry) runOne(ctx context.Context, env *Env, c Check) (res Result) {
	timeout := c.Timeout
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	start := time.Now()

	ch := make(chan Result, 1)
	go func() {
		defer func() {
			if p := recover(); p != nil {
				ch <- Result{Status: StatusFail, Summary: "check crashed", Detail: fmt.Sprint(p)}
			}
		}()
		ch <- c.Run(cctx, env)
	}()
	select {
	case res = <-ch:
	case <-cctx.Done():
		res = Result{Status: StatusFail, Summary: fmt.Sprintf("timed out after %s", timeout)}
	}
	return r.finish(c, res, time.Since(start))
}

// finish stamps a check's static metadata onto its result.
func (r *Registry) finish(c Check, res Result, took time.Duration) Result {
	res.ID, res.Group, res.Title = c.ID, c.Group, c.Title
	res.Required, res.Features = c.Required, c.Features
	if res.Source == "" {
		res.Source = "monoagent"
	}
	res.Millis = took.Milliseconds()
	if res.FixID != "" && res.Status != StatusOK && res.Status != StatusSkip {
		if f, ok := r.Fix(res.FixID); ok {
			info := f.FixInfo
			if res.FixCommand != "" {
				info.Command = res.FixCommand
			}
			res.Fix = &info
		}
	}
	return res
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}
