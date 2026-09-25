package recordanalyze

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/monoes/mono-agent/internal/action"
	"github.com/monoes/mono-agent/internal/automation"
)

// saveFragment merges the draft action, as a fragment, into a copy of the
// target package (or a new package) and installs that copy.
func saveFragment(reg Installer, staging, stage, id, from, name string) (*SaveResult, error) {
	var def action.ActionDef
	if err := readJSON(filepath.Join(stage, "actions", from+".json"), &def); err != nil {
		return nil, err
	}
	var draftM automation.Manifest
	if err := readJSON(filepath.Join(stage, "automation.json"), &draftM); err != nil {
		return nil, err
	}
	target := filepath.Join(staging, "target")
	var m automation.Manifest
	source := automation.SourceLocal
	if p, err := reg.Get(id); err == nil && p != nil {
		if p.Source != "" {
			source = p.Source
		}
		if err := os.CopyFS(target, p.FS); err != nil {
			return nil, fmt.Errorf("copy %s: %w", id, err)
		}
		if err := readJSON(filepath.Join(target, "automation.json"), &m); err != nil {
			return nil, err
		}
		m.Version = bumpPatch(m.Version)
		if len(m.Permissions.Steps) > 0 {
			m.Permissions.Steps = union(m.Permissions.Steps, draftM.Permissions.Steps)
		}
		if len(m.Site.Domains) > 0 {
			m.Site.Domains = union(m.Site.Domains, draftM.Site.Domains)
		}
		m.Permissions.Scripts = union(m.Permissions.Scripts, draftM.Permissions.Scripts)
	} else {
		m = draftM
		m.ID, m.Actions = id, []string{}
		if m.Version == "" {
			m.Version = "0.1.0"
		}
	}

	exists := func(rel string) bool {
		_, err := os.Stat(filepath.Join(target, rel))
		return err == nil
	}
	name = Unique(name, "_", func(n string) bool { return exists("fragments/" + n + ".json") })
	frag := action.FragmentDef{Name: name, Description: def.Description, Inputs: def.Inputs, Steps: def.Steps}
	if err := writeJSON(filepath.Join(target, "fragments", name+".json"), &frag); err != nil {
		return nil, err
	}
	for _, dir := range []string{"fragments", "scripts"} {
		entries, _ := os.ReadDir(filepath.Join(stage, dir))
		for _, e := range entries {
			rel := dir + "/" + e.Name()
			if e.IsDir() || exists(rel) {
				continue
			}
			b, err := os.ReadFile(filepath.Join(stage, rel))
			if err != nil {
				return nil, err
			}
			if err := writeFile(filepath.Join(target, rel), b); err != nil {
				return nil, err
			}
		}
	}
	sels := map[string]action.SelectorEntry{}
	_ = readJSON(filepath.Join(target, "selectors.json"), &sels)
	draftSels := map[string]action.SelectorEntry{}
	_ = readJSON(filepath.Join(stage, "selectors.json"), &draftSels)
	for k, v := range draftSels {
		if _, ok := sels[k]; !ok {
			sels[k] = v
		}
	}
	if err := writeJSON(filepath.Join(target, "selectors.json"), sels); err != nil {
		return nil, err
	}
	if err := writeJSON(filepath.Join(target, "automation.json"), &m); err != nil {
		return nil, err
	}
	ir, err := reg.Install(target, automation.InstallOptions{Source: source})
	if err != nil {
		return nil, installErr(err, ir)
	}
	return &SaveResult{Automation: ir.ID, Action: name, Version: ir.Version, Warnings: ir.Warnings}, nil
}

// splitAtNavigations cuts steps into chunks, each starting at a top-level
// navigate (the first chunk also holds anything before the first one).
func splitAtNavigations(steps []action.StepDef) [][]action.StepDef {
	var chunks [][]action.StepDef
	for i, s := range steps {
		if len(chunks) == 0 || (s.Type == "navigate" && i > 0 && len(chunks[len(chunks)-1]) > 0 && hasNonNavigate(chunks[len(chunks)-1])) {
			chunks = append(chunks, nil)
		}
		chunks[len(chunks)-1] = append(chunks[len(chunks)-1], s)
	}
	return chunks
}

