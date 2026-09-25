package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/monoes/mono-agent/internal/action"
	"github.com/monoes/mono-agent/internal/automation"
	"github.com/spf13/cobra"
)

// Authoring commands: validate, pack (new is in automation_new.go, test in
// automation_fixtures.go).

func newAutomationValidateCmd(cfg *globalConfig) *cobra.Command {
	var builtin bool
	cmd := &cobra.Command{
		Use:   "validate <dir|file.mpkg|action.json>",
		Short: "Validate a package directory, a .mpkg file or a single action file",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			issues, err := validateTarget(args[0], builtin)
			if err != nil {
				return err
			}
			ok := !issuesHaveErrors(issues)
			out := cmd.OutOrStdout()
			if cfg.JSONOutput {
				return writeJSONTo(out, map[string]any{"ok": ok, "issues": issues})
			}
			printIssues(out, issues)
			if ok {
				fmt.Fprintf(out, "OK: %s is valid\n", args[0])
				return nil
			}
			return fmt.Errorf("%s has validation errors", args[0])
		},
	}
	cmd.Flags().BoolVar(&builtin, "builtin", false, "Validate as a built-in package (automatic under data/automations/)")
	return cmd
}

// validateTarget validates whatever path points at. Always returns a
// non-nil slice so --json prints "issues": []. A package is checked as a
// built-in (no imported-package rules) with builtin or when it lives under
// data/automations/.
func validateTarget(target string, builtin bool) ([]automation.IssueJSON, error) {
	st, err := os.Stat(target)
	if err != nil {
		return nil, err
	}
	var pkg *automation.Package
	switch {
	case st.IsDir():
		pkg, err = automation.OpenDir(target)
	case strings.EqualFold(filepath.Ext(target), ".json"):
		return validateActionFile(target)
	default:
		pkg, err = automation.OpenFile(target)
	}
	if err != nil {
		return nil, err
	}
	if builtin || isBuiltinSourceDir(target) {
		pkg.Source = automation.SourceBuiltin
	}
	issues := automation.Validate(pkg)
	if issues == nil {
		issues = []automation.IssueJSON{}
	}
	return issues, nil
}

// isBuiltinSourceDir reports whether dir is a package of the shipped seed
// set: data/automations/<id> in a source checkout.
func isBuiltinSourceDir(dir string) bool {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return false
	}
	return filepath.Base(filepath.Dir(abs)) == "automations" &&
		filepath.Base(filepath.Dir(filepath.Dir(abs))) == "data"
}

// validateActionFile lints one loose action JSON with no package context.
func validateActionFile(file string) ([]automation.IssueJSON, error) {
	raw, err := os.ReadFile(file)
	if err != nil {
		return nil, err
	}
	var def action.ActionDef
	if err := json.Unmarshal(raw, &def); err != nil {
		return []automation.IssueJSON{{File: filepath.Base(file), Severity: "error",
			Code: "invalid_json", Message: err.Error()}}, nil
	}
	out := []automation.IssueJSON{}
	for _, is := range action.Validate(&def, nil) {
		out = append(out, automation.IssueJSON{File: filepath.Base(file), Severity: is.Severity,
			StepID: is.StepID, Code: is.Code, Message: is.Message})
	}
	return out, nil
}

func issuesHaveErrors(issues []automation.IssueJSON) bool {
	for _, is := range issues {
		if is.Severity == "error" {
			return true
		}
	}
	return false
}

func newAutomationPackCmd(cfg *globalConfig) *cobra.Command {
	var outFile string
	cmd := &cobra.Command{
		Use:   "pack <dir>",
		Short: "Pack a package directory into a .mpkg file",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			dir := args[0]
			pkg, err := automation.OpenDir(dir)
			if err != nil {
				return err
			}
			if issues := automation.Validate(pkg); issuesHaveErrors(issues) {
				if !cfg.JSONOutput {
					printIssues(cmd.ErrOrStderr(), issues)
				}
				return fmt.Errorf("%s has validation errors; fix them before packing", dir)
			}
			if outFile == "" {
				outFile = fmt.Sprintf("%s-%s.mpkg", pkg.Manifest.ID, pkg.Manifest.Version)
			}
			sum, err := writeHashed(outFile, func(w io.Writer) error { return automation.Pack(dir, w) })
			if err != nil {
				return err
			}
			return printFileResult(cmd.OutOrStdout(), cfg, outFile, sum, "Packed")
		},
	}
	cmd.Flags().StringVarP(&outFile, "output", "o", "", "Output file (default <id>-<version>.mpkg)")
	return cmd
}

// writeHashed creates file, streams write into it and returns the sha256 of
// what was written. A failed write removes the partial file.
func writeHashed(file string, write func(io.Writer) error) (string, error) {
	f, err := os.Create(file)
	if err != nil {
		return "", err
	}
	h := sha256.New()
	werr := write(io.MultiWriter(f, h))
	cerr := f.Close()
	if werr == nil {
		werr = cerr
	}
	if werr != nil {
		os.Remove(file)
		return "", werr
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// printFileResult prints {"file","sha256"} (contracts §5) or a line.
func printFileResult(out io.Writer, cfg *globalConfig, file, sum, verb string) error {
	abs, err := filepath.Abs(file)
	if err != nil {
		abs = file
	}
	if cfg.JSONOutput {
		return writeJSONTo(out, map[string]string{"file": abs, "sha256": sum})
	}
	fmt.Fprintf(out, "%s: %s\nsha256: %s\n", verb, abs, sum)
	return nil
}
