package recordanalyze

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/monoes/mono-agent/internal/action"
)

// DraftInput is one action input, flattened for the review UI.
type DraftInput struct {
	Name        string `json:"name"`
	Type        string `json:"type"`
	Required    bool   `json:"required"`
	Default     any    `json:"default,omitempty"`
	Description string `json:"description,omitempty"`
	Format      string `json:"format,omitempty"`
	Secret      bool   `json:"secret"`
	// NeedsValue: verify/run needs a value the draft does not have — a
	// secret, or a required input with no recorded value and no default.
	NeedsValue bool `json:"needsValue"`
}

// DraftView is the `draft` of `record analyze --json`: draft.json's fields
// plus the proposed action, its inputs (flat), selectors and fragments.
type DraftView struct {
	*Draft
	ActionDef *action.ActionDef               `json:"actionDef"`
	Inputs    []DraftInput                    `json:"inputs"`
	Selectors map[string]action.SelectorEntry `json:"selectors"`
	Fragments []string                        `json:"fragments"`
}

// LoadDraftView reads a draft directory into a DraftView.
func LoadDraftView(dir string) (*DraftView, error) {
	d, err := ReadDraft(dir)
	if err != nil {
		return nil, err
	}
	v := &DraftView{Draft: d, Inputs: []DraftInput{}, Selectors: map[string]action.SelectorEntry{}, Fragments: []string{}}
	var def action.ActionDef
	if err := readJSON(filepath.Join(dir, "actions", d.Action+".json"), &def); err != nil {
		return nil, err
	}
	v.ActionDef = &def
	v.Inputs = flatInputs(&def, d.RecordedInputs)
	_ = readJSON(filepath.Join(dir, "selectors.json"), &v.Selectors)
	entries, _ := os.ReadDir(filepath.Join(dir, "fragments"))
	for _, e := range entries {
		if name, ok := strings.CutSuffix(e.Name(), ".json"); ok && !e.IsDir() {
			v.Fragments = append(v.Fragments, name)
		}
	}
	sort.Strings(v.Fragments)
	return v, nil
}

func flatInputs(def *action.ActionDef, recorded map[string]any) []DraftInput {
	out := []DraftInput{}
	if def.Inputs == nil {
		return out
	}
	for gi, group := range [][]json.RawMessage{def.Inputs.Required, def.Inputs.Optional} {
		for _, raw := range group {
			in := DraftInput{Type: "string"}
			if json.Unmarshal(raw, &in) != nil {
				in.Name = inputName(raw)
			}
			if in.Type == "" {
				in.Type = "string"
			}
			in.Required = gi == 0
			in.Secret = in.Type == "secret"
			_, hasRecorded := recorded[in.Name]
			in.NeedsValue = in.Secret || (in.Required && !hasRecorded && (in.Default == nil || in.Default == ""))
			out = append(out, in)
		}
	}
	return out
}
