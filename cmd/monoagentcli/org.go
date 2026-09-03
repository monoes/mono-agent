package main

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/monoes/mono-agent/internal/monomind"
	"github.com/monoes/mono-agent/internal/orgdesign"
)

// defaultOrgProjectRoot is the project root org state resolves under
// (`<root>/.monomind/orgs/`, protocol §7.1) when --project is not given.
// mono-agent has no per-workflow project directory of its own, so orgs live
// under the app's home data dir by default, same as configs/output.
const defaultOrgProjectRoot = "~/.monoagent"

// newOrgCmd exposes monomind's org observe/action surface: thin proxies over
// `monomind org <sub> [<name>] --format json` (protocol §7). Output is
// always JSON — these commands exist for monoagentcli's own Wails bindings
// and for scripting, not interactive table browsing (mirrors the doctrine
// established by `chat`: the CLI's machine output IS the UI contract).
func newOrgCmd(cfg *globalConfig) *cobra.Command {
	var projectRoot string
	cmd := &cobra.Command{
		Use:   "org",
		Short: "Local agent organizations (via the monomind engine)",
		Long: "Observe and act on monomind-managed agent organizations — status, logs, costs, " +
			"pending questions/gates, and a live event tail. Every subcommand prints JSON to " +
			"stdout; there is no human table mode.",
	}
	cmd.PersistentFlags().StringVar(&projectRoot, "project", defaultOrgProjectRoot,
		"Project root orgs resolve under (<root>/.monomind/orgs/)")
	root := func() string { return expandPath(projectRoot) }

	cmd.AddCommand(
		newOrgListCmd(root),
		newOrgStatusCmd(root),
		newOrgRunCmd(root),
		newOrgLogsCmd(root),
		newOrgReportCmd(root),
		newOrgCostsCmd(root),
		newOrgFlowCmd(root),
		newOrgQuestionsCmd(root),
		newOrgApprovalsCmd(root),
		newOrgGatesCmd(root),
		newOrgDecisionsCmd(root),
		newOrgMemoryCmd(root),
		newOrgAnswerCmd(root),
		newOrgApproveCmd(root),
		newOrgDenyCmd(root),
		newOrgGateApproveCmd(root),
		newOrgGateRejectCmd(root),
		newOrgEventsCmd(root),
		newOrgValidateCmd(root),
		newOrgReloadCmd(root),
		newOrgCreateJSONCmd(root),
	)
	return cmd
}

// printOrgJSON writes a raw JSON payload to stdout with a trailing newline.
func printOrgJSON(payload []byte) error {
	_, err := fmt.Println(string(payload))
	return err
}

func newOrgListCmd(root func() string) *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List all orgs in the project",
		RunE: func(cmd *cobra.Command, args []string) error {
			out, err := monomind.OrgList(cmd.Context(), root())
			if err != nil {
				return err
			}
			return printOrgJSON(out)
		},
	}
}

func newOrgStatusCmd(root func() string) *cobra.Command {
	return &cobra.Command{
		Use:   "status [name]",
		Short: "Show one org's status, or every org's when name is omitted",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := ""
			if len(args) == 1 {
				name = args[0]
			}
			out, err := monomind.OrgStatus(cmd.Context(), root(), name)
			if err != nil {
				return err
			}
			return printOrgJSON(out)
		},
	}
}

func newOrgLogsCmd(root func() string) *cobra.Command {
	var run string
	c := &cobra.Command{
		Use:   "logs <name>",
		Short: "Show the org's bus event log",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			out, err := monomind.OrgLogs(cmd.Context(), root(), args[0], run)
			if err != nil {
				return err
			}
			return printOrgJSON(out)
		},
	}
	c.Flags().StringVar(&run, "run", "", "Specific run id (default: most recent run)")
	return c
}

func newOrgReportCmd(root func() string) *cobra.Command {
	var all bool
	var run string
	c := &cobra.Command{
		Use:   "report <name>",
		Short: "Show the org's run report",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			out, err := monomind.OrgReport(cmd.Context(), root(), args[0], all, run)
			if err != nil {
				return err
			}
			return printOrgJSON(out)
		},
	}
	c.Flags().BoolVar(&all, "all", false, "Report on every run instead of just one")
	c.Flags().StringVar(&run, "run", "", "Specific run id (default: most recent run; ignored with --all)")
	return c
}

func newOrgRunCmd(root func() string) *cobra.Command {
	var task string
	var dryRun bool
	c := &cobra.Command{
		Use:   "run <name>",
		Short: "Start an org run (blocks until it completes or hands off to a live serve daemon)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			out, err := monomind.OrgRun(cmd.Context(), root(), args[0], task, dryRun)
			if err != nil {
				return err
			}
			return printOrgJSON(out)
		},
	}
	c.Flags().StringVar(&task, "task", "", "Initial message/goal override for this run")
	c.Flags().BoolVar(&dryRun, "dry-run", false, "Preview role briefings without starting sessions")
	return c
}

