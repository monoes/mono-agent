package main

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"sort"
	"strings"
	"time"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/launcher"
	"github.com/go-rod/rod/lib/proto"
	"github.com/monoes/mono-agent/internal/action"
	"github.com/monoes/mono-agent/internal/automation"
	"github.com/spf13/cobra"
)

// `automation test`: validate the package, then check each fixture
// (tests/fixtures/<action>[.<variant>].html) in a headless browser: every
// selector the action's extract steps use must match in the snapshot. With
// no browser on the machine each fixture is reported "skipped: no browser"
// and counts as ok, so CI never fails for a missing Chrome.

// fixtureResult is one row of `automation test --json` "results".
type fixtureResult struct {
	Action  string `json:"action"`
	Fixture string `json:"fixture"`
	OK      bool   `json:"ok"`
	Message string `json:"message"`
}

// fixtureRunner checks one fixture. nil means no browser is available.
type fixtureRunner func(html string, selectors []fixtureSelector) (missing []string, err error)

type fixtureSelector struct {
	label, css, xpath string
}

func newAutomationTestCmd(cfg *globalConfig) *cobra.Command {
	var live bool
	cmd := &cobra.Command{
		Use:   "test <id|dir> [action]",
		Short: "Validate an automation and check its fixture snapshots",
		Args:  cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			if live {
				return errors.New("automation test --live is not supported yet; use 'record verify' to replay against the browser")
			}
			pkg, err := openTestTarget(args[0])
			if err != nil {
				return err
			}
			only := ""
			if len(args) == 2 {
				only = args[1]
			}
			runner, closeRunner := newBrowserFixtureRunner()
			defer closeRunner()
			results := runAutomationTests(pkg, only, runner)
			out := cmd.OutOrStdout()
			if cfg.JSONOutput {
				return writeJSONTo(out, map[string]any{"results": results})
			}
			failed := 0
			table := newPlainTable(out, []string{"Action", "Fixture", "Result", "Message"}, nil)
			for _, r := range results {
				res := "ok"
				if !r.OK {
					res, failed = "FAIL", failed+1
				}
				table.Append([]string{r.Action, orDash(r.Fixture), res, truncateStr(r.Message, 80)})
			}
			table.Render()
			if failed > 0 {
				return fmt.Errorf("%d test(s) failed", failed)
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&live, "live", false, "Run against the real site in the browser (not supported yet)")
	return cmd
}

// openTestTarget accepts an installed id or a package directory.
func openTestTarget(target string) (*automation.Package, error) {
	if st, err := os.Stat(target); err == nil && st.IsDir() {
		return automation.OpenDir(target)
	}
	reg, err := openAutomationRegistry()
	if err != nil {
		return nil, err
	}
	return reg.Get(target)
}

// runAutomationTests returns the validation row followed by one row per
// fixture of the selected action(s). Always non-nil.
func runAutomationTests(pkg *automation.Package, only string, run fixtureRunner) []fixtureResult {
	results := []fixtureResult{}
	issues := automation.Validate(pkg)
	errs, warns := 0, 0
	for _, is := range issues {
		if is.Severity == "error" {
			errs++
		} else {
			warns++
		}
	}
	msg := fmt.Sprintf("validate: %d error(s), %d warning(s)", errs, warns)
	if errs > 0 {
		for _, is := range issues {
			if is.Severity == "error" {
				msg += "; " + is.File + " " + is.Code + ": " + is.Message
				break
			}
		}
	}
	valAction := only
	if valAction == "" {
		valAction = "*"
	}
	results = append(results, fixtureResult{Action: valAction, OK: errs == 0, Message: msg})

	for _, fx := range listFixtures(pkg.FS) {
		if only != "" && fx.action != only {
			continue
		}
		results = append(results, runFixture(pkg, fx, run))
	}
	return results
}

type fixtureFile struct {
	action, name, htmlPath, expectPath string
}

// listFixtures pairs tests/fixtures/<name>.html with tests/<name>.expect.json.
// The action is the part of <name> before the first dot.
func listFixtures(fsys fs.FS) []fixtureFile {
	var out []fixtureFile
	if fsys == nil {
		return out
	}
	entries, _ := fs.ReadDir(fsys, "tests/fixtures")
	for _, e := range entries {
		if e.IsDir() || path.Ext(e.Name()) != ".html" {
			continue
		}
		name := strings.TrimSuffix(e.Name(), ".html")
		act, _, _ := strings.Cut(name, ".")
		fx := fixtureFile{action: act, name: name, htmlPath: "tests/fixtures/" + e.Name()}
		if _, err := fs.Stat(fsys, "tests/"+name+".expect.json"); err == nil {
			fx.expectPath = "tests/" + name + ".expect.json"
		}
		out = append(out, fx)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].name < out[j].name })
	return out
}

