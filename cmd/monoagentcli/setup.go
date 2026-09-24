package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/monoes/mono-agent/internal/health"
)

// setupWants is what the user asked for up front (flags); anything not
// asked for is offered interactively when stdin is a terminal.
type setupWants struct {
	runtimes  []string
	autostart bool
	mcp       bool
}

func newSetupCmd(cfg *globalConfig) *cobra.Command {
	var yes bool
	var wants setupWants
	cmd := &cobra.Command{
		Use:   "setup",
		Short: "Guided setup: install and configure everything monoagent needs",
		Long: `Walks this machine from nothing to ready, in order:

  1. checks everything (the same checks as ` + "`doctor`" + `)
  2. applies the fixes: data folder and database, profile folder, Node.js
     (downloads a private one when needed), monomind, the profile's
     monomind setup, the workflow daemon — asking before anything that
     installs software (--yes accepts)
  3. offers the optional extras: an AI agent runtime, starting the daemon at
     login, registering monoagent's MCP server with Claude Code (or pass
     --runtime/--autostart/--mcp)
  4. prints the final report and what is left to do by hand

Safe to run again at any time; it only does what is still missing.`,
		Example: `  monoagentcli setup
  monoagentcli setup --yes --runtime claude --autostart`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			reg := health.Default()
			opts := health.Options{}
			out := cmd.OutOrStdout()
			// In JSON mode stdout carries the final report; narration and
			// progress go to stderr as NDJSON events.
			narrate := func(msg string) { fmt.Fprintf(out, "\n▸ %s\n", msg) }
			progress := progressWriter(out, false)
			if cfg.JSONOutput {
				progress = progressWriter(os.Stderr, true)
				narrate = progress
			}
			interactive := !cfg.JSONOutput && stdinIsTerminal()
			in := bufio.NewReader(cmd.InOrStdin())
			ask := func(question string) string {
				if !interactive {
					return ""
				}
				fmt.Fprintf(out, "  %s ", question)
				ans, _ := in.ReadString('\n')
				return strings.TrimSpace(ans)
			}

			narrate("Checking this machine")
			rep := runHealth(ctx, cfg, reg, opts)

			narrate("Fixing what is missing")
			rep, outcomes := fixUntilStable(ctx, cfg, reg, opts, rep, fixPrompter(cmd, yes, cfg.JSONOutput), progress)

			ids := optionalFixIDs(rep, wants, ask)
			if len(ids) > 0 {
				narrate("Optional extras")
				for _, id := range ids {
					f, ok := reg.Fix(id)
					if !ok {
						continue
					}
					progress("→ " + f.Label)
					if err := applyFix(ctx, cfg, f, progress); err != nil {
						progress("✗ " + err.Error())
						outcomes = append(outcomes, fixOutcome{ID: id, Outcome: "failed", Error: err.Error()})
						continue
					}
					outcomes = append(outcomes, fixOutcome{ID: id, Outcome: "applied"})
				}
				rep = runHealth(ctx, cfg, reg, opts)
			}

			if cfg.JSONOutput {
				enc := json.NewEncoder(out)
				enc.SetIndent("", "  ")
				if err := enc.Encode(doctorJSON{Report: rep, Fixes: outcomes}); err != nil {
					return err
				}
			} else {
				narrate("Result")
				printDoctorReport(out, rep, true)
				printManualSteps(out, rep)
			}
			if n := rep.RequiredFailures(); n > 0 {
				return errRequiredChecksFailed(n)
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&yes, "yes", false, "Accept fixes that install software without asking")
	cmd.Flags().StringSliceVar(&wants.runtimes, "runtime", nil, "Also install these AI agent runtimes (e.g. claude, codex)")
	cmd.Flags().BoolVar(&wants.autostart, "autostart", false, "Also start the daemon at login")
	cmd.Flags().BoolVar(&wants.mcp, "mcp", false, "Also register monoagent's MCP server with Claude Code")
	return cmd
}

// optionalFixIDs decides which optional fixes setup applies: the ones the
// flags ask for, plus — when ask can reach a person — the ones they accept.
// A runtime is only offered when none is installed yet, so re-running setup
// doesn't nag.
func optionalFixIDs(rep *health.Report, w setupWants, ask func(question string) string) []string {
	offered := map[string]bool{}
	var installable []string
	commands := map[string]string{}
	runtimesInstalled := false
	for _, r := range rep.Results {
		if r.ID == health.CheckRuntimes && r.Status == health.StatusOK {
			runtimesInstalled = true
		}
		if r.Fix == nil || !r.Fix.Optional {
			continue
		}
		offered[r.Fix.ID] = true
		if strings.HasPrefix(r.Fix.ID, health.FixRuntimeInstall+":") {
			id := strings.TrimPrefix(r.Fix.ID, health.FixRuntimeInstall+":")
			installable = append(installable, id)
			commands[id] = r.Fix.Command
		}
	}
	yesTo := func(q string) bool {
		a := strings.ToLower(ask(q + " [y/N]"))
		return a == "y" || a == "yes"
	}

	var ids []string
	runtimes := w.runtimes
	if len(runtimes) == 0 && !runtimesInstalled && len(installable) > 0 {
		// Name what each choice runs (a vendor script's URL, or the npm
		// package) before anyone picks one: installing it is the consent.
		var q strings.Builder
		q.WriteString("No AI agent runtime is installed. These can be installed:\n")
		for _, id := range installable {
			fmt.Fprintf(&q, "    %s — %s\n", id, commands[id])
		}
		q.WriteString("  Install which? (comma-separated, Enter to skip):")
		if a := ask(q.String()); a != "" {
			runtimes = strings.Split(a, ",")
		}
	}
	for _, rt := range runtimes {
		id := health.FixRuntimeInstall + ":" + strings.TrimSpace(rt)
		if offered[id] || len(w.runtimes) > 0 {
			ids = append(ids, id) // flags win even if the check didn't list it
		}
	}
	if offered[health.FixAutostart] && (w.autostart || yesTo("Start the workflow daemon automatically at login?")) {
		ids = append(ids, health.FixAutostart)
	}
	if offered[health.FixMCPRegister] && (w.mcp || yesTo("Register monoagent's tools (MCP) with Claude Code?")) {
		ids = append(ids, health.FixMCPRegister)
	}
	return ids
}

// printManualSteps lists what setup could not do itself: steps only a
// person can take, and fixes that need a yes nobody gave (no terminal to
// ask on, or declined), so a run that fixed nothing does not look done.
func printManualSteps(w io.Writer, rep *health.Report) {
	var steps, declined []string
	for _, r := range rep.Results {
		switch {
		case r.Fix == nil || r.Fix.Optional:
		case r.Fix.Safety == health.SafetyManual:
			steps = append(steps, fmt.Sprintf("  • %s: %s", r.Title, r.Fix.Command))
		case r.Fix.Safety == health.SafetyConfirm:
			declined = append(declined, fmt.Sprintf("  • %s: %s", r.Title, r.Fix.Label))
		}
	}
	if len(steps) > 0 {
		fmt.Fprintln(w, "\nStill to do by hand:")
		for _, s := range steps {
			fmt.Fprintln(w, s)
		}
	}
	if len(declined) > 0 {
		fmt.Fprintln(w, "\nNot done, since nobody said yes (run `monoagentcli setup` in a terminal, or add --yes to accept):")
		for _, s := range declined {
			fmt.Fprintln(w, s)
		}
	}
}
