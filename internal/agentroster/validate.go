package agentroster

import (
	"context"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/monoes/mono-agent/internal/monomind"
)

// DefaultModel is the roster id for a runtime that cannot list its models:
// the test runs without --model, on the runtime's own default.
const DefaultModel = "default"

// TestPrompt is the one-word test turn.
const TestPrompt = "Reply with the single word: ok"

// Target is one runtime × model pair to test.
type Target struct {
	Runtime        string   `json:"runtime"`
	Model          string   `json:"model"`
	Label          string   `json:"label,omitempty"`
	EffortLevels   []string `json:"effort_levels,omitempty"`
	Source         string   `json:"source"`
	RuntimeVersion string   `json:"runtime_version,omitempty"`
}

// Plan is what a run will do: the targets, and a cost estimate from earlier
// results (a model that never reported a cost counts in UnknownCost).
type Plan struct {
	Targets     []Target `json:"targets"`
	Calls       int      `json:"calls"`
	EstCostUSD  float64  `json:"est_cost_usd"`
	UnknownCost int      `json:"unknown_cost"`
	// Skipped lists runtimes that could not be planned (not installed, model
	// listing failed); the latter still get a "default" target.
	Skipped []SkippedRuntime `json:"skipped,omitempty"`
}

// SkippedRuntime explains a runtime left out of, or narrowed in, a plan.
type SkippedRuntime struct {
	Runtime string `json:"runtime"`
	Reason  string `json:"reason"`
}

// EstimateCost fills p's cost estimate from earlier results.
func (p *Plan) EstimateCost(previous []Result) {
	byKey := map[string]Result{}
	for _, r := range previous {
		byKey[r.Runtime+"\x00"+r.Model] = r
	}
	p.Calls, p.EstCostUSD, p.UnknownCost = len(p.Targets), 0, 0
	for _, t := range p.Targets {
		if r, ok := byKey[t.Runtime+"\x00"+t.Model]; ok && r.HasCost {
			p.EstCostUSD += r.CostUSD
		} else {
			p.UnknownCost++
		}
	}
}

// Summary counts a run's results.
type Summary struct {
	RunID     string `json:"run_id"`
	Planned   int    `json:"planned"`
	OK        int    `json:"ok"`
	Failed    int    `json:"failed"`
	Cancelled int    `json:"cancelled"`
}

// Line is one NDJSON progress line of a validate run.
type Line struct {
	Type    string   `json:"type"` // validate.plan|validate.started|validate.result|validate.done
	RunID   string   `json:"run_id,omitempty"`
	Plan    *Plan    `json:"plan,omitempty"`
	Target  *Target  `json:"target,omitempty"`
	Result  *Result  `json:"result,omitempty"`
	Summary *Summary `json:"summary,omitempty"`
}

// ExecFunc runs one exec turn; monomind.Exec in production.
type ExecFunc func(ctx context.Context, opts monomind.ExecOptions, onEvent func(monomind.Event)) (*monomind.TurnResult, error)

// RunOptions configures Run.
type RunOptions struct {
	RunID       string
	Bin         string
	Timeout     time.Duration
	Concurrency int // runtimes tested at once; each runtime runs one test at a time
	Exec        ExecFunc
	Now         func() time.Time
	// Save stores each result as it arrives; nil skips storing.
	Save func(Result) error
}

// Run tests every target and emits progress lines. Targets of one runtime
// run one after another, so no single CLI gets rate-limited; up to
// Concurrency runtimes run at once. A cancelled ctx stops the run: tests not
// yet started are counted as cancelled and nothing is stored for them.
func Run(ctx context.Context, targets []Target, opts RunOptions, emit func(Line)) Summary {
	if opts.Concurrency < 1 {
		opts.Concurrency = 3
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.Exec == nil {
		opts.Exec = monomind.Exec
	}
	var order []string
	groups := map[string][]Target{}
	for _, t := range targets {
		if _, seen := groups[t.Runtime]; !seen {
			order = append(order, t.Runtime)
		}
		groups[t.Runtime] = append(groups[t.Runtime], t)
	}

	sum := Summary{RunID: opts.RunID, Planned: len(targets)}
	var mu sync.Mutex // guards sum and emit
	send := func(l Line) {
		l.RunID = opts.RunID
		emit(l)
	}
	sem := make(chan struct{}, opts.Concurrency)
	var wg sync.WaitGroup
	for _, rt := range order {
		wg.Add(1)
		go func(list []Target) {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
				defer func() { <-sem }()
			case <-ctx.Done():
			}
			for _, t := range list {
				if ctx.Err() != nil {
					mu.Lock()
					sum.Cancelled++
					mu.Unlock()
					continue
				}
				t := t
				mu.Lock()
				send(Line{Type: "validate.started", Target: &t})
				mu.Unlock()
				r := testOne(ctx, t, opts)
				mu.Lock()
				switch {
				case r.Status == StatusCancelled:
					sum.Cancelled++
				case Works(r.Status):
					sum.OK++
				default:
					sum.Failed++
				}
				if r.Status != StatusCancelled && opts.Save != nil {
					if err := opts.Save(r); err != nil {
						r.Detail = strings.TrimSpace(r.Detail + " (not saved: " + err.Error() + ")")
					}
				}
				send(Line{Type: "validate.result", Target: &t, Result: &r})
				mu.Unlock()
			}
		}(groups[rt])
	}
	wg.Wait()
	send(Line{Type: "validate.done", Summary: &sum})
	return sum
}

// testOne runs the test turn for one target in a throwaway folder.
func testOne(ctx context.Context, t Target, opts RunOptions) Result {
	r := Result{
		Runtime: t.Runtime, Model: t.Model, Label: t.Label, EffortLevels: t.EffortLevels,
		Source: orDefault(t.Source, SourceListed), RuntimeVersion: t.RuntimeVersion, RunID: opts.RunID,
	}
	dir, err := os.MkdirTemp("", "monoagent-validate-")
	if err != nil {
		r.Status, r.Detail, r.ValidatedAt = StatusError, clip(err.Error()), opts.Now()
		return r
	}
	defer os.RemoveAll(dir)

	model := t.Model
	if model == DefaultModel {
		model = ""
	}
	start := opts.Now()
	var first time.Time
	res, execErr := opts.Exec(ctx, monomind.ExecOptions{
		Bin:      opts.Bin,
		Runtime:  t.Runtime,
		Model:    model,
		Prompt:   TestPrompt,
		Cwd:      dir,
		MaxTurns: 1,
		Timeout:  opts.Timeout,
		Stderr:   discard{},
	}, func(ev monomind.Event) {
		if ev.Type == monomind.EventAssistant && first.IsZero() {
			first = opts.Now()
		}
	})
	end := opts.Now()
	if ctx.Err() != nil {
		r.Status, r.Detail = StatusCancelled, "cancelled"
	} else {
		r.Status, r.Detail = Classify(res, execErr)
	}
	r.LatencyMs = end.Sub(start).Milliseconds()
	if !first.IsZero() {
		r.LatencyFirstMs = first.Sub(start).Milliseconds()
	}
	if res != nil {
		r.Reply = clip(res.ResultText)
		r.TokensIn, r.TokensOut = res.InputTokens, res.OutputTokens
		r.CostUSD, r.HasCost = res.CostUSD, res.HasCostUSD
	}
	r.ValidatedAt = end
	return r
}

// discard drops monomind's stderr diagnostics; the result carries the error.
type discard struct{}

func (discard) Write(p []byte) (int, error) { return len(p), nil }