func newOrgCostsCmd(root func() string) *cobra.Command {
	var run string
	c := &cobra.Command{
		Use:   "costs <name>",
		Short: "Show per-role token/cost totals",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			out, err := monomind.OrgCosts(cmd.Context(), root(), args[0], run)
			if err != nil {
				return err
			}
			return printOrgJSON(out)
		},
	}
	c.Flags().StringVar(&run, "run", "", "Specific run id (default: most recent run)")
	return c
}

func newOrgFlowCmd(root func() string) *cobra.Command {
	var run string
	c := &cobra.Command{
		Use:   "flow <name>",
		Short: "Show the org's role communication graph",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			out, err := monomind.OrgFlow(cmd.Context(), root(), args[0], run)
			if err != nil {
				return err
			}
			return printOrgJSON(out)
		},
	}
	c.Flags().StringVar(&run, "run", "", "Specific run id (default: most recent run)")
	return c
}

func newOrgQuestionsCmd(root func() string) *cobra.Command {
	return &cobra.Command{
		Use:   "questions <name>",
		Short: "List pending human-input questions",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			out, err := monomind.OrgQuestions(cmd.Context(), root(), args[0])
			if err != nil {
				return err
			}
			return printOrgJSON(out)
		},
	}
}

func newOrgApprovalsCmd(root func() string) *cobra.Command {
	return &cobra.Command{
		Use:   "approvals <name>",
		Short: "List pending tool/action approval requests",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			out, err := monomind.OrgApprovals(cmd.Context(), root(), args[0])
			if err != nil {
				return err
			}
			return printOrgJSON(out)
		},
	}
}

func newOrgGatesCmd(root func() string) *cobra.Command {
	return &cobra.Command{
		Use:   "gates <name>",
		Short: "List pending decision gates",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			out, err := monomind.OrgGates(cmd.Context(), root(), args[0])
			if err != nil {
				return err
			}
			return printOrgJSON(out)
		},
	}
}

func newOrgDecisionsCmd(root func() string) *cobra.Command {
	var run string
	c := &cobra.Command{
		Use:   "decisions <name>",
		Short: "Show the org's decision trace",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			out, err := monomind.OrgDecisions(cmd.Context(), root(), args[0], run)
			if err != nil {
				return err
			}
			return printOrgJSON(out)
		},
	}
	c.Flags().StringVar(&run, "run", "", "Specific run id (default: most recent run)")
	return c
}

func newOrgMemoryCmd(root func() string) *cobra.Command {
	return &cobra.Command{
		Use:   "memory <name>",
		Short: "Show org memory statistics",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			out, err := monomind.OrgMemoryStats(cmd.Context(), root(), args[0])
			if err != nil {
				return err
			}
			return printOrgJSON(out)
		},
	}
}

func newOrgAnswerCmd(root func() string) *cobra.Command {
	return &cobra.Command{
		Use:   "answer <name> <questionId> <answer...>",
		Short: "Answer a pending human-input question",
		Args:  cobra.MinimumNArgs(3),
		RunE: func(cmd *cobra.Command, args []string) error {
			answer := joinRemainder(args[2:])
			out, err := monomind.OrgAnswer(cmd.Context(), root(), args[0], args[1], answer)
			if err != nil {
				return err
			}
			return printOrgJSON(out)
		},
	}
}

func newOrgApproveCmd(root func() string) *cobra.Command {
	return &cobra.Command{
		Use:   "approve <name> <role> <action>",
		Short: "Approve a pending tool-approval request",
		Args:  cobra.ExactArgs(3),
		RunE: func(cmd *cobra.Command, args []string) error {
			out, err := monomind.OrgApprove(cmd.Context(), root(), args[0], args[1], args[2])
			if err != nil {
				return err
			}
			return printOrgJSON(out)
		},
	}
}

func newOrgDenyCmd(root func() string) *cobra.Command {
	return &cobra.Command{
		Use:   "deny <name> <role> <action>",
		Short: "Deny a pending tool-approval request",
		Args:  cobra.ExactArgs(3),
		RunE: func(cmd *cobra.Command, args []string) error {
			out, err := monomind.OrgDeny(cmd.Context(), root(), args[0], args[1], args[2])
			if err != nil {
				return err
			}
			return printOrgJSON(out)
		},
	}
}

func newOrgGateApproveCmd(root func() string) *cobra.Command {
	return &cobra.Command{
		Use:   "gate-approve <name> <gateId> [resolution...]",
		Short: "Approve a decision gate",
		Args:  cobra.MinimumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			resolution := joinRemainder(args[2:])
			out, err := monomind.OrgGateApprove(cmd.Context(), root(), args[0], args[1], resolution)
			if err != nil {
				return err
			}
			return printOrgJSON(out)
		},
	}
}

