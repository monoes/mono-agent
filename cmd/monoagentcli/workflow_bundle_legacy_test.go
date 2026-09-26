package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/monoes/mono-agent/internal/action"
	"github.com/monoes/mono-agent/internal/browser"
	"github.com/monoes/mono-agent/internal/workflow"
	"github.com/rs/zerolog"
)

// D5a: a workflow whose nodes use a legacy action (~/.monoagent/actions/<p>,
// wrapped into a generated local-* package that keeps <p> as its alias) must
// bundle that package under canonical node types, and on a fresh machine the
// imported package must run the node.

// legacyPage is a minimal page with a set of present CSS selectors.
type legacyPage struct {
	browser.PageInterface
	present map[string]bool
	hits    int // lookups that found an element
}

type legacyElement struct{ browser.ElementHandle }

func (p *legacyPage) Element(sel string, _ time.Duration) (browser.ElementHandle, error) {
	if p.present[sel] {
		p.hits++
		return legacyElement{}, nil
	}
	return nil, errors.New("element not found: " + sel)
}
func (p *legacyPage) ElementX(xp string, t time.Duration) (browser.ElementHandle, error) {
	return p.Element(xp, t)
}
func (p *legacyPage) Race(sels []string, _ time.Duration) (int, browser.ElementHandle, error) {
	for i, s := range sels {
		if p.present[s] {
			p.hits++
			return i, legacyElement{}, nil
		}
	}
	return -1, nil, errors.New("none of the selectors matched")
}
func (p *legacyPage) GetURL() (string, error)                     { return "https://example.com/", nil }
func (p *legacyPage) Navigate(string) error                       { return nil }
func (p *legacyPage) Timeout(time.Duration) browser.PageInterface { return p }
func (p *legacyPage) WaitLoad() error                             { return nil }

