package nodes

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/monoes/mono-agent/internal/action"
	"github.com/monoes/mono-agent/internal/workflow"
)

// formKeyFeeds is what one node form field sets for the action, as
// BrowserNode.Execute maps config: "targets" becomes selectedListItems,
// "message" the message variables, "keywords" the keyword variables and
// "limit" maxResultsCount. Every other key is passed through as itself.
// The "username" field is the browser session, so it feeds no input.
var formKeyFeeds = map[string][]string{
	"targets":  {"targets", "selectedListItems"},
	"message":  {"message", "messageText", "contentMessage", "commentText", "replyText", "text"},
	"keywords": {"keywords", "keyword"},
	"limit":    {"limit", "maxResultsCount"},
	"username": nil,
}

// requiredInput is one required input of an action: its name, aliases,
// and whether it declares a default (then the user need not supply it).
type requiredInput struct {
	Name       string
	Aliases    []string
	HasDefault bool
}

func requiredInputs(def *action.ActionDef) []requiredInput {
	var out []requiredInput
	if def.Inputs == nil {
		return nil
	}
	for _, raw := range def.Inputs.Required {
		var name string
		if json.Unmarshal(raw, &name) == nil {
			out = append(out, requiredInput{Name: name})
			continue
		}
		var obj struct {
			Name    string          `json:"name"`
			Aliases []string        `json:"aliases"`
			Default json.RawMessage `json:"default"`
		}
		if json.Unmarshal(raw, &obj) == nil && obj.Name != "" {
			out = append(out, requiredInput{Name: obj.Name, Aliases: obj.Aliases,
				HasDefault: len(obj.Default) > 0 && string(obj.Default) != "null"})
		}
	}
	return out
}

// TestBuiltinForms_RequiredInputsAreRequiredFields: every required input of
// every built-in action is set by a required field of the node's resolved
// form, or by a field with a default the editor fills in (directly, through
// the node-facing names above, or through one of the input's declared
// aliases). An input the form can't set, or sets only from an optional empty
// field, is a form the user can't fill in correctly.
func TestBuiltinForms_RequiredInputsAreRequiredFields(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	action.SetDefSource(nil)

	for _, nt := range browserNodeTypes() {
		dot := strings.Index(nt, ".")
		def, err := action.GetLoader().Load(nt[:dot], nt[dot+1:])
		if err != nil {
			t.Errorf("%s: %v", nt, err)
			continue
		}
		schema, err := workflow.LoadDefaultSchema(nt)
		if err != nil {
			t.Errorf("%s: %v", nt, err)
			continue
		}
		fed := map[string]bool{} // input name → set by a required field
		for _, f := range schema.Fields {
			// A field the user must fill, or one that always sends a value.
			if !f.Required && f.Default == nil {
				continue
			}
			feeds, mapped := formKeyFeeds[f.Key]
			if !mapped {
				feeds = []string{f.Key}
			}
			for _, n := range feeds {
				fed[n] = true
			}
		}
		for _, in := range requiredInputs(def) {
			ok := fed[in.Name] // a required input's own default isn't applied at run time
			for _, a := range in.Aliases {
				ok = ok || fed[a]
			}
			if !ok {
				t.Errorf("%s: required input %q has no required form field", nt, in.Name)
			}
		}
	}
}
