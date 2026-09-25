package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"sort"
	"strings"
	"time"

	"github.com/wailsapp/wails/v2/pkg/runtime"
)

// ─────────────────────────────────────────────────────────────────────────────
// Browser automation packages and recordings (Connections page)
//
// Same doctrine as app_org_unification.go: this file imports nothing from
// internal/. Every binding shells out to `monoagentcli --profile <p> --json
// automation|action|record …` and returns its stdout verbatim — the CLI JSON
// in the build contracts §5 is the UI contract. Failures come back in the
// {"error":"…"} shape (cliResultJSON), so the frontend has one check.
// ─────────────────────────────────────────────────────────────────────────────

// automationCLITimeout bounds list/show/install/lifecycle calls.
const automationCLITimeout = 2 * time.Minute

// automationLongCLITimeout bounds calls that drive the browser or the AI
// runner (analyze, verify, test, doctor).
const automationLongCLITimeout = 10 * time.Minute

// automationCLIArgs builds the full argv: global flags first, then the
// subcommand words.
func automationCLIArgs(profileID string, sub ...string) []string {
	full := []string{}
	if profileID != "" {
		full = append(full, "--profile", profileID)
	}
	return append(append(full, "--json"), sub...)
}

func requireArg(name, v string) error {
	if strings.TrimSpace(v) == "" {
		return fmt.Errorf("%s required", name)
	}
	return nil
}

// automationInstallArgs builds `automation install <src> --dry-run|--yes`.
// The GUI shows the review from the dry run, then confirms with --yes.
func automationInstallArgs(path string, dryRun bool) ([]string, error) {
	if err := requireArg("package path", path); err != nil {
		return nil, err
	}
	args := []string{"automation", "install", path}
	if dryRun {
		return append(args, "--dry-run"), nil
	}
	return append(args, "--yes"), nil
}

// automationLifecycleArgs builds `automation <verb> <id>` for the footer
// buttons (uninstall, restore, enable, disable, rollback).
func automationLifecycleArgs(verb, id string) ([]string, error) {
	switch verb {
	case "uninstall", "restore", "enable", "disable", "rollback":
	default:
		return nil, fmt.Errorf("unknown automation command %q", verb)
	}
	if err := requireArg("automation id", id); err != nil {
		return nil, err
	}
	return []string{"automation", verb, id}, nil
}

func automationTestArgs(id, action string, live bool) ([]string, error) {
	if err := requireArg("automation id", id); err != nil {
		return nil, err
	}
	args := []string{"automation", "test", id}
	if action != "" {
		args = append(args, action)
	}
	if live {
		args = append(args, "--live")
	}
	return args, nil
}

func automationExportArgs(id, path string, withRecordings bool) ([]string, error) {
	if err := requireArg("automation id", id); err != nil {
		return nil, err
	}
	if err := requireArg("output path", path); err != nil {
		return nil, err
	}
	args := []string{"automation", "export", id, "-o", path}
	if withRecordings {
		args = append(args, "--with-recordings")
	}
	return args, nil
}

func actionExportArgs(ref, path string) ([]string, error) {
	if !strings.Contains(ref, ".") {
		return nil, fmt.Errorf("action reference must be <automation>.<action>, got %q", ref)
	}
	if err := requireArg("output path", path); err != nil {
		return nil, err
	}
	return []string{"action", "export", ref, "-o", path}, nil
}

func recordVerifyArgs(draftDir string, full bool, inputs map[string]string) ([]string, error) {
	if err := requireArg("draft directory", draftDir); err != nil {
		return nil, err
	}
	args := []string{"record", "verify", draftDir}
	if full {
		args = append(args, "--full")
	}
	for _, name := range sortedKeys(inputs) {
		if inputs[name] != "" {
			args = append(args, "--input", name+"="+inputs[name])
		}
	}
	return args, nil
}

// SaveDraftSpec is SaveDraft's JSON argument (one JSON string crosses the
// Wails boundary instead of a long positional list).
type SaveDraftSpec struct {
	As           string            `json:"as"`           // action | fragment | workflow
	Automation   string            `json:"automation"`   // existing target package
	New          string            `json:"new"`          // new package id (wins over Automation)
	Name         string            `json:"name"`         // action / fragment name
	RenameInputs map[string]string `json:"renameInputs"` // AI name → user name, changed ones only
}