// writeLegacyHeading writes ~/.monoagent/actions/<platform>/get_heading.json.
func writeLegacyHeading(t *testing.T, home, platform string) {
	t.Helper()
	dir := filepath.Join(home, ".monoagent", "actions", platform)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := `{"actionType":"get_heading","platform":"` + platform + `","steps":[
  {"id":"open","type":"navigate","url":"https://example.com/"},
  {"id":"heading","type":"find_element","selector":"h1","timeout":0.2}
]}`
	if err := os.WriteFile(filepath.Join(dir, "get_heading.json"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// runNodeType loads "<prefix>.<action>" through the registry's DefSource —
// the path a workflow node takes — and runs it on a fake page.
func runNodeType(t *testing.T, prefix, actionName string) {
	t.Helper()
	reg, err := openAutomationRegistry()
	if err != nil {
		t.Fatal(err)
	}
	src := reg.DefSource()
	raw, err := src.Load(prefix, actionName)
	if err != nil {
		t.Fatalf("node %s.%s does not resolve: %v", prefix, actionName, err)
	}
	var def action.ActionDef
	if err := json.Unmarshal(raw, &def); err != nil {
		t.Fatal(err)
	}
	page := &legacyPage{present: map[string]bool{"h1": true}}
	ae := action.NewActionExecutor(context.Background(), page, nil, nil, nil, nil, zerolog.Nop())
	if pc := src.Package(prefix); pc != nil {
		ae.SetPackage(pc)
	}
	res, err := ae.ExecuteDef(&action.StorageAction{ID: "d5a", Type: actionName, TargetPlatform: prefix}, &def)
	if err != nil {
		t.Fatalf("node %s.%s failed: %v", prefix, actionName, err)
	}
	if res == nil || len(res.FailedItems) != 0 || page.hits == 0 {
		t.Fatalf("node %s.%s did not run its step: result %+v, element hits %d", prefix, actionName, res, page.hits)
	}
}

func TestWorkflowBundleLegacyAlias(t *testing.T) {
	for _, platform := range []string{"examplesite", "example_site"} {
		t.Run(platform, func(t *testing.T) {
			home1 := t.TempDir()
			t.Setenv("HOME", home1)
			writeLegacyHeading(t, home1, platform)
			reg, err := openAutomationRegistry() // seeding wraps the legacy dir
			if err != nil {
				t.Fatal(err)
			}
			pkgID := packageResolver(reg)(platform)
			if pkgID == "" {
				t.Fatalf("prefix %q resolves to no package", platform)
			}
			if _, err := reg.Info(pkgID); err != nil {
				t.Fatalf("resolved %q to %q, which is not installed: %v", platform, pkgID, err)
			}

			cfg := &globalConfig{DBPath: filepath.Join(t.TempDir(), "src.db"), JSONOutput: true, ProfileID: "default"}
			wf := `{"name":"legacy-wf","nodes":[
  {"id":"t","type":"trigger.manual","name":"Trigger","position":{"x":0,"y":0},"config":{}},
  {"id":"h","type":"` + platform + `.get_heading","name":"Heading","position":{"x":200,"y":0},"config":{}}
],"connections":[{"id":"t-h","source":"t","target":"h"}]}`
			src := filepath.Join(t.TempDir(), "wf.json")
			if err := os.WriteFile(src, []byte(wf), 0o644); err != nil {
				t.Fatal(err)
			}
			var imported struct {
				ID string `json:"id"`
			}
			if err := json.Unmarshal([]byte(runWorkflowSubcmd(t, cfg, "import", "--file", src)), &imported); err != nil {
				t.Fatal(err)
			}
			bundled := filepath.Join(t.TempDir(), "bundled.json")
			// A legacy package runs unrestricted and lists no sites; the
			// bundle names them (D8), here the registry's suggestion.
			p, err := reg.Get(pkgID)
			if err != nil || p.Manifest.Legacy == nil || len(p.Manifest.Legacy.SuggestedDomains) == 0 {
				t.Fatalf("legacy package %s has no suggested domains: %v", pkgID, err)
			}
			runWorkflowSubcmd(t, cfg, "export", imported.ID, "--bundle-automations", "-o", bundled,
				"--bundle-domains", pkgID+"="+strings.Join(p.Manifest.Legacy.SuggestedDomains, ","))
			raw, err := os.ReadFile(bundled)
			if err != nil {
				t.Fatal(err)
			}
			var doc workflowBundleFile
			if err := json.Unmarshal(raw, &doc); err != nil {
				t.Fatal(err)
			}
			if _, ok := doc.Automations[pkgID]; !ok || len(doc.Automations) != 1 {
				t.Fatalf("bundle holds %v, want exactly %s", keysOf(doc.Automations), pkgID)
			}
			// The bundled workflow names the package it ships, not the local alias.
			if doc.Nodes[1].Type != pkgID+".get_heading" || doc.Nodes[0].Type != "trigger.manual" {
				t.Fatalf("bundled node types: %q, %q", doc.Nodes[0].Type, doc.Nodes[1].Type)
			}
			// On the exporting machine the alias itself still runs.
			runNodeType(t, platform, "get_heading")

			// A fresh machine without the legacy directory.
			t.Setenv("HOME", t.TempDir())
			cfg2 := &globalConfig{DBPath: filepath.Join(t.TempDir(), "dst.db"), JSONOutput: true, ProfileID: "default"}
			var res struct {
				Automations []bundleImportItem `json:"automations"`
			}
			if err := json.Unmarshal([]byte(runWorkflowSubcmd(t, cfg2, "import", "--file", bundled, "--yes")), &res); err != nil {
				t.Fatal(err)
			}
			if len(res.Automations) != 1 || res.Automations[0].ID != pkgID || res.Automations[0].Status != "installed" {
				t.Fatalf("import report: %+v", res.Automations)
			}
			// The imported workflow's node runs from the imported package.
			runNodeType(t, pkgID, "get_heading")
		})
	}
}

// A disabled legacy package is hidden from the DefSource; the resolver still
// finds it through its recorded platform, so the bundle is not silently short.
func TestPackageResolverDisabledLegacy(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	writeLegacyHeading(t, home, "example_site")
	reg, err := openAutomationRegistry()
	if err != nil {
		t.Fatal(err)
	}
	id := packageResolver(reg)("example_site")
	if id == "" {
		t.Fatal("enabled legacy package not resolved")
	}
	if err := reg.SetEnabled(id, false); err != nil {
		t.Fatal(err)
	}
	if got := packageResolver(reg)("example_site"); got != id {
		t.Fatalf("disabled legacy package resolved to %q, want %q", got, id)
	}
	if got := packageResolver(reg)("core"); got != "" {
		t.Fatalf("core resolved to %q", got)
	}
}

func TestCanonicalNodeTypes(t *testing.T) {
	resolve := func(p string) string {
		return map[string]string{"foo": "local-foo", "hn": "hn", "bar": "local-bar"}[p]
	}
	in := []workflow.WorkflowFileNode{{Type: "foo.x"}, {Type: "hn.y"}, {Type: "bar.z"}, {Type: "core.set"}}
	got := canonicalNodeTypes(in, resolve, map[string]bundledAutomation{"local-foo": {}, "hn": {}})
	want := []string{"local-foo.x", "hn.y", "bar.z", "core.set"} // bar: not bundled, left alone
	for i, w := range want {
		if got[i].Type != w {
			t.Fatalf("node %d = %q, want %q", i, got[i].Type, w)
		}
	}
	if in[0].Type != "foo.x" {
		t.Fatal("input nodes were mutated")
	}
}

// A legacy package whose sites cannot be worked out (no literal navigate)
// cannot be installed elsewhere; the bundle export fails with registry's
// explanation instead of writing a bundle that would not import.
func TestWorkflowBundleLegacyWithoutDomainsFails(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	dir := filepath.Join(home, ".monoagent", "actions", "nosite")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := `{"actionType":"get_heading","platform":"nosite","steps":[{"id":"h","type":"find_element","selector":"h1"}]}`
	if err := os.WriteFile(filepath.Join(dir, "get_heading.json"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := &globalConfig{DBPath: filepath.Join(t.TempDir(), "src.db"), JSONOutput: true, ProfileID: "default"}
	src := filepath.Join(t.TempDir(), "wf.json")
	wf := `{"name":"nosite-wf","nodes":[{"id":"h","type":"nosite.get_heading","name":"H","position":{"x":0,"y":0},"config":{}}],"connections":[]}`
	if err := os.WriteFile(src, []byte(wf), 0o644); err != nil {
		t.Fatal(err)
	}
	var imported struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal([]byte(runWorkflowSubcmd(t, cfg, "import", "--file", src)), &imported); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(t.TempDir(), "bundled.json")
	var runErr error
	captureStdout(t, func() {
		cmd := newWorkflowCmd(cfg)
		cmd.SetArgs([]string{"export", imported.ID, "--bundle-automations", "-o", out})
		cmd.SilenceUsage, cmd.SilenceErrors = true, true
		runErr = cmd.Execute()
	})
	if runErr == nil || !strings.Contains(runErr.Error(), "cannot export") || !strings.Contains(runErr.Error(), "--bundle-domains local-nosite=") {
		t.Fatalf("export of a domainless legacy package: err = %v", runErr)
	}
	if b, err := os.ReadFile(out); err == nil && len(b) > 0 {
		var doc workflowBundleFile
		if json.Unmarshal(b, &doc) == nil && len(doc.Automations) > 0 {
			t.Fatal("a bundle with an uninstallable package was written")
		}
	}
}

func TestParseBundleDomains(t *testing.T) {
	got, err := parseBundleDomains([]string{"local-a=a.test, www.a.test", "b=b.test"})
	if err != nil || strings.Join(got["local-a"], ",") != "a.test,www.a.test" || len(got["b"]) != 1 {
		t.Fatalf("got %v, %v", got, err)
	}
	for _, bad := range []string{"noequals", "=a.test", "x="} {
		if _, err := parseBundleDomains([]string{bad}); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}