func newOrgGateRejectCmd(root func() string) *cobra.Command {
	return &cobra.Command{
		Use:   "gate-reject <name> <gateId> [resolution...]",
		Short: "Reject a decision gate",
		Args:  cobra.MinimumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			resolution := joinRemainder(args[2:])
			out, err := monomind.OrgGateReject(cmd.Context(), root(), args[0], args[1], resolution)
			if err != nil {
				return err
			}
			return printOrgJSON(out)
		},
	}
}

func newOrgEventsCmd(root func() string) *cobra.Command {
	var run, since string
	var follow bool
	c := &cobra.Command{
		Use:   "events <name>",
		Short: "Stream the org's bus event log as NDJSON",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return monomind.OrgEvents(cmd.Context(), root(), args[0], monomind.OrgEventsOptions{
				Run:    run,
				Follow: follow,
				Since:  since,
			}, func(line []byte) {
				os.Stdout.Write(line)
				os.Stdout.Write([]byte("\n"))
			})
		},
	}
	c.Flags().StringVar(&run, "run", "", "Specific run id (default: current run)")
	c.Flags().BoolVarP(&follow, "follow", "f", false, "Keep streaming as new events arrive")
	c.Flags().StringVar(&since, "since", "", "Replay from a cursor (event id or ISO timestamp)")
	return c
}

func newOrgValidateCmd(root func() string) *cobra.Command {
	return &cobra.Command{
		Use:   "validate <name>",
		Short: "Validate an org's config against the runtime schema and structural rules",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			out, valErr := monomind.OrgValidate(cmd.Context(), root(), name)
			payload := map[string]interface{}{
				"v":     1,
				"org":   name,
				"valid": valErr == nil,
			}
			if valErr != nil {
				payload["error"] = valErr.Error()
			} else {
				payload["output"] = out
			}
			b, err := json.Marshal(payload)
			if err != nil {
				return err
			}
			return printOrgJSON(b)
		},
	}
}

// newOrgCreateJSONCmd is the only CLI path that can set custom roles — `org
// create` (the real monomind binary) only scaffolds from a fixed set of
// templates, and monomind itself has no --project flag at all (every other
// org subcommand's --project support comes entirely from monoagentcli
// itself spawning monomind with the resolved directory as its cwd; a model
// with Bash access calling `monomind` directly, bypassing monoagentcli,
// gets none of that and silently resolves against its own actual cwd).
// This command sidesteps monomind's binary entirely for the write itself —
// it reuses the exact orgdesign.Doc/Save path the Wails GUI's own
// SaveOrgDesign already goes through (wails-app/app_orgs_design.go) — then
// still runs monomind's real `org validate` afterward so the response
// reflects the same schema/structural checks every other org gets.
func newOrgCreateJSONCmd(root func() string) *cobra.Command {
	var jsonBlob string
	c := &cobra.Command{
		Use:   "create-json <name>",
		Short: "Create or overwrite an org from a full JSON document (the only way to set custom roles from the CLI)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			var d orgdesign.Doc
			if err := json.Unmarshal([]byte(jsonBlob), &d); err != nil {
				return fmt.Errorf("invalid --json: %w", err)
			}
			if d.Name == "" {
				d.Name = name
			} else if d.Name != name {
				return fmt.Errorf("org name %q in --json does not match the <name> argument %q", d.Name, name)
			}
			sha, err := orgdesign.Save(root(), &d)
			if err != nil {
				return err
			}
			out, valErr := monomind.OrgValidate(cmd.Context(), root(), name)
			payload := map[string]interface{}{
				"v": 1, "org": name, "sha256": sha, "valid": valErr == nil,
			}
			if valErr != nil {
				payload["validate_error"] = valErr.Error()
			} else {
				payload["validate_output"] = out
			}
			b, err := json.Marshal(payload)
			if err != nil {
				return err
			}
			return printOrgJSON(b)
		},
	}
	c.Flags().StringVar(&jsonBlob, "json", "", `Full org document JSON: {"name","goal","status","schedule","run_config":{...},"roles":[{"id","title","type","reports_to","responsibilities":[...],"policy":{...},...}]} — same shape as .monomind/orgs/<name>.json`)
	_ = c.MarkFlagRequired("json")
	return c
}

func newOrgReloadCmd(root func() string) *cobra.Command {
	return &cobra.Command{
		Use:   "reload <name>",
		Short: "Signal a running org's daemon to reload its config",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			out, reloadErr := monomind.OrgReload(cmd.Context(), root(), name)
			payload := map[string]interface{}{
				"v":        1,
				"org":      name,
				"reloaded": reloadErr == nil,
			}
			if reloadErr != nil {
				payload["error"] = reloadErr.Error()
			} else {
				payload["output"] = out
			}
			b, err := json.Marshal(payload)
			if err != nil {
				return err
			}
			return printOrgJSON(b)
		},
	}
}

// joinRemainder joins trailing positional args with a space, mirroring
// monomind's own answer/gate-approve/gate-reject argument handling.
func joinRemainder(args []string) string {
	out := ""
	for i, a := range args {
		if i > 0 {
			out += " "
		}
		out += a
	}
	return out
}
