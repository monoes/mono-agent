package main

import (
	"context"

	"github.com/spf13/cobra"

	"github.com/monoes/mono-agent/internal/account"
)

// startServingGuard starts the refresher of the process guard at once. A process
// that serves (daemon, httpapi, mcp, extension serve, org serve --foreground)
// must not wait the account.LateRefresher that every other process waits (spec
// section 6.4): a daemon that sat out its first minutes could not notice a
// refusal in time. With no guard installed there is nothing to start. A test
// replaces it.
var startServingGuard = func(ctx context.Context) {
	g := account.Current()
	if g == nil {
		return
	}
	if ctx == nil {
		ctx = context.Background()
	}
	g.StartRefresher(ctx)
}

// servingCommand marks c as a command that serves for as long as it runs: its pre-run
// starts the account guard's refresher. when, if given, narrows that to the
// invocations that really serve (`org serve` without --foreground starts a
// background process and exits). The command's own PreRun, if any, still runs.
func servingCommand(c *cobra.Command, when ...func(*cobra.Command) bool) *cobra.Command {
	prev := c.PreRun
	c.PreRun = func(cmd *cobra.Command, args []string) {
		if len(when) == 0 || when[0](cmd) {
			startServingGuard(cmd.Context())
		}
		if prev != nil {
			prev(cmd, args)
		}
	}
	return c
}

// servesInForeground is servingCommand's predicate for `org serve`: only
// `--foreground` (and not `--stop`) keeps this process alive to serve.
func servesInForeground(cmd *cobra.Command) bool {
	foreground, _ := cmd.Flags().GetBool("foreground")
	stop, _ := cmd.Flags().GetBool("stop")
	return foreground && !stop
}
