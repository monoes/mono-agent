package schemagen

import (
	"bytes"
	"encoding/json"
	"io/fs"
	"net/url"
	"path"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/monoes/mono-agent/data"
	"github.com/monoes/mono-agent/internal/action"
	"github.com/monoes/mono-agent/internal/automation"
)

// Checks on the shipped package data: the built-ins in data/automations and
// the scaffolding templates in data/automation-templates.

// pkgFiles is one package tree read from an fs.FS.
type pkgFiles struct {
	name     string
	manifest []byte
	actions  map[string][]byte // action name → JSON
	other    map[string][]byte // other .json files by package-relative path
}

func readPackages(t *testing.T, fsys fs.FS, dir string, transform func([]byte, string) []byte) []pkgFiles {
	t.Helper()
	entries, err := fs.ReadDir(fsys, dir)
	if err != nil {
		t.Fatal(err)
	}
	var out []pkgFiles
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		root := path.Join(dir, e.Name())
		p := pkgFiles{name: e.Name(), actions: map[string][]byte{}, other: map[string][]byte{}}
		err := fs.WalkDir(fsys, root, func(fp string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return err
			}
			b, err := fs.ReadFile(fsys, fp)
			if err != nil {
				return err
			}
			rel := strings.TrimPrefix(fp, root+"/")
			if transform != nil {
				b = transform(b, rel)
			}
			switch {
			case rel == "automation.json":
				p.manifest = b
			case path.Dir(rel) == "actions" && path.Ext(rel) == ".json":
				p.actions[strings.TrimSuffix(path.Base(rel), ".json")] = b
			case path.Ext(rel) == ".json":
				p.other[rel] = b
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, p)
	}
	if len(out) == 0 {
		t.Fatalf("no packages under %s", dir)
	}
	return out
}

func decodeStrict(b []byte, v any) error {
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	return dec.Decode(v)
}

// stepTypes returns every step type used, nested for_each bodies included.
func stepTypes(steps []action.StepDef, into map[string]bool) {
	for _, s := range steps {
		into[s.Type] = true
		stepTypes(s.Steps, into)
	}
}

func sideEffectSteps(steps []action.StepDef) (n int) {
	for _, s := range steps {
		if s.SideEffect {
			n++
		}
		n += sideEffectSteps(s.Steps)
	}
	return n
}

// permitted mirrors the manifest rule: an exact step type or a prefix glob.
func permitted(allowed []string, typ string) bool {
	for _, a := range allowed {
		if a == typ || (strings.HasSuffix(a, "*") && strings.HasPrefix(typ, strings.TrimSuffix(a, "*"))) {
			return true
		}
	}
	return false
}

func hostAllowed(domains []string, rawURL string) bool {
	u, err := url.Parse(rawURL)
	if err != nil || u.Hostname() == "" {
		return false
	}
	h := u.Hostname()
	for _, d := range domains {
		if d == h {
			return true
		}
		if suf, ok := strings.CutPrefix(d, "*."); ok && (h == suf || strings.HasSuffix(h, "."+suf)) {
			return true
		}
	}
	return false
}

// checkPackage runs the shared checks and returns the parsed manifest.
func checkPackage(t *testing.T, p pkgFiles, strictActions bool) (*automation.Manifest, map[string]*action.ActionDef) {
	t.Helper()
	var m automation.Manifest
	if err := decodeStrict(p.manifest, &m); err != nil {
		t.Fatalf("%s/automation.json: %v", p.name, err)
	}
	if m.Schema != automation.SchemaV1 {
		t.Errorf("%s: schema = %q", p.name, m.Schema)
	}
	if m.Policy.Tier != "standard" && m.Policy.Tier != "social" {
		t.Errorf("%s: policy.tier = %q", p.name, m.Policy.Tier)
	}

	files := make([]string, 0, len(p.actions))
	for a := range p.actions {
		files = append(files, a)
	}
	sort.Strings(files)
	listed := slices.Clone(m.Actions)
	sort.Strings(listed)
	if !slices.Equal(files, listed) {
		t.Errorf("%s: manifest actions %v != actions/ files %v", p.name, m.Actions, files)
	}

	used := map[string]bool{}
	defs := map[string]*action.ActionDef{}
	for name, b := range p.actions {
		var def action.ActionDef
		var err error
		if strictActions {
			err = decodeStrict(b, &def)
		} else {
			err = json.Unmarshal(b, &def)
		}
		if err != nil {
			t.Errorf("%s/actions/%s.json: %v", p.name, name, err)
			continue
		}
		defs[name] = &def
		if def.ActionType != name {
			t.Errorf("%s/%s: actionType = %q", p.name, name, def.ActionType)
		}
		if !slices.Contains(SideEffectLevels, def.SideEffects) {
			t.Errorf("%s/%s: sideEffects = %q, want one of %v", p.name, name, def.SideEffects, SideEffectLevels)
		}
		n := sideEffectSteps(def.Steps)
		switch def.SideEffects {
		case "write", "message", "destructive":
			if n == 0 {
				t.Errorf("%s/%s: sideEffects %q but no step is marked sideEffect", p.name, name, def.SideEffects)
			}
		default:
			if n > 0 {
				t.Errorf("%s/%s: sideEffects %q but %d step(s) marked sideEffect", p.name, name, def.SideEffects, n)
			}
		}
		stepTypes(def.Steps, used)
	}
	for _, b := range p.other {
		var frag action.FragmentDef
		if json.Unmarshal(b, &frag) == nil && len(frag.Steps) > 0 {
			stepTypes(frag.Steps, used)
		}
	}
	for typ := range used {
		if !slices.Contains(StepTypes, typ) {
			t.Errorf("%s: unknown step type %q", p.name, typ)
		}
		if !permitted(m.Permissions.Steps, typ) {
			t.Errorf("%s: step type %q is not in permissions.steps %v", p.name, typ, m.Permissions.Steps)
		}
	}
	return &m, defs
}