func recordSaveArgs(draftDir string, s SaveDraftSpec) ([]string, error) {
	if err := requireArg("draft directory", draftDir); err != nil {
		return nil, err
	}
	args := []string{"record", "save", draftDir}
	switch s.As {
	case "":
	case "action", "fragment", "workflow":
		args = append(args, "--as", s.As)
	default:
		return nil, fmt.Errorf("save as must be action, fragment or workflow, got %q", s.As)
	}
	if s.New != "" {
		args = append(args, "--new", s.New)
	} else if s.Automation != "" {
		args = append(args, "--automation", s.Automation)
	}
	if s.Name != "" {
		args = append(args, "--name", s.Name)
	}
	for _, from := range sortedKeys(s.RenameInputs) {
		to := s.RenameInputs[from]
		if to != "" && to != from {
			args = append(args, "--rename-input", from+"="+to)
		}
	}
	return args, nil
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// ── Execution ──────────────────────────────────────────────────────────────

// runAutomationCLI runs one monoagentcli call with the active profile and
// returns the frontend JSON string.
func (a *App) runAutomationCLI(timeout time.Duration, sub []string, buildErr error) string {
	if buildErr != nil {
		return aiError(buildErr)
	}
	cliBin, err := findMonoAgentCLI()
	if err != nil {
		return aiError(err)
	}
	parent := a.ctx
	if parent == nil {
		parent = context.Background()
	}
	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()
	fullArgs := automationCLIArgs(a.getActiveProfileID(), sub...)
	label := strings.Join(sub, " ")
	a.emitLog("AUTOMATION", "INFO", fmt.Sprintf("$ %s %s", cliBin, strings.Join(fullArgs, " ")))
	startedAt := time.Now()
	cmd := exec.CommandContext(ctx, cliBin, fullArgs...)
	hideWindow(cmd)
	out, runErr := cmd.Output()
	elapsed := time.Since(startedAt).Round(time.Millisecond)
	res := cliResultJSON(cliBin, out, runErr)
	if runErr != nil {
		a.emitLog("AUTOMATION", "ERROR", fmt.Sprintf("%s failed after %s: %s", label, elapsed, res))
	} else {
		a.emitLog("AUTOMATION", "INFO", fmt.Sprintf("%s finished in %s", label, elapsed))
	}
	return res
}

func (a *App) runAutomation(sub ...string) string {
	return a.runAutomationCLI(automationCLITimeout, sub, nil)
}

// ── Bindings: packages (contracts §5) ──────────────────────────────────────

func (a *App) ListAutomations() string {
	return a.runAutomation("automation", "list")
}

func (a *App) ShowAutomation(id string) string {
	return a.runAutomationCLI(automationCLITimeout, []string{"automation", "show", id}, requireArg("automation id", id))
}

func (a *App) InstallAutomationDryRun(path string) string {
	args, err := automationInstallArgs(path, true)
	return a.runAutomationCLI(automationCLITimeout, args, err)
}

func (a *App) InstallAutomation(path string) string {
	args, err := automationInstallArgs(path, false)
	return a.runAutomationCLI(automationCLITimeout, args, err)
}

func (a *App) ExportAutomation(id, path string) string {
	args, err := automationExportArgs(id, path, false)
	return a.runAutomationCLI(automationCLITimeout, args, err)
}

func (a *App) ExportAction(ref, path string) string {
	args, err := actionExportArgs(ref, path)
	return a.runAutomationCLI(automationCLITimeout, args, err)
}

func (a *App) UninstallAutomation(id string) string { return a.automationLifecycle("uninstall", id) }
func (a *App) RestoreAutomation(id string) string   { return a.automationLifecycle("restore", id) }
func (a *App) EnableAutomation(id string) string    { return a.automationLifecycle("enable", id) }
func (a *App) DisableAutomation(id string) string   { return a.automationLifecycle("disable", id) }
func (a *App) RollbackAutomation(id string) string  { return a.automationLifecycle("rollback", id) }

func (a *App) automationLifecycle(verb, id string) string {
	args, err := automationLifecycleArgs(verb, id)
	return a.runAutomationCLI(automationCLITimeout, args, err)
}

// ValidateAutomation validates a package directory or action file.
func (a *App) ValidateAutomation(path string) string {
	return a.runAutomationCLI(automationCLITimeout, []string{"automation", "validate", path}, requireArg("path", path))
}

// TestAutomation runs fixture tests (or live ones) for a package or one action.
func (a *App) TestAutomation(id, action string, live bool) string {
	args, err := automationTestArgs(id, action, live)
	return a.runAutomationCLI(automationLongCLITimeout, args, err)
}

// DoctorAutomations reports selector health and issues; id "" = all.
func (a *App) DoctorAutomations(id string) string {
	sub := []string{"automation", "doctor"}
	if id != "" {
		sub = append(sub, id)
	}
	return a.runAutomationCLI(automationLongCLITimeout, sub, nil)
}

// ── Bindings: recordings ───────────────────────────────────────────────────

func (a *App) ListRecordings() string {
	return a.runAutomation("record", "list")
}

func (a *App) ShowRecording(id string) string {
	return a.runAutomationCLI(automationCLITimeout, []string{"record", "show", id}, requireArg("recording id", id))
}

func (a *App) DeleteRecording(id string) string {
	return a.runAutomationCLI(automationCLITimeout, []string{"record", "delete", id}, requireArg("recording id", id))
}

// AnalyzeRecording turns a recording into a draft; automation "" lets the
// analyzer propose a target (or a new package).
func (a *App) AnalyzeRecording(id, automation string) string {
	sub := []string{"record", "analyze", id}
	if automation != "" {
		sub = append(sub, "--automation", automation)
	}
	return a.runAutomationCLI(automationLongCLITimeout, sub, requireArg("recording id", id))
}

// VerifyDraft replays a draft; full=false is safe mode (stops before the
// first side-effecting step). inputsJSON is an optional {name: value}
// object for inputs the recording could not hold (secrets are never
// recorded), passed as --input name=value.
func (a *App) VerifyDraft(draftDir string, full bool, inputsJSON string) string {
	var inputs map[string]string
	if strings.TrimSpace(inputsJSON) != "" {
		if err := json.Unmarshal([]byte(inputsJSON), &inputs); err != nil {
			return aiError(fmt.Errorf("verify inputs: %w", err))
		}
	}
	args, err := recordVerifyArgs(draftDir, full, inputs)
	return a.runAutomationCLI(automationLongCLITimeout, args, err)
}

// SaveDraft saves a verified draft; specJSON is a SaveDraftSpec.
func (a *App) SaveDraft(draftDir, specJSON string) string {
	var spec SaveDraftSpec
	if strings.TrimSpace(specJSON) != "" {
		if err := json.Unmarshal([]byte(specJSON), &spec); err != nil {
			return aiError(fmt.Errorf("save spec: %w", err))
		}
	}
	args, err := recordSaveArgs(draftDir, spec)
	return a.runAutomationCLI(automationCLITimeout, args, err)
}

// ── File dialogs ───────────────────────────────────────────────────────────

// ChooseAutomationPackage opens a native picker for a .mpkg file and
// returns its path ("" when cancelled).
func (a *App) ChooseAutomationPackage() string {
	path, err := runtime.OpenFileDialog(a.ctx, runtime.OpenDialogOptions{
		Title:   "Import automation",
		Filters: []runtime.FileFilter{{DisplayName: "Automation package", Pattern: "*.mpkg"}},
	})
	if err != nil {
		return ""
	}
	return path
}

// ChooseAutomationExportPath opens a native save dialog for a .mpkg file
// and returns its path ("" when cancelled).
func (a *App) ChooseAutomationExportPath(defaultName string) string {
	if defaultName == "" {
		defaultName = "automation.mpkg"
	}
	path, err := runtime.SaveFileDialog(a.ctx, runtime.SaveDialogOptions{
		Title:           "Export automation",
		DefaultFilename: defaultName,
		Filters:         []runtime.FileFilter{{DisplayName: "Automation package", Pattern: "*.mpkg"}},
	})
	if err != nil {
		return ""
	}
	return path
}
