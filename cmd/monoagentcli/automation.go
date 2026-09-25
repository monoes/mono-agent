package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"strings"

	"github.com/monoes/mono-agent/data"
	"github.com/monoes/mono-agent/internal/automation"
	"github.com/spf13/cobra"
)

// newAutomationCmd builds `monoagentcli automation …` (browser automation
// packages). Owned by the cli builder; see the build contracts doc.
// JSON shapes are fixed by contracts §5 — the GUI parses them as-is.
func newAutomationCmd(cfg *globalConfig) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "automation",
		Short: "Manage browser automation packages",
		Long: `Browser automation packages: a manifest, actions, fragments, selectors and
optional page scripts for one site. Built-ins ship with the binary and are
seeded into ~/.monoagent/automations; everything else is installed from a
.mpkg file, a package directory or a URL.`,
	}
	cmd.AddCommand(
		newAutomationListCmd(cfg),
		newAutomationShowCmd(cfg),
		newAutomationNewCmd(cfg),
		newAutomationValidateCmd(cfg),
		newAutomationTestCmd(cfg),
		newAutomationPackCmd(cfg),
		newAutomationInstallCmd(cfg),
		newAutomationExportCmd(cfg),
		newAutomationDoctorCmd(cfg), // automation_doctor.go (health builder)
		newAutomationTrustCmd(cfg),
	)
	cmd.AddCommand(newAutomationLifecycleCmds(cfg)...)
	for _, sub := range cmd.Commands() {
		withJSONErrors(cfg, sub)
	}
	return cmd
}

// withJSONErrors makes a failing command also print {"error": "…"} on
// stdout when --json is set (contracts §5). main still prints the error to
// stderr and exits non-zero.
func withJSONErrors(cfg *globalConfig, cmd *cobra.Command) {
	run := cmd.RunE
	if run == nil {
		return
	}
	cmd.RunE = func(c *cobra.Command, args []string) error {
		err := run(c, args)
		if err != nil && cfg.JSONOutput {
			body := map[string]any{"error": err.Error()}
			var re *installResultError
			if errors.As(err, &re) {
				body["issues"], body["result"] = re.res.Issues, re.res
			}
			b, _ := json.Marshal(body)
			fmt.Fprintln(c.OutOrStdout(), string(b))
		}
		return err
	}
}

// installResultError is an Install/AddAction failure that still carries the
// result (review + issues). With --json the error object adds "issues" and
// "result"; without, the issues are printed on stderr.
type installResultError struct {
	err error
	res *automation.InstallResult
}

func (e *installResultError) Error() string { return e.err.Error() }
func (e *installResultError) Unwrap() error { return e.err }

// withInstallResult wraps err so the failed result is not lost.
func withInstallResult(err error, res *automation.InstallResult) error {
	if err == nil || res == nil {
		return err
	}
	return &installResultError{err: err, res: res}
}

// builtinAutomations is the embedded seed set rooted at the package dirs.
func builtinAutomations() fs.FS {
	sub, err := fs.Sub(data.AutomationsFS, "automations")
	if err != nil {
		panic(err) // embed layout is fixed at compile time
	}
	return sub
}

// openAutomationRegistry opens the default registry and seeds the embedded
// built-ins (cheap when nothing changed). Every automation subcommand starts
// here so the installed set is never older than the binary.
func openAutomationRegistry() (*automation.Registry, error) {
	if version != "" { // release build: enforce manifests' "engine" ranges
		automation.EngineVersion = version
	}
	reg, err := automation.Default()
	if err != nil {
		return nil, fmt.Errorf("open automation registry: %w", err)
	}
	rep, err := reg.SeedWithReport(builtinAutomations())
	if err != nil {
		return nil, fmt.Errorf("seed built-in automations: %w", err)
	}
	for _, id := range rep.LegacyWrapped {
		stderrf("note: wrapped legacy actions into automation package %s (~/.monoagent/actions is left as is)\n", id)
	}
	for _, s := range rep.Skipped {
		stderrf("warning: built-in automation skipped: %s\n", s)
	}
	return reg, nil
}

// writeJSONTo writes v as indented JSON to w.
func writeJSONTo(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

// confirmYes asks a y/N question on in; anything but y/yes is a no.
func confirmYes(in io.Reader, out io.Writer, question string) bool {
	fmt.Fprintf(out, "%s [y/N]: ", question)
	line, _ := bufio.NewReader(in).ReadString('\n')
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "y", "yes":
		return true
	}
	return false
}

// splitCSV splits a comma-separated flag value, dropping empty items.
func splitCSV(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// stderrf prints a note on stderr (kept off stdout so --json stays parseable).
func stderrf(format string, a ...any) {
	fmt.Fprintf(os.Stderr, format, a...)
}