func TestBuiltinPackages(t *testing.T) {
	pkgs := readPackages(t, data.AutomationsFS, "automations", nil)
	total := 0
	for _, p := range pkgs {
		m, defs := checkPackage(t, p, false)
		total += len(defs)
		if m.ID != p.name {
			t.Errorf("%s: id = %q", p.name, m.ID)
		}
		if m.Requires.Native != p.name {
			t.Errorf("%s: requires.native = %q", p.name, m.Requires.Native)
		}
		wantTier := "social"
		if p.name == "gemini" {
			wantTier = "standard"
		}
		if m.Policy.Tier != wantTier {
			t.Errorf("%s: policy.tier = %q, want %q", p.name, m.Policy.Tier, wantTier)
		}
		if len(m.Site.Domains) == 0 || !hostAllowed(m.Site.Domains, m.Site.StartURL) {
			t.Errorf("%s: startUrl %q not inside domains %v", p.name, m.Site.StartURL, m.Site.Domains)
		}
		if m.Login == nil || !hostAllowed(m.Site.Domains, m.Login.URL) || m.Login.LoggedIn == nil || m.Login.SessionTTLDays != 30 {
			t.Errorf("%s: incomplete login block %+v", p.name, m.Login)
		}
		// Permissions are exact for built-ins: nothing listed that is unused.
		used := map[string]bool{}
		for _, d := range defs {
			stepTypes(d.Steps, used)
		}
		for _, s := range m.Permissions.Steps {
			if !used[s] {
				t.Errorf("%s: permissions.steps lists unused %q", p.name, s)
			}
		}
		for name, def := range defs {
			if def.Schema != "../../../schemas/action.v1.schema.json" {
				t.Errorf("%s/%s: $schema = %q", p.name, name, def.Schema)
			}
			for _, s := range def.Steps {
				if s.Type == "navigate" && strings.HasPrefix(s.URL, "http") && !hostAllowed(m.Site.Domains, s.URL) {
					t.Errorf("%s/%s: navigate %q outside domains", p.name, name, s.URL)
				}
			}
		}
		if m.SchemaRef != "../../schemas/automation.v1.schema.json" {
			t.Errorf("%s: $schema = %q", p.name, m.SchemaRef)
		}
	}
	if total != 64 {
		t.Errorf("built-in actions = %d, want 64", total)
	}
}

// templateValues are the scaffold placeholder values used in tests. The name
// holds characters that need JSON escaping.
var templateValues = map[string]string{
	"{{id}}":       "acme-crm",
	"{{name}}":     `Acme "CRM" \ Tools`,
	"{{startUrl}}": "https://app.acme-crm.com/",
	"{{domain}}":   "app.acme-crm.com",
}

// substitute does what the scaffolder must do: JSON-escape values in .json
// files, insert them verbatim elsewhere.
func substitute(b []byte, rel string) []byte {
	s := string(b)
	for k, v := range templateValues {
		if strings.HasSuffix(rel, ".json") {
			q, _ := json.Marshal(v)
			v = string(q[1 : len(q)-1])
		}
		s = strings.ReplaceAll(s, k, v)
	}
	return []byte(s)
}