func runFixture(pkg *automation.Package, fx fixtureFile, run fixtureRunner) fixtureResult {
	r := fixtureResult{Action: fx.action, Fixture: fx.name}
	def, err := pkg.Action(fx.action)
	if err != nil {
		r.Message = "no such action: " + err.Error()
		return r
	}
	if fx.expectPath == "" {
		r.Message = "warning: no " + "tests/" + fx.name + ".expect.json; "
	}
	sels := fixtureSelectors(def.Steps, pkg.Context())
	if len(sels) == 0 {
		r.OK = true
		r.Message += "skipped: action has no extract selectors to check"
		return r
	}
	if run == nil {
		r.OK = true
		r.Message += "skipped: no browser"
		return r
	}
	html, err := fs.ReadFile(pkg.FS, fx.htmlPath)
	if err != nil {
		r.Message += err.Error()
		return r
	}
	missing, err := run(string(html), sels)
	switch {
	case err != nil:
		r.Message += "browser: " + err.Error()
	case len(missing) > 0:
		r.Message += "no match in fixture: " + strings.Join(missing, ", ")
	default:
		r.OK = true
		r.Message += fmt.Sprintf("%d selector(s) matched", len(sels))
	}
	return r
}

// fixtureSelectors collects the selectors of extract_* steps (nested steps
// included). A configKey resolves to its first css/xpath candidate.
func fixtureSelectors(steps []action.StepDef, ctx action.PackageContext) []fixtureSelector {
	var out []fixtureSelector
	for _, s := range steps {
		out = append(out, fixtureSelectors(s.Steps, ctx)...)
		if !strings.HasPrefix(s.Type, "extract_") {
			continue
		}
		label := s.ID
		switch {
		case s.XPath != "":
			out = append(out, fixtureSelector{label: label, xpath: s.XPath})
		case s.Selector != "":
			out = append(out, fixtureSelector{label: label, css: s.Selector})
		case s.ConfigKey != "" && ctx != nil:
			if e, ok := ctx.Selector(s.ConfigKey); ok {
				for _, c := range e.Candidates {
					if c.CSS != "" || c.XPath != "" {
						out = append(out, fixtureSelector{label: label + " (" + s.ConfigKey + ")", css: c.CSS, xpath: c.XPath})
						break
					}
				}
			}
		}
	}
	return out
}

// newBrowserFixtureRunner starts a headless browser only if one is already
// installed (launcher.LookPath; rod's own download is never triggered).
// Returns a nil runner when none is found.
func newBrowserFixtureRunner() (fixtureRunner, func()) {
	noop := func() {}
	if os.Getenv("MONOAGENT_TEST_NO_BROWSER") != "" {
		return nil, noop
	}
	bin, found := launcher.LookPath()
	if !found {
		return nil, noop
	}
	l := launcher.New().Bin(bin).Headless(true)
	u, err := l.Launch()
	if err != nil {
		return nil, noop
	}
	b := rod.New().ControlURL(u)
	if err := b.Connect(); err != nil {
		l.Kill()
		return nil, noop
	}
	closeFn := func() { b.Close(); l.Kill() }
	return func(html string, sels []fixtureSelector) ([]string, error) {
		page, err := b.Page(proto.TargetCreateTarget{URL: "about:blank"})
		if err != nil {
			return nil, err
		}
		defer page.Close()
		page = page.Timeout(20 * time.Second)
		if err := page.SetDocumentContent(html); err != nil {
			return nil, err
		}
		var missing []string
		for _, s := range sels {
			if !fixtureMatches(page, s) {
				missing = append(missing, s.label)
			}
		}
		return missing, nil
	}, closeFn
}

func fixtureMatches(page *rod.Page, s fixtureSelector) bool {
	if s.xpath != "" {
		els, err := page.ElementsX(s.xpath)
		return err == nil && len(els) > 0
	}
	has, _, err := page.Has(s.css)
	return err == nil && has
}
