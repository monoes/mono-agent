package workflow

import (
	"fmt"
	"testing"

	"github.com/monoes/mono-agent/internal/action"
)

// pkgSource is a DefSource serving fixed action JSON.
type pkgSource map[string]string

func (s pkgSource) Load(a, t string) ([]byte, error) {
	if raw, ok := s[a+"/"+t]; ok {
		return []byte(raw), nil
	}
	return nil, fmt.Errorf("not found: %s/%s", a, t)
}
func (s pkgSource) List() ([]string, error)                         { return nil, nil }
func (s pkgSource) Package(automation string) action.PackageContext { return nil }

func fieldByKey(t *testing.T, s *NodeSchema, key string) NodeSchemaField {
	t.Helper()
	for _, f := range s.Fields {
		if f.Key == key {
			return f
		}
	}
	t.Fatalf("field %q missing; have %+v", key, s.Fields)
	return NodeSchemaField{}
}

func TestLoadDefaultSchema_GeneratedFromPackageInputs(t *testing.T) {
	action.SetDefSource(pkgSource{
		"acme-crm/create_contact": `{
		  "actionType": "create_contact",
		  "inputs": {
		    "required": [
		      {"name": "email", "type": "string", "format": "email",
		       "ui": {"label": "Email", "placeholder": "jane@x.com", "help": "Work email"}},
		      {"name": "selectedListItems", "type": "list", "description": "Accounts"}
		    ],
		    "optional": [
		      "note",
		      {"name": "maxRetries", "type": "number", "default": 3, "min": 1, "max": 9, "description": "Retries"},
		      {"name": "stage", "type": "string", "enum": ["lead", "customer"], "default": "lead"},
		      {"name": "notify", "type": "boolean"},
		      {"name": "bio", "type": "string", "ui": {"widget": "textarea", "rows": 4}},
		      {"name": "username", "type": "string"}
		    ]
		  },
		  "steps": []
		}`,
		// Same action name as a built-in shared form: must NOT borrow it.
		"acme-crm/find_by_keyword": `{"actionType":"find_by_keyword","inputs":{"required":[{"name":"query","type":"string"}]},"steps":[]}`,
	})
	t.Cleanup(func() { action.SetDefSource(nil) })

	s, err := LoadDefaultSchema("acme-crm.create_contact")
	if err != nil {
		t.Fatal(err)
	}
	if s.Fields[0].Key != "username" || s.Fields[0].Label != "Session Username" {
		t.Errorf("first field should be the session field, got %+v", s.Fields[0])
	}
	n := 0
	for _, f := range s.Fields {
		if f.Key == "username" {
			n++
		}
	}
	if n != 1 {
		t.Errorf("username appears %d times", n)
	}

	email := fieldByKey(t, s, "email")
	if !email.Required || email.Type != "text" || email.Label != "Email" || email.Placeholder != "jane@x.com" || email.Help != "Work email" {
		t.Errorf("email: %+v", email)
	}
	targets := fieldByKey(t, s, "targets")
	if !targets.Required || targets.Type != "array" || targets.ItemType != "text" || targets.Help != "Accounts" {
		t.Errorf("targets: %+v", targets)
	}
	note := fieldByKey(t, s, "note")
	if note.Required || note.Type != "text" || note.Label != "Note" {
		t.Errorf("note: %+v", note)
	}
	mr := fieldByKey(t, s, "maxRetries")
	if mr.Type != "number" || mr.Label != "Max Retries" || mr.Default != float64(3) || mr.Min == nil || *mr.Min != 1 || mr.Max == nil || *mr.Max != 9 {
		t.Errorf("maxRetries: %+v", mr)
	}
	stage := fieldByKey(t, s, "stage")
	if stage.Type != "select" || len(stage.Options) != 2 || stage.Options[1] != "customer" || stage.Default != "lead" {
		t.Errorf("stage: %+v", stage)
	}
	if f := fieldByKey(t, s, "notify"); f.Type != "boolean" {
		t.Errorf("notify: %+v", f)
	}
	if f := fieldByKey(t, s, "bio"); f.Type != "textarea" || f.Rows != 4 {
		t.Errorf("bio: %+v", f)
	}

	fk, _ := LoadDefaultSchema("acme-crm.find_by_keyword")
	fieldByKey(t, fk, "query")
	for _, f := range fk.Fields {
		if f.Key == "keywords" {
			t.Error("imported package borrowed the built-in action.find_by_keyword form")
		}
	}

	raw, ok := ReadEmbeddedSchema("acme-crm.create_contact")
	if !ok || len(raw) == 0 {
		t.Error("ReadEmbeddedSchema: no generated schema")
	}
	if _, ok := ReadEmbeddedSchema("acme-crm.nope"); ok {
		t.Error("ReadEmbeddedSchema: unknown action should report missing")
	}
}

func TestLoadDefaultSchema_GeneratedForBuiltins(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	action.SetDefSource(nil)

	// hackernews had no form at all before; now its inputs drive one.
	s, _ := LoadDefaultSchema("hackernews.submit_post")
	if f := fieldByKey(t, s, "title"); !f.Required {
		t.Errorf("title: %+v", f)
	}
	fieldByKey(t, s, "url")

	// A built-in without a shared form gets its declared inputs rather than
	// the generic keywords/message box.
	s, _ = LoadDefaultSchema("instagram.list_post_comments")
	fieldByKey(t, s, "targets")
	fieldByKey(t, s, "maxComments")

	// Explicit files keep precedence.
	s, _ = LoadDefaultSchema("linkedin.find_by_keyword")
	fieldByKey(t, s, "keywords")
	if s.Fields[0].Key == "username" {
		t.Error("shared action.find_by_keyword form replaced by a generated one")
	}
}

func TestHumanizeName(t *testing.T) {
	for in, want := range map[string]string{
		"maxComments": "Max Comments", "max_comments": "Max Comments",
		"postURL": "Post URL", "email": "Email",
	} {
		if got := humanizeName(in); got != want {
			t.Errorf("humanizeName(%q) = %q, want %q", in, got, want)
		}
	}
}
