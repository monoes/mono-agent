package main

import (
	"fmt"
	"os"
	"testing"

	"github.com/monoes/mono-agent/internal/nodes"
	"github.com/spf13/cobra"
)

// skipAutomationBoot keeps test binaries from seeding the real ~/.monoagent;
// a test that wants the registry sets HOME to a temp dir and clears it.
var skipAutomationBoot = testing.Testing()

// bootAutomationsFor boots the automation registry (seed + DefSource) before
// any command runs, so every path that reads action definitions (node
// schema/run, workflow run/validate/inputs, action …, mcp, httpapi, daemon)
// sees installed packages, not only the built-in seed. Help, version and
// shell completion skip it; a bare command group boots too, since it can't
// be told apart from a runnable parent such as `daemon`. Seeding only writes
// when a built-in changed; a failure leaves the legacy loader in place.
func bootAutomationsFor(cmd *cobra.Command) {
	if skipAutomationBoot || !needsAutomations(cmd) {
		return
	}
	if _, err := nodes.BootAutomations(""); err != nil {
		fmt.Fprintf(os.Stderr, "warning: automations: %v (using the built-in action set)\n", err)
	}
}

func needsAutomations(cmd *cobra.Command) bool {
	for c := cmd; c != nil; c = c.Parent() {
		switch c.Name() {
		case "help", "version", "completion", cobra.ShellCompRequestCmd, cobra.ShellCompNoDescRequestCmd:
			return false
		}
	}
	return true
}
