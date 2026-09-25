package recordanalyze

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"regexp"
	"sort"

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
	body, err := json.Marshal(struct {
		Steps []action.StepDef `json:"steps"`
		Loops []action.LoopDef `json:"loops,omitempty"`
	}{def.Steps, def.Loops})
	if err != nil {
		return err
	}
	s := string(body)
	for _, old := range olds {
		re := regexp.MustCompile(`\{\{(\s*(?:secret:)?)` + regexp.QuoteMeta(old) + `(\s*\}\}|[.\[])`)
		s = re.ReplaceAllString(s, "{{${1}"+ren[old]+"${2}")
	}
	var back struct {
		Steps []action.StepDef `json:"steps"`
		Loops []action.LoopDef `json:"loops,omitempty"`
	}
	if err := json.Unmarshal([]byte(s), &back); err != nil {
		return err
	}
	def.Steps, def.Loops = back.Steps, back.Loops
	return nil
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
	for old, nw := range ren {
		if v, ok := d.RecordedInputs[old]; ok {
			delete(d.RecordedInputs, old)
			d.RecordedInputs[nw] = v
		}
	}
	return writeJSON(path, &def)
}