func hasNonNavigate(steps []action.StepDef) bool {
	for _, s := range steps {
		if s.Type != "navigate" {
			return true
		}
	}
	return false
}

// usedInputs keeps the input entries whose name the steps reference.
func usedInputs(in *action.InputDef, steps []action.StepDef) *action.InputDef {
	if in == nil {
		return nil
	}
	b, _ := json.Marshal(steps)
	body := string(b)
	keep := func(list []json.RawMessage) []json.RawMessage {
		var out []json.RawMessage
		for _, raw := range list {
			var e struct {
				Name string `json:"name"`
			}
			if json.Unmarshal(raw, &e) != nil {
				var s string
				if json.Unmarshal(raw, &s) != nil {
					continue
				}
				e.Name = s
			}
			if strings.Contains(body, "{{"+e.Name) || strings.Contains(body, "secret:"+e.Name+"}}") {
				out = append(out, raw)
			}
		}
		return out
	}
	return &action.InputDef{Required: keep(in.Required), Optional: keep(in.Optional)}
}

// saveWorkflow saves one action per recorded page phase and a workflow
// wiring them: trigger → part1 → part2 …
func saveWorkflow(ctx context.Context, reg Installer, stage, id string, d *Draft, name string, create WorkflowCreator) (*SaveResult, error) {
	if create == nil {
		return nil, errors.New("no workflow store available")
	}
	m, def, err := retarget(stage, id, d.Action, name)
	if err != nil {
		return nil, err
	}
	chunks := splitAtNavigations(def.Steps)
	parts := []string{name}
	if len(chunks) > 1 {
		parts = parts[:0]
		if err := os.Remove(filepath.Join(stage, "actions", name+".json")); err != nil {
			return nil, err
		}
		for i, steps := range chunks {
			part := *def
			part.ActionType = fmt.Sprintf("%s_part%d", name, i+1)
			part.Steps = steps
			part.Inputs = usedInputs(def.Inputs, steps)
			part.Description = fmt.Sprintf("%s (part %d of %d)", def.Description, i+1, len(chunks))
			if i < len(chunks)-1 {
				part.Outputs, part.OutputSchema = nil, nil
			}
			if err := writeJSON(filepath.Join(stage, "actions", part.ActionType+".json"), &part); err != nil {
				return nil, err
			}
			parts = append(parts, part.ActionType)
		}
		m.Actions = parts
		if err := writeJSON(filepath.Join(stage, "automation.json"), m); err != nil {
			return nil, err
		}
	}
	p, err := automation.OpenDir(stage)
	if err != nil {
		return nil, err
	}
	res := &SaveResult{Automation: id, Action: parts[0], Actions: parts}
	var nodes []WorkflowNode
	for _, part := range parts {
		ir, err := reg.AddAction(id, p, part, automation.InstallOptions{})
		if err != nil {
			return nil, installErr(err, ir)
		}
		res.Automation, res.Version = ir.ID, ir.Version
		res.Warnings = append(res.Warnings, ir.Warnings...)
		pdef, _ := p.Action(part)
		cfg := map[string]interface{}{}
		for k, v := range d.RecordedInputs {
			if pdef == nil || usedInputs(&action.InputDef{Required: []json.RawMessage{mustName(k)}}, pdef.Steps).Required != nil {
				cfg[k] = v
			}
		}
		nodes = append(nodes, WorkflowNode{Name: part, Type: ir.ID + "." + part, Config: cfg})
	}
	res.NodeType = nodes[0].Type
	title := firstNonEmpty(d.Goal, def.Description, name)
	wfID, err := create(ctx, title, "Recorded from "+d.RecordingID, nodes)
	if err != nil {
		return nil, fmt.Errorf("create workflow: %w", err)
	}
	res.WorkflowID = wfID
	return res, nil
}

func mustName(n string) json.RawMessage {
	b, _ := json.Marshal(map[string]string{"name": n})
	return b
}