func TestTemplatesParseRaw(t *testing.T) {
	err := fs.WalkDir(data.TemplatesFS, "automation-templates", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || path.Ext(p) != ".json" {
			return err
		}
		b, _ := fs.ReadFile(data.TemplatesFS, p)
		if !json.Valid(b) {
			t.Errorf("%s: invalid JSON before substitution", p)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestTemplatesSubstitute(t *testing.T) {
	want := []string{"basic", "list-scrape", "login-form", "post-content", "search-and-extract"}
	pkgs := readPackages(t, data.TemplatesFS, "automation-templates", substitute)
	var names []string
	fragments := 0
	for _, p := range pkgs {
		names = append(names, p.name)
		m, defs := checkPackage(t, p, true)
		if m.ID != "acme-crm" || m.Name != templateValues["{{name}}"] {
			t.Errorf("%s: placeholders not substituted: id %q name %q", p.name, m.ID, m.Name)
		}
		if !hostAllowed(m.Site.Domains, m.Site.StartURL) {
			t.Errorf("%s: startUrl outside domains", p.name)
		}
		for name, def := range defs {
			if def.Automation != "acme-crm" {
				t.Errorf("%s/%s: automation = %q", p.name, name, def.Automation)
			}
			used := map[string]bool{}
			stepTypes(def.Steps, used)
			if used["call_bot_method"] || used["page_script"] {
				t.Errorf("%s/%s: templates must be declarative", p.name, name)
			}
			for _, s := range def.Steps {
				if s.Type == "navigate" && !hostAllowed(m.Site.Domains, s.URL) {
					t.Errorf("%s/%s: navigate %q outside domains", p.name, name, s.URL)
				}
			}
		}
		for rel, b := range p.other {
			switch {
			case rel == "selectors.json":
				var sel map[string]action.SelectorEntry
				if err := decodeStrict(b, &sel); err != nil {
					t.Errorf("%s/%s: %v", p.name, rel, err)
				}
				for k, e := range sel {
					if len(e.Candidates) == 0 {
						t.Errorf("%s: selector %q has no candidates", p.name, k)
					}
				}
			case strings.HasPrefix(rel, "fragments/"):
				fragments++
				var f action.FragmentDef
				if err := json.Unmarshal(b, &f); err != nil || f.Name+".json" != path.Base(rel) {
					t.Errorf("%s/%s: bad fragment (%v)", p.name, rel, err)
				}
			case strings.HasPrefix(rel, "tests/"):
				if !json.Valid(b) {
					t.Errorf("%s/%s: invalid JSON", p.name, rel)
				}
			}
		}
		if _, err := fs.Stat(data.TemplatesFS, path.Join("automation-templates", p.name, "README.md")); err != nil {
			t.Errorf("%s: no README.md", p.name)
		}
	}
	if !slices.Equal(names, want) {
		t.Errorf("templates = %v, want %v", names, want)
	}
	if fragments == 0 {
		t.Error("no template ships a fragment")
	}
	for _, fx := range []string{"list-scrape/tests/fixtures/list_items.html", "list-scrape/tests/list_items.expect.json",
		"search-and-extract/tests/fixtures/search.html", "search-and-extract/tests/search.expect.json"} {
		if _, err := fs.Stat(data.TemplatesFS, "automation-templates/"+fx); err != nil {
			t.Errorf("missing %s", fx)
		}
	}
}

// TestShippedFilesMatchSchemas validates every built-in and template file
// against the generated schemas.
func TestShippedFilesMatchSchemas(t *testing.T) {
	root, _ := ModuleRoot(".")
	files, err := GenerateAll(root)
	if err != nil {
		t.Fatal(err)
	}
	v := map[string]*miniValidator{}
	for name, b := range files {
		if v[name], err = newMiniValidator(b); err != nil {
			t.Fatal(err)
		}
	}
	check := func(label, schema string, b []byte) {
		for _, e := range v[schema].Validate(b) {
			t.Errorf("%s: %s", label, e)
		}
	}
	for _, src := range []struct {
		fsys fs.FS
		dir  string
		tf   func([]byte, string) []byte
	}{
		{data.AutomationsFS, "automations", nil},
		{data.TemplatesFS, "automation-templates", substitute},
	} {
		for _, p := range readPackages(t, src.fsys, src.dir, src.tf) {
			check(p.name+"/automation.json", "automation.v1.schema.json", p.manifest)
			for a, b := range p.actions {
				check(p.name+"/actions/"+a, "action.v1.schema.json", b)
			}
			for rel, b := range p.other {
				switch {
				case rel == "selectors.json":
					check(p.name+"/"+rel, "selectors.v1.schema.json", b)
				case strings.HasPrefix(rel, "fragments/"):
					check(p.name+"/"+rel, "fragment.v1.schema.json", b)
				}
			}
		}
	}
}
