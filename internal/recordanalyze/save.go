package recordanalyze

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/monoes/mono-agent/internal/action"
	"github.com/monoes/mono-agent/internal/automation"
)

// Installer is the part of *automation.Registry save needs.
type Installer interface {
	AddAction(id string, src *automation.Package, actionName string, opts automation.InstallOptions) (*automation.InstallResult, error)
	Install(src string, opts automation.InstallOptions) (*automation.InstallResult, error)
	Get(id string) (*automation.Package, error)
}

// WorkflowNode is one node of a saved workflow draft (after the trigger).
type WorkflowNode struct {
	Name   string
	Type   string
	Config map[string]interface{}
}

// WorkflowCreator stores a linear workflow trigger → nodes… and returns its id.
type WorkflowCreator func(ctx context.Context, name, description string, nodes []WorkflowNode) (string, error)

// SaveOptions controls Save.
type SaveOptions struct {
	As         string // action (default) | fragment | workflow
	Automation string // existing automation id
	New        string // new automation id
	Name       string // action / fragment name override
	// RenameInputs renames action inputs (old → new) before saving.
	RenameInputs   map[string]string
	CreateWorkflow WorkflowCreator
	// LinkRecording records the saved automation on the recording; nil
	// adds a warning instead.
	LinkRecording func(recordingID, automationID string) error
}

// SaveResult is `record save --json`.
type SaveResult struct {
	Automation string   `json:"automation"`
	Action     string   `json:"action"`
	Version    string   `json:"version"`
	NodeType   string   `json:"nodeType"`
	WorkflowID string   `json:"workflowId"`
	Actions    []string `json:"actions,omitempty"` // workflow: one per segment
	Warnings   []string `json:"warnings,omitempty"`
}

// Save installs the draft in dir into the registry. It deletes nothing:
// the draft stays for another save or verify.
func Save(ctx context.Context, reg Installer, dir string, opts SaveOptions) (*SaveResult, error) {
	d, err := ReadDraft(dir)
	if err != nil {
		return nil, err
	}
	as := opts.As
	if as == "" {
		as = SaveAsAction
	}
	id := d.TargetAutomation
	switch {
	case opts.New != "" && opts.Automation != "":
		return nil, errors.New("use either --automation or --new, not both")
	case opts.New != "":
		if !ValidAutomationID(opts.New) {
			return nil, fmt.Errorf("invalid automation id %q (lowercase letters, digits and dashes)", opts.New)
		}
		if p, err := reg.Get(opts.New); err == nil && p != nil {
			return nil, fmt.Errorf("automation %s already exists; use --automation %s", opts.New, opts.New)
		}
		id = opts.New
	case opts.Automation != "":
		id = opts.Automation
	}
	name := d.Action
	if as == SaveAsFragment {
		name = firstNonEmpty(d.Names.Fragment, d.Action)
	}
	if opts.Name != "" {
		name = ActionSlug(opts.Name)
	}

	staging, err := os.MkdirTemp(filepath.Dir(dir), ".save-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(staging)
	stage := filepath.Join(staging, "pkg")
	if err := os.CopyFS(stage, os.DirFS(dir)); err != nil {
		return nil, fmt.Errorf("stage draft: %w", err)
	}
	_ = os.Remove(filepath.Join(stage, DraftFile))
	if err := renameStaged(stage, d.Action, opts.RenameInputs, d); err != nil {
		return nil, err
	}

	var res *SaveResult
	switch as {
	case SaveAsAction:
		res, err = saveAction(reg, stage, id, d.Action, name)
	case SaveAsFragment:
		res, err = saveFragment(reg, staging, stage, id, d.Action, name)
	case SaveAsWorkflow:
		res, err = saveWorkflow(ctx, reg, stage, id, d, name, opts.CreateWorkflow)
	default:
		return nil, fmt.Errorf("--as must be action, fragment or workflow (got %q)", as)
	}
	if err != nil {
		return nil, err
	}
	if opts.LinkRecording != nil {
		if err := opts.LinkRecording(d.RecordingID, res.Automation); err != nil {
			res.Warnings = append(res.Warnings, "could not link the recording: "+err.Error())
		}
	} else {
		res.Warnings = append(res.Warnings, "the recording was not linked to the automation (no recording link available)")
	}
	return res, nil
}

// retarget rewrites the staged manifest id and renames action from → to.
func retarget(stage, id, from, to string) (*automation.Manifest, *action.ActionDef, error) {
	var m automation.Manifest
	if err := readJSON(filepath.Join(stage, "automation.json"), &m); err != nil {
		return nil, nil, err
	}
	var def action.ActionDef
	src := filepath.Join(stage, "actions", from+".json")
	if err := readJSON(src, &def); err != nil {
		return nil, nil, err
	}
	m.ID, def.Automation, def.ActionType = id, id, to
	for i, a := range m.Actions {
		if a == from {
			m.Actions[i] = to
		}
	}
	if from != to {
		if err := os.Remove(src); err != nil {
			return nil, nil, err
		}
	}
	if err := writeJSON(filepath.Join(stage, "actions", to+".json"), &def); err != nil {
		return nil, nil, err
	}
	return &m, &def, writeJSON(filepath.Join(stage, "automation.json"), &m)
}

func saveAction(reg Installer, stage, id, from, name string) (*SaveResult, error) {
	if _, _, err := retarget(stage, id, from, name); err != nil {
		return nil, err
	}
	p, err := automation.OpenDir(stage)
	if err != nil {
		return nil, err
	}
	ir, err := reg.AddAction(id, p, name, automation.InstallOptions{})
	if err != nil {
		return nil, installErr(err, ir)
	}
	return &SaveResult{Automation: ir.ID, Action: name, Version: ir.Version, NodeType: ir.ID + "." + name, Warnings: ir.Warnings}, nil
}

func installErr(err error, ir *automation.InstallResult) error {
	if ir == nil || len(ir.Issues) == 0 {
		return err
	}
	var b strings.Builder
	b.WriteString(err.Error())
	for _, is := range ir.Issues {
		if is.Severity == "error" {
			fmt.Fprintf(&b, "\n- %s %s: %s", is.File, is.Code, is.Message)
		}
	}
	return errors.New(b.String())
}

func readJSON(path string, v any) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(b, v); err != nil {
		return fmt.Errorf("%s: %w", filepath.Base(path), err)
	}
	return nil
}

func writeJSON(path string, v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return writeFile(path, append(b, '\n'))
}

// bumpPatch returns v with its patch number incremented ("0.1.0" when v is
// not x.y.z).
func bumpPatch(v string) string {
	parts := strings.Split(strings.TrimPrefix(v, "v"), ".")
	if len(parts) != 3 {
		return "0.1.0"
	}
	n, err := strconv.Atoi(parts[2])
	if err != nil {
		return "0.1.0"
	}
	return parts[0] + "." + parts[1] + "." + strconv.Itoa(n+1)
}
