package main

import (
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/monoes/mono-agent/internal/automation"
	"github.com/spf13/cobra"
)

// errConfirmationRequired is returned when an install needs a yes/no answer
// that nobody can give: --json, or stdin is not a terminal (contracts §5).
var errConfirmationRequired = errors.New("confirmation required (pass --yes to install, or --dry-run to review)")

// installConfirmer decides whether an install proceeds after its review.
// interactive=false means no prompt is possible.
type installConfirmer struct {
	yes         bool
	interactive bool
	in          io.Reader
	out         io.Writer
}

// runInstall runs the review → confirm → install flow shared by
// `automation install` and `action import`. do performs the real (or dry)
// install for the given options; opts.DryRun only reviews.
func runInstall(opts automation.InstallOptions, c installConfirmer,
	do func(automation.InstallOptions) (*automation.InstallResult, error)) (*automation.InstallResult, error) {
	if opts.Source == "" {
		opts.Source = automation.SourceImported
	}
	if opts.DryRun {
		res, err := do(opts)
		return res, withInstallResult(err, res)
	}
	if !c.yes {
		if !c.interactive {
			return nil, errConfirmationRequired
		}
		dry := opts
		dry.DryRun = true
		review, err := do(dry)
		if err != nil {
			return nil, withInstallResult(err, review)
		}
		printInstallReview(c.out, review)
		if issuesHaveErrors(review.Issues) {
			return review, fmt.Errorf("package %s has validation errors; not installed", review.ID)
		}
		if !confirmYes(c.in, c.out, fmt.Sprintf("Install %s %s?", review.ID, review.Version)) {
			return nil, errors.New("install cancelled")
		}
		if opts.ExpectSHA256 == "" {
			opts.ExpectSHA256 = review.SHA256 // install exactly the bytes reviewed
		}
	}
	res, err := do(opts)
	return res, withInstallResult(err, res)
}

func newAutomationInstallCmd(cfg *globalConfig) *cobra.Command {
	var dryRun, yes bool
	var expectSHA string
	cmd := &cobra.Command{
		Use:   "install <file.mpkg|dir|url>",
		Short: "Install or update an automation package (shows a review first)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			reg, err := openAutomationRegistry()
			if err != nil {
				return err
			}
			c := installConfirmer{yes: yes, interactive: !cfg.JSONOutput && stdinIsTerminal(),
				in: cmd.InOrStdin(), out: cmd.ErrOrStderr()}
			res, err := runInstall(automation.InstallOptions{DryRun: dryRun, ExpectSHA256: strings.ToLower(strings.TrimSpace(expectSHA))}, c, func(o automation.InstallOptions) (*automation.InstallResult, error) {
				return reg.Install(args[0], o)
			})
			if err != nil {
				printFailedIssues(cmd, cfg, err)
				return err
			}
			return printInstallResult(cmd.OutOrStdout(), cfg, res)
		},
	}
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Validate and show the review without installing")
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "Install without asking")
	cmd.Flags().StringVar(&expectSHA, "expect-sha256", "", "Refuse unless the package bytes have this sha256 (e.g. from a --dry-run review)")
	return cmd
}

// printFailedIssues shows a failed install's validation issues on stderr
// (the --json error object carries them instead).
func printFailedIssues(cmd *cobra.Command, cfg *globalConfig, err error) {
	var re *installResultError
	if !cfg.JSONOutput && errors.As(err, &re) {
		printIssues(cmd.ErrOrStderr(), re.res.Issues)
	}
}

func printInstallResult(out io.Writer, cfg *globalConfig, res *automation.InstallResult) error {
	if cfg.JSONOutput {
		return writeJSONTo(out, res)
	}
	if res.DryRun {
		printInstallReview(out, res)
		fmt.Fprintln(out, "\nDry run: nothing installed.")
		return nil
	}
	switch {
	case res.PreviousVersion != "":
		fmt.Fprintf(out, "Updated %s %s -> %s\n", res.ID, res.PreviousVersion, res.Version)
	default:
		fmt.Fprintf(out, "Installed %s %s\n", res.ID, res.Version)
	}
	if res.Dir != "" {
		fmt.Fprintf(out, "  %s\n", res.Dir)
	}
	if res.Review.PolicyBlocked {
		fmt.Fprintf(out, "  disabled by policy: %s\n", res.Review.PolicyReason)
	}
	for _, w := range res.Warnings {
		fmt.Fprintf(out, "  warning: %s\n", w)
	}
	return nil
}

