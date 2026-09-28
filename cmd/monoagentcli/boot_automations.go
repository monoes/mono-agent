package main

import (
	"fmt"
	"os"
	"testing"

	"github.com/monoes/mono-agent/internal/nodes"
	"github.com/spf13/cobra"
)

// skipAutomationBoot keeps test binaries from booting the real ~/.monoagent;
// a test that wants the registry sets HOME to a temp dir and clears it.
var skipAutomationBoot = testing.Testing()

// bootAutomationsFor boots the automation registry (legacy fold +
// DefSource) before any command runs, so every path that reads action
// definitions (node schema/run, workflow run/validate/inputs, action …, mcp,
// httpapi, daemon) sees the installed packages. Help, version and shell
// completion skip it; a bare command group boots too, since it can't be told
// apart from a runnable parent such as `daemon`. The fold only writes when a
// legacy directory changed; a failure leaves the legacy loader in place.
func bootAutomationsFor(cmd *cobra.Command) {
	if skipAutomationBoot || !needsAutomations(cmd) {
		return
	}
	if _, err := nodes.BootAutomations(""); err != nil {
		fmt.Fprintf(os.Stderr, "warning: automations: %v (using ~/.monoagent/actions only)\n", err)
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
