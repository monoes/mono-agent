package capturesummary

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/monoes/mono-agent/internal/monomind"
)

// DefaultBudgetUSD caps what one summary may spend. A summary of an hour of
// transcript costs well under this; the cap exists for a runtime that
// decides to go exploring.
const DefaultBudgetUSD = 1.0

var nowFunc = time.Now

// ExecRunner runs a summary as one `monomind agent exec` turn — the same
// path `monoagentcli chat --runtime <r> [--model <m>] --no-history` takes, called
// in-process rather than by shelling out to ourselves. No tools are wired
// (ExecOptions.Tools empty sends `--tools none`), no session is resumed,
// and nothing is written to chat history.
func ExecRunner(budgetUSD float64) RunFunc {
	if budgetUSD <= 0 {
		budgetUSD = DefaultBudgetUSD
	}
	return func(ctx context.Context, t Target, prompt string) (Answer, error) {
		bin, _, err := monomind.Ensure(ctx)
		if err != nil {
			return Answer{}, err
		}
		opts := monomind.ExecOptions{
			Bin:       bin,
			Runtime:   t.Runtime,
			Model:     t.Model,
			Prompt:    prompt,
			BudgetUSD: budgetUSD,
		}
		if deadline, ok := ctx.Deadline(); ok {
			// Let the runtime stop itself a little before we would kill it,
			// so a slow turn reports "timeout" rather than a dead pipe.
			if d := deadline.Sub(nowFunc()); d > 0 {
				opts.Timeout = d
			}
		}
		res, err := monomind.Exec(ctx, opts, nil)
		if err != nil {
			return Answer{}, err
		}
		return answerOf(res)
	}
}

// answerOf turns a finished turn into an Answer, or an error that says why
// it is not one. A turn that stopped on a limit, or never said it was done,
// did not produce a summary even when it produced some text.
func answerOf(res *monomind.TurnResult) (Answer, error) {
	a := Answer{Text: strings.TrimSpace(res.ResultText)}
	if res.HasCostUSD {
		a.CostUSD = res.CostUSD
	}
	if res.Err != nil {
		return a, fmt.Errorf("%s", strings.TrimSpace(res.Err.Code+" "+res.Err.Message))
	}
	switch res.StopReason {
	case "", "end_turn":
	default:
		return a, fmt.Errorf("the turn stopped early (%s)", res.StopReason)
	}
	if !res.SawDone && res.ExitCode != 0 {
		return a, fmt.Errorf("the runtime exited with code %d", res.ExitCode)
	}
	return a, nil
}