// printInstallReview is the trust review of spec §6.3: publisher, domains,
// steps, scripts, side effects, policy block and (on update) the changes.
func printInstallReview(out io.Writer, res *automation.InstallResult) {
	r := res.Review
	fmt.Fprintf(out, "Package   %s (%s) %s\n", res.Name, res.ID, res.Version)
	if res.SHA256 != "" {
		fmt.Fprintf(out, "SHA256    %s\n", res.SHA256)
	}
	if res.PreviousVersion != "" {
		fmt.Fprintf(out, "Replaces  %s\n", res.PreviousVersion)
	}
	fmt.Fprintf(out, "Publisher %s\n", orDash(r.Publisher))
	fmt.Fprintf(out, "Domains   %s\n", orDash(strings.Join(r.Domains, ", ")))
	steps := strings.Join(r.Steps, ", ")
	if steps == "" {
		steps = "(unrestricted)"
	}
	fmt.Fprintf(out, "Steps     %s\n", steps)
	scripts := strings.Join(r.Scripts, ", ")
	if scripts == "" {
		scripts = "none"
	}
	fmt.Fprintf(out, "Scripts   %s\n", scripts)
	fmt.Fprintf(out, "Downloads %v\n", r.Downloads)
	fmt.Fprintf(out, "Tier      %s\n", orDash(r.Tier))
	if len(r.ActionEffects) > 0 {
		names := make([]string, 0, len(r.ActionEffects))
		for n := range r.ActionEffects {
			names = append(names, n)
		}
		sort.Strings(names)
		fmt.Fprintln(out, "Actions")
		for _, n := range names {
			fmt.Fprintf(out, "  %-28s %s\n", n, orDash(r.ActionEffects[n]))
		}
	}
	if r.PolicyBlocked {
		fmt.Fprintf(out, "POLICY    blocked in this build: %s (installs disabled)\n", r.PolicyReason)
	}
	if ch := r.Changes; ch != nil {
		fmt.Fprintln(out, "Changes since the installed version")
		if len(ch.AddedDomains)+len(ch.AddedSteps)+len(ch.AddedScripts)+len(ch.ChangedScripts) == 0 {
			fmt.Fprintln(out, "  no new domains, steps or scripts")
		}
		printChange(out, "added domains", ch.AddedDomains)
		printChange(out, "added steps", ch.AddedSteps)
		printChange(out, "added scripts", ch.AddedScripts)
		printChange(out, "changed scripts", ch.ChangedScripts)
	}
	if len(r.Files) > 0 {
		var total int64
		for _, f := range r.Files {
			total += f.Size
		}
		fmt.Fprintf(out, "Files     %d (%d bytes)\n", len(r.Files), total)
	}
	for _, w := range res.Warnings {
		fmt.Fprintf(out, "warning: %s\n", w)
	}
	printIssues(out, res.Issues)
}

func printChange(out io.Writer, label string, items []string) {
	if len(items) > 0 {
		fmt.Fprintf(out, "  %-16s %s\n", label, strings.Join(items, ", "))
	}
}

func newAutomationExportCmd(cfg *globalConfig) *cobra.Command {
	var outFile, actions string
	var withRecordings bool
	cmd := &cobra.Command{
		Use:   "export <id>",
		Short: "Export an installed automation as a .mpkg file",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			reg, err := openAutomationRegistry()
			if err != nil {
				return err
			}
			id := args[0]
			if outFile == "" {
				info, err := reg.Info(id)
				if err != nil {
					return err
				}
				outFile = fmt.Sprintf("%s-%s.mpkg", id, info.Version)
			}
			opts := automation.ExportOptions{Actions: splitCSV(actions), WithRecordings: withRecordings}
			sum, err := writeHashed(outFile, func(w io.Writer) error { return reg.Export(id, w, opts) })
			if err != nil {
				return err
			}
			return printFileResult(cmd.OutOrStdout(), cfg, outFile, sum, "Exported")
		},
	}
	cmd.Flags().StringVarP(&outFile, "output", "o", "", "Output file (default <id>-<version>.mpkg)")
	cmd.Flags().StringVar(&actions, "actions", "", "Only these actions (comma-separated) and what they reference")
	cmd.Flags().BoolVar(&withRecordings, "with-recordings", false, "Include recordings/ (real page content)")
	return cmd
}

// newAutomationLifecycleCmds builds uninstall/restore/enable/disable/rollback,
// which all print {"ok":true,"info":InstalledInfo|null}.
func newAutomationLifecycleCmds(cfg *globalConfig) []*cobra.Command {
	type op struct {
		use, short, done string
		run              func(*automation.Registry, string) error
	}
	ops := []op{
		{"uninstall", "Uninstall an automation (a built-in stays removed until restored)", "Uninstalled",
			func(r *automation.Registry, id string) error { return r.Uninstall(id) }},
		{"restore", "Reinstall a built-in automation from the copy shipped with this binary", "Restored",
			func(r *automation.Registry, id string) error { return r.Restore(id, builtinAutomations()) }},
		{"enable", "Enable an installed automation", "Enabled",
			func(r *automation.Registry, id string) error { return r.SetEnabled(id, true) }},
		{"disable", "Disable an installed automation", "Disabled",
			func(r *automation.Registry, id string) error { return r.SetEnabled(id, false) }},
		{"rollback", "Switch an automation back to its previous installed version", "Rolled back",
			func(r *automation.Registry, id string) error { return r.Rollback(id) }},
	}
	var cmds []*cobra.Command
	for _, o := range ops {
		o := o
		cmds = append(cmds, &cobra.Command{
			Use:   o.use + " <id>",
			Short: o.short,
			Args:  cobra.ExactArgs(1),
			RunE: func(cmd *cobra.Command, args []string) error {
				reg, err := openAutomationRegistry()
				if err != nil {
					return err
				}
				id := args[0]
				if err := o.run(reg, id); err != nil {
					return err
				}
				info, err := reg.Info(id)
				if err != nil {
					info = nil // e.g. an uninstalled imported package is gone
				}
				out := cmd.OutOrStdout()
				if cfg.JSONOutput {
					return writeJSONTo(out, map[string]any{"ok": true, "info": info})
				}
				if info != nil {
					fmt.Fprintf(out, "%s %s (now %s)\n", o.done, id, info.Version)
				} else {
					fmt.Fprintf(out, "%s %s\n", o.done, id)
				}
				return nil
			},
		})
	}
	return cmds
}
