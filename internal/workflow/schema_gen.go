package workflow

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"strings"
	"unicode"

	"github.com/monoes/mono-agent/data"
	"github.com/monoes/mono-agent/internal/action"
)

// Forms for browser automation nodes ("<automation>.<action>") that have no
// schema file are generated from the action's declared inputs (spec §4.3).

// actionInput is one entry of an action's inputs.required/optional. An entry
// may also be a bare string, which is just the name.
type actionInput struct {
	Name        string        `json:"name"`
	Type        string        `json:"type"`
	Description string        `json:"description"`
	Default     interface{}   `json:"default"`
	Min         *float64      `json:"min"`
	Max         *float64      `json:"max"`
	Enum        []interface{} `json:"enum"`
	Options     []interface{} `json:"options"`
	Format      string        `json:"format"`
	UI          *inputUI      `json:"ui"`
}

// inputUI is the optional "ui" hint block on an input.
type inputUI struct {
	Label       string `json:"label"`
	Placeholder string `json:"placeholder"`
	Help        string `json:"help"`
	Widget      string `json:"widget"` // any NodeSchemaField type, e.g. textarea, password, code
	Rows        int    `json:"rows"`
}

// sessionField is the browser-session selector every generated browser form
// starts with (the same field browser.generic.json offers).
var sessionField = NodeSchemaField{
	Key:      "username",
	Label:    "Session Username",
	Type:     "text",
	Required: false,
	Help:     "Username of the browser session to use. Leave blank to use the default session.",
}

// isBuiltinAutomation reports whether id is one of the embedded built-in
// packages. Only those share the action.<name>.json forms: an imported
// package's "send_dms" is not Instagram's.
func isBuiltinAutomation(id string) bool {
	if id == "" || strings.ContainsAny(id, "/\\.") {
		return false
	}
	_, err := fs.Stat(data.AutomationsFS, "automations/"+id+"/automation.json")
	return err == nil
}

// loadActionDef finds the action behind a browser node type, through the
// installed definition source when there is one, else the legacy loader. A
// legacy "<p>.<action>" name also resolves to the wrapped "local-<p>" package.
func loadActionDef(automation, actionType string) (*action.ActionDef, error) {
	candidates := []string{automation}
	if !strings.HasPrefix(automation, "local-") {
		candidates = append(candidates, "local-"+automation)
	}
	var lastErr error
	for _, a := range candidates {
		def, err := loadActionDefFrom(a, actionType)
		if err == nil {
			return def, nil
		}
		lastErr = err
	}
	return nil, lastErr
}

func loadActionDefFrom(automation, actionType string) (*action.ActionDef, error) {
	src := action.CurrentDefSource()
	if src == nil {
		return action.GetLoader().Load(automation, actionType)
	}
	raw, err := src.Load(automation, actionType)
	if err != nil {
		return nil, err
	}
	var def action.ActionDef
	if err := json.Unmarshal(raw, &def); err != nil {
		return nil, fmt.Errorf("parse %s/%s: %w", automation, actionType, err)
	}
	return &def, nil
}

// generateActionSchema builds the form for a browser node type from its
// action's inputs. ok is false when nodeType is not a known action.
func generateActionSchema(nodeType string) (*NodeSchema, bool) {
	dot := strings.Index(nodeType, ".")
	if dot <= 0 || dot == len(nodeType)-1 {
		return nil, false
	}
	def, err := loadActionDef(nodeType[:dot], nodeType[dot+1:])
	if err != nil || def == nil {
		return nil, false
	}
	return schemaFromInputs(def.Inputs), true
}

// schemaFromInputs turns declared inputs into form fields, required first,
// after the session field.
func schemaFromInputs(in *action.InputDef) *NodeSchema {
	schema := &NodeSchema{Fields: []NodeSchemaField{sessionField}}
	if in == nil {
		return schema
	}
	seen := map[string]bool{sessionField.Key: true}
	add := func(raws []json.RawMessage, required bool) {
		for _, raw := range raws {
			ai, ok := parseActionInput(raw)
			if !ok {
				continue
			}
			f := inputField(ai, required)
			if seen[f.Key] {
				continue
			}
			seen[f.Key] = true
			schema.Fields = append(schema.Fields, f)
		}
	}
	add(in.Required, true)
	add(in.Optional, false)
	return schema
}

func parseActionInput(raw json.RawMessage) (actionInput, bool) {
	var name string
	if json.Unmarshal(raw, &name) == nil {
		return actionInput{Name: name}, name != ""
	}
	var ai actionInput
	if err := json.Unmarshal(raw, &ai); err != nil || ai.Name == "" {
		return actionInput{}, false
	}
	return ai, true
}

func inputField(ai actionInput, required bool) NodeSchemaField {
	f := NodeSchemaField{
		Key:      ai.Name,
		Label:    humanizeName(ai.Name),
		Required: required,
		Default:  ai.Default,
		Help:     ai.Description,
		Min:      ai.Min,
		Max:      ai.Max,
	}
	// The browser node takes its target list as "targets" and feeds it to
	// the action as selectedListItems.
	if ai.Name == "selectedListItems" {
		f.Key, f.Label = "targets", "Targets"
	}

	opts := ai.Options
	if len(opts) == 0 {
		opts = ai.Enum
	}
	switch {
	case len(opts) > 0:
		f.Type = "select"
		for _, o := range opts {
			f.Options = append(f.Options, fmt.Sprint(o))
		}
	case ai.Type == "number" || ai.Type == "integer":
		f.Type = "number"
	case ai.Type == "boolean":
		f.Type = "boolean"
	case ai.Type == "list" || ai.Type == "array":
		f.Type, f.ItemType = "array", "text"
	case ai.Format == "password":
		f.Type = "password"
	default:
		f.Type = "text"
	}

	if ui := ai.UI; ui != nil {
		if ui.Label != "" {
			f.Label = ui.Label
		}
		if ui.Placeholder != "" {
			f.Placeholder = ui.Placeholder
		}
		if ui.Help != "" {
			f.Help = ui.Help
		}
		if ui.Widget != "" {
			f.Type = ui.Widget
		}
		if ui.Rows > 0 {
			f.Rows = ui.Rows
		}
	}
	return f
}

// acronyms are written in capitals in generated labels ("postUrl" → "Post URL").
var acronyms = map[string]bool{"url": true, "id": true, "api": true, "dm": true, "dms": true, "html": true, "json": true}

// humanizeName turns "maxComments" or "max_comments" into "Max Comments".
func humanizeName(name string) string {
	var words []string
	var cur []rune
	flush := func() {
		if len(cur) > 0 {
			words = append(words, string(cur))
			cur = cur[:0]
		}
	}
	prevLower := false
	for _, r := range name {
		startsWord := unicode.IsUpper(r) && prevLower
		prevLower = unicode.IsLower(r) || unicode.IsDigit(r)
		switch {
		case r == '_' || r == '-' || r == ' ':
			flush()
		case startsWord:
			flush()
			cur = append(cur, r)
		default:
			cur = append(cur, r)
		}
	}
	flush()
	for i, w := range words {
		if acronyms[strings.ToLower(w)] {
			words[i] = strings.ToUpper(w)
			continue
		}
		rs := []rune(w)
		rs[0] = unicode.ToUpper(rs[0])
		words[i] = string(rs)
	}
	return strings.Join(words, " ")
}
