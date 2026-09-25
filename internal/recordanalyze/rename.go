package recordanalyze

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/monoes/mono-agent/internal/action"
)

// RenameInputs renames action inputs (old → new) in def: the input entries
// and every template use — {{old}}, {{ old }}, {{old.x}}, {{old[0]}} and
// {{secret:old}} — in the steps and loops.
func RenameInputs(def *action.ActionDef, ren map[string]string) error {
	if len(ren) == 0 {
		return nil
	}
	names := map[string]bool{}
	if def.Inputs != nil {
		for _, group := range [][]json.RawMessage{def.Inputs.Required, def.Inputs.Optional} {
			for _, raw := range group {
				names[inputName(raw)] = true
			}
		}
	}
	olds := make([]string, 0, len(ren))
	for old := range ren {
		olds = append(olds, old)
	}
	sort.Strings(olds)
	for _, old := range olds {
		nw := ren[old]
		switch {
		case !names[old]:
			return fmt.Errorf("--rename-input: the action has no input %q", old)
		case !ValidInputName(nw):
			return fmt.Errorf("--rename-input: %q is not a valid input name", nw)
		case names[nw] && ren[nw] == "":
			return fmt.Errorf("--rename-input: the action already has an input %q", nw)
		}
	}
	if def.Inputs != nil {
		for _, group := range []*[]json.RawMessage{&def.Inputs.Required, &def.Inputs.Optional} {
			for i, raw := range *group {
				nw, ok := ren[inputName(raw)]
				if !ok {
					continue
				}
				var obj map[string]any
				if json.Unmarshal(raw, &obj) == nil {
					obj["name"] = nw
					(*group)[i], _ = json.Marshal(obj)
				} else {
					(*group)[i], _ = json.Marshal(nw)
				}
			}
		}
	}
	for i := range def.Steps {
		if err := renameInStep(&def.Steps[i], ren); err != nil {
			return err
		}
	}
	for i := range def.Loops {
		l := &def.Loops[i]
		l.Iterator = renamePath(l.Iterator, ren)
		l.MaxItems, l.MaxItemsPerDay = renameTemplates(l.MaxItems, ren), renameTemplates(l.MaxItemsPerDay, ren)
	}
	return nil
}

// RenameInFragment applies input renames to a draft fragment whose body
// uses the caller's variables.
func RenameInFragment(f *action.FragmentDef, ren map[string]string) error {
	for i := range f.Steps {
		if err := renameInStep(&f.Steps[i], ren); err != nil {
			return err
		}
	}
	return nil
}

// pathKeys hold a bare variable path (no {{ }}) rather than text.
var pathKeys = map[string]bool{"variable": true, "variable_name": true, "increment": true, "items": true, "input": true, "iterator": true}

// renameInStep rewrites one step (and its nested body) at reference level:
// every template string, and the bare paths of pathKeys, anywhere in it
// (condition, onSuccess, until, inputs, fields, set, transform ops, …).
func renameInStep(st *action.StepDef, ren map[string]string) error {
	b, err := json.Marshal(st)
	if err != nil {
		return err
	}
	var tree any
	if err := json.Unmarshal(b, &tree); err != nil {
		return err
	}
	tree = renameTree(tree, "", ren)
	b, err = json.Marshal(tree)
	if err != nil {
		return err
	}
	var out action.StepDef
	if err := json.Unmarshal(b, &out); err != nil {
		return err
	}
	*st = out
	return nil
}

func renameTree(v any, key string, ren map[string]string) any {
	switch t := v.(type) {
	case map[string]any:
		for k, c := range t {
			t[k] = renameTree(c, k, ren)
		}
		return t
	case []any:
		for i, c := range t {
			t[i] = renameTree(c, key, ren)
		}
		return t
	case string:
		if strings.Contains(t, "{{") {
			return renameTemplates(t, ren)
		}
		if pathKeys[key] {
			return renamePath(t, ren)
		}
		return t
	}
	return v
}

var (
	templateRe = regexp.MustCompile(`\{\{(.*?)\}\}`)
	orRe       = regexp.MustCompile(`\s+or\s+`)
)

// renameTemplates renames the head of every variable path inside {{ }}:
// alternatives split on " or " ({{q or 'x'}}), a "secret:" prefix kept,
// quoted and numeric literals left alone.
func renameTemplates(s string, ren map[string]string) string {
	return templateRe.ReplaceAllStringFunc(s, func(m string) string {
		inner := m[2 : len(m)-2]
		seps := orRe.FindAllString(inner, -1)
		parts := orRe.Split(inner, -1)
		var b strings.Builder
		for i, p := range parts {
			lead := p[:len(p)-len(strings.TrimLeft(p, " \t"))]
			trail := p[len(strings.TrimRight(p, " \t")):]
			core := strings.TrimSpace(p)
			prefix := ""
			if rest, ok := strings.CutPrefix(core, "secret:"); ok {
				prefix, core = "secret:", rest
			}
			b.WriteString(lead + prefix + renamePath(core, ren) + trail)
			if i < len(seps) {
				b.WriteString(seps[i])
			}
		}
		return "{{" + b.String() + "}}"
	})
}

// renamePath renames the head of a variable path ("q", "q.x", "q[0].y").
func renamePath(p string, ren map[string]string) string {
	if p == "" || strings.HasPrefix(p, "'") || strings.HasPrefix(p, `"`) {
		return p
	}
	end := strings.IndexAny(p, ".[ ")
	if end < 0 {
		end = len(p)
	}
	if nw, ok := ren[p[:end]]; ok {
		return nw + p[end:]
	}
	return p
}

// inputName reads the name of an input entry (object or legacy string).
func inputName(raw json.RawMessage) string {
	var obj struct {
		Name string `json:"name"`
	}
	if json.Unmarshal(raw, &obj) == nil && obj.Name != "" {
		return obj.Name
	}
	var s string
	_ = json.Unmarshal(raw, &s)
	return s
}

// renameStaged applies the renames to the staged draft action and to the
// recorded values in d.
func renameStaged(stage, actionName string, ren map[string]string, d *Draft) error {
	if len(ren) == 0 {
		return nil
	}
	path := filepath.Join(stage, "actions", actionName+".json")
	var def action.ActionDef
	if err := readJSON(path, &def); err != nil {
		return err
	}
	if err := RenameInputs(&def, ren); err != nil {
		return err
	}
	for _, name := range d.NewFragments {
		fpath := filepath.Join(stage, "fragments", name+".json")
		var f action.FragmentDef
		if err := readJSON(fpath, &f); err != nil {
			return err
		}
		if err := RenameInFragment(&f, ren); err != nil {
			return err
		}
		if err := writeJSON(fpath, &f); err != nil {
			return err
		}
	}
	for old, nw := range ren {
		if v, ok := d.RecordedInputs[old]; ok {
			delete(d.RecordedInputs, old)
			d.RecordedInputs[nw] = v
		}
	}
	return writeJSON(path, &def)
}
