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

// saveFragment adds the draft action, as a fragment, to automation id
// (created when missing) through Registry.AddFragment, which keeps built-in
// lineage (<seed>+local.N), conflict checks and trust merging.
func saveFragment(reg Installer, stage, id, from, name string) (*SaveResult, error) {
	var def action.ActionDef
	if err := readJSON(filepath.Join(stage, "actions", from+".json"), &def); err != nil {
		return nil, err
	}
	var m automation.Manifest
	if err := readJSON(filepath.Join(stage, "automation.json"), &m); err != nil {
		return nil, err
	}
	var target *automation.Package
	if p, err := reg.Get(id); err == nil {
		target = p
	}
	name = Unique(name, "_", func(n string) bool {
		if _, err := os.Stat(filepath.Join(stage, "fragments", n+".json")); err == nil {
			return true
		}
		if target != nil {
			if _, err := target.Fragment(n); err == nil {
				return true
			}
		}
		return false
	})
	frag := action.FragmentDef{Name: name, Description: def.Description, Inputs: def.Inputs, Steps: def.Steps}
	if err := writeJSON(filepath.Join(stage, "fragments", name+".json"), &frag); err != nil {
		return nil, err
	}
	m.ID = id
	if err := writeJSON(filepath.Join(stage, "automation.json"), &m); err != nil {
		return nil, err
	}
	src, err := automation.OpenDir(stage)
	if err != nil {
		return nil, err
	}
	ir, err := reg.AddFragment(id, src, name, automation.InstallOptions{Trust: automation.TrustRecorded})
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
		ir, err := reg.AddAction(id, p, part, automation.InstallOptions{Trust: automation.TrustRecorded})
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
