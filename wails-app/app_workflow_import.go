package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/wailsapp/wails/v2/pkg/runtime"
)

// ─────────────────────────────────────────────────────────────────────────────
// Workflow import screen
//
// ImportWorkflowFull is the binding behind the Workflows page's "Import
// workflow" dialog. Like ImportWorkflow (kept for its callers) it shells out
// to `monoagentcli workflow import --json`, but it returns the CLI's JSON
// verbatim — status, bundled automations with their per-package status,
// missingAutomations, installCommand, remapped ids — so the dialog renders
// what the CLI decided instead of a trimmed struct. The only addition is
// "reviewLines": the per-package review lines the CLI prints on stderr while
// installing bundled automations (--yes), which --json keeps off stdout.
// ─────────────────────────────────────────────────────────────────────────────

// workflowImportTimeout covers installing bundled automation packages.
const workflowImportTimeout = 3 * time.Minute

// WorkflowImportOptions is ImportWorkflowFull's JSON argument.
type WorkflowImportOptions struct {
	AsNew bool `json:"asNew"` // --as-new: always create a new workflow
	Yes   bool `json:"yes"`   // --yes: install bundled automations that are missing
}

// workflowImportArgs builds `[--profile P] --json workflow import [flags]`.
// A file path goes to --file (so a re-import of the same file is found by
// its source); pasted JSON goes on stdin (file "").
func workflowImportArgs(profileID, file string, o WorkflowImportOptions) []string {
	args := []string{}
	if profileID != "" {
		args = append(args, "--profile", profileID)
	}
	args = append(args, "--json", "workflow", "import")
	if file != "" {
		args = append(args, "--file", file)
	}
	if o.AsNew {
		args = append(args, "--as-new")
	}
	if o.Yes {
		args = append(args, "--yes")
	}
	return args
}

// bundleReviewLines picks the CLI's per-package review lines out of stderr.
func bundleReviewLines(stderr []byte) []string {
	var lines []string
	sc := bufio.NewScanner(bytes.NewReader(stderr))
	for sc.Scan() {
		if line := strings.TrimSpace(sc.Text()); strings.HasPrefix(line, "Bundled automation ") {
			lines = append(lines, line)
		}
	}
	return lines
}

// withReviewLines adds "reviewLines" to the CLI's JSON object. Output that
// is not a JSON object is returned unchanged.
func withReviewLines(stdout string, lines []string) string {
	if len(lines) == 0 {
		return stdout
	}
	var obj map[string]json.RawMessage
	if json.Unmarshal([]byte(stdout), &obj) != nil {
		return stdout
	}
	b, _ := json.Marshal(lines)
	obj["reviewLines"] = b
	out, err := json.Marshal(obj)
	if err != nil {
		return stdout
	}
	return string(out)
}

// ImportWorkflowFull imports a workflow from a path to a JSON file or from
// pasted workflow JSON; optsJSON is a WorkflowImportOptions. Returns the
// CLI's JSON, or {"error": "..."}.
func (a *App) ImportWorkflowFull(input, optsJSON string) string {
	input = strings.TrimSpace(input)
	if input == "" {
		return aiError(fmt.Errorf("choose a workflow file or paste workflow JSON"))
	}
	var opts WorkflowImportOptions
	if strings.TrimSpace(optsJSON) != "" {
		if err := json.Unmarshal([]byte(optsJSON), &opts); err != nil {
			return aiError(fmt.Errorf("import options: %w", err))
		}
	}
	file, stdin := "", []byte(nil)
	if fileExists(input) {
		file = input
	} else if json.Valid([]byte(input)) {
		stdin = []byte(input)
	} else {
		return aiError(fmt.Errorf("that is neither an existing file nor valid workflow JSON"))
	}

	cliBin, err := findMonoAgentCLI()
	if err != nil {
		return aiError(err)
	}
	parent := a.ctx
	if parent == nil {
		parent = context.Background()
	}
	ctx, cancel := context.WithTimeout(parent, workflowImportTimeout)
	defer cancel()
	args := workflowImportArgs(a.getActiveProfileID(), file, opts)
	a.emitLog("WORKFLOW", "INFO", fmt.Sprintf("$ %s %s", cliBin, strings.Join(args, " ")))
	cmd := exec.CommandContext(ctx, cliBin, args...)
	hideWindow(cmd)
	if stdin != nil {
		cmd.Stdin = bytes.NewReader(stdin)
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	runErr := cmd.Run()
	if runErr != nil {
		if ee, ok := runErr.(*exec.ExitError); ok && len(ee.Stderr) == 0 {
			ee.Stderr = stderr.Bytes()
		}
	}
	res := cliResultJSON(cliBin, stdout.Bytes(), runErr)
	if runErr == nil {
		res = withReviewLines(res, bundleReviewLines(stderr.Bytes()))
		a.emitLog("WORKFLOW", "INFO", "workflow import finished")
	} else {
		a.emitLog("WORKFLOW", "ERROR", "workflow import failed: "+res)
	}
	return res
}

// ChooseWorkflowFile opens a native picker for a workflow .json file and
// returns its path ("" when cancelled).
func (a *App) ChooseWorkflowFile() string {
	path, err := runtime.OpenFileDialog(a.ctx, runtime.OpenDialogOptions{
		Title:   "Import workflow",
		Filters: []runtime.FileFilter{{DisplayName: "Workflow JSON", Pattern: "*.json"}},
	})
	if err != nil {
		return ""
	}
	return path
}
