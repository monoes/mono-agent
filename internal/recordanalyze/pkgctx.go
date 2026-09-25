package recordanalyze

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"path"
	"strings"

	"github.com/monoes/mono-agent/internal/action"
	"github.com/monoes/mono-agent/internal/automation"
)

// Existing is what the draft may reuse from the target automation.
type Existing struct {
	Manifest  automation.Manifest
	Selectors map[string]action.SelectorEntry
	Fragments map[string]*action.FragmentDef
	Scripts   map[string]string
	Actions   map[string]bool
}

// Env is everything the checks and the naming need besides the output.
type Env struct {
	Analysis *Analysis
	// Target is the automation the action is added to; nil for a new one.
	Target *Existing
	// AutomationTaken reports installed (or removed built-in) package ids.
	AutomationTaken func(id string) bool
}

// LoadExisting reads the reusable parts of an opened package.
func LoadExisting(p *automation.Package) (*Existing, error) {
	e := &Existing{
		Manifest:  p.Manifest,
		Selectors: map[string]action.SelectorEntry{},
		Fragments: map[string]*action.FragmentDef{},
		Scripts:   map[string]string{},
		Actions:   map[string]bool{},
	}
	for _, a := range p.Manifest.Actions {
		e.Actions[a] = true
	}
	if b, err := fs.ReadFile(p.FS, "selectors.json"); err == nil {
		if err := json.Unmarshal(b, &e.Selectors); err != nil {
			return nil, fmt.Errorf("selectors.json: %w", err)
		}
	}
	for _, dir := range []string{"actions", "fragments", "scripts"} {
		entries, _ := fs.ReadDir(p.FS, dir)
		for _, en := range entries {
			if en.IsDir() {
				continue
			}
			b, err := fs.ReadFile(p.FS, path.Join(dir, en.Name()))
			if err != nil {
				return nil, err
			}
			switch dir {
			case "actions":
				e.Actions[strings.TrimSuffix(en.Name(), ".json")] = true
			case "fragments":
				var f action.FragmentDef
				if err := json.Unmarshal(b, &f); err != nil {
					return nil, fmt.Errorf("fragments/%s: %w", en.Name(), err)
				}
				name := strings.TrimSuffix(en.Name(), ".json")
				if f.Name == "" {
					f.Name = name
				}
				e.Fragments[name] = &f
			case "scripts":
				e.Scripts[en.Name()] = string(b)
			}
		}
	}
	return e, nil
}

// draftContext is the action.PackageContext of a draft that is not on
// disk yet: the output's selectors/fragments/scripts over the target's.
type draftContext struct {
	id, start string
	domains   []string
	permitted []string
	out       *Output
	existing  *Existing
}

func newDraftContext(out *Output, env *Env, m *automation.Manifest) *draftContext {
	return &draftContext{id: m.ID, start: m.Site.StartURL, domains: m.Site.Domains,
		permitted: m.Permissions.Steps, out: out, existing: env.Target}
}

func (c *draftContext) ID() string               { return c.id }
func (c *draftContext) StartURL() string         { return c.start }
func (c *draftContext) Domains() []string        { return c.domains }
func (c *draftContext) PermittedSteps() []string { return c.permitted }

func (c *draftContext) Fragment(name string) (*action.FragmentDef, error) {
	for i := range c.out.Fragments {
		if c.out.Fragments[i].Name == name {
			return &c.out.Fragments[i], nil
		}
	}
	if c.existing != nil {
		if f, ok := c.existing.Fragments[name]; ok {
			return f, nil
		}
	}
	return nil, fmt.Errorf("fragment %q not found", name)
}

func (c *draftContext) Selector(key string) (*action.SelectorEntry, bool) {
	if e, ok := c.out.Selectors[key]; ok {
		return &e, true
	}
	if c.existing != nil {
		if e, ok := c.existing.Selectors[key]; ok {
			return &e, true
		}
	}
	return nil, false
}

func (c *draftContext) Script(name string) (string, error) {
	if s, ok := c.out.Scripts[name]; ok {
		return s, nil
	}
	if c.existing != nil {
		if s, ok := c.existing.Scripts[name]; ok {
			return s, nil
		}
	}
	return "", fmt.Errorf("script %q not found", name)
}

func (c *draftContext) ResolveAction(ref string) (*action.ActionDef, action.PackageContext, error) {
	return nil, nil, fmt.Errorf("call_action %q cannot be resolved inside a draft", ref)
}

// walkSteps calls fn for every step, depth first, including inline bodies.
func walkSteps(steps []action.StepDef, fn func(s *action.StepDef)) {
	for i := range steps {
		fn(&steps[i])
		walkSteps(steps[i].Steps, fn)
	}
}

// allSteps walks the action and every draft fragment.
func allSteps(out *Output, fn func(s *action.StepDef)) {
	walkSteps(out.Action.Steps, fn)
	for i := range out.Fragments {
		walkSteps(out.Fragments[i].Steps, fn)
	}
}
