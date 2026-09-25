package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"reflect"
	"sort"
	"strings"
	"time"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/launcher"
	"github.com/go-rod/rod/lib/proto"
	"github.com/monoes/mono-agent/internal/action"
	"github.com/monoes/mono-agent/internal/automation"
	"github.com/monoes/mono-agent/internal/browser"
	"github.com/rs/zerolog"
	"github.com/spf13/cobra"
)

// `automation test`: validate the package, then run each fixture
// (tests/fixtures/<name>.html, <name> = <action>[.<variant>]) in a headless
// browser: the action runs through action.ExecuteDef in safe mode on a page
// that serves the fixture for every document URL, and its records must equal
// tests/<name>.expect.json. Inputs come from tests/<name>.inputs.json, else
// from same-named fields of the first expected record. Anything that stops
// the comparison (no browser, no expect file, missing inputs, a side-effect
// stop) is reported "skipped", never "pass".

// fixtureResult is one row of `automation test --json` "results".
// Status is pass | fail | skipped; OK is false only for fail, so a missing
// browser never fails CI.
type fixtureResult struct {
	Action  string `json:"action"`
	Fixture string `json:"fixture"`
	OK      bool   `json:"ok"`
	Status  string `json:"status"`
	Message string `json:"message"`
}

// fixtureRun is what running one action against one fixture produced.
type fixtureRun struct {
	items    []map[string]interface{}
	safeStop *action.SafeStop
	err      error
}

// fixtureRunner runs def (inputs as params) on a page that answers every
// document request with html. nil means no browser is available.
type fixtureRunner func(pkg *automation.Package, def *action.ActionDef, html string, inputs map[string]interface{}) fixtureRun

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
				if !r.OK {
					failed++
				}
				table.Append([]string{r.Action, orDash(r.Fixture), r.Status, truncateStr(r.Message, 80)})
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
	status := "pass"
	if errs > 0 {
		status = "fail"
	}
	results = append(results, fixtureResult{Action: valAction, OK: errs == 0, Status: status, Message: msg})

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
	r := fixtureResult{Action: fx.action, Fixture: fx.name, OK: true, Status: "skipped"}
	def, err := pkg.Action(fx.action)
	if err != nil {
		r.OK, r.Status, r.Message = false, "fail", "no such action: "+err.Error()
		return r
	}
	if fx.expectPath == "" {
		r.Message = "skipped: no tests/" + fx.name + ".expect.json"
		return r
	}
	var want interface{}
	if b, err := fs.ReadFile(pkg.FS, fx.expectPath); err != nil || json.Unmarshal(b, &want) != nil {
		r.OK, r.Status, r.Message = false, "fail", "unreadable "+fx.expectPath
		return r
	}
	inputs, missing := fixtureInputs(pkg.FS, fx.name, def, want)
	if len(missing) > 0 {
		r.Message = "skipped: no value for required input(s) " + strings.Join(missing, ", ") +
			" (add tests/" + fx.name + ".inputs.json)"
		return r
	}
	if run == nil {
		r.Message = "skipped: no browser"
		return r
	}
	html, err := fs.ReadFile(pkg.FS, fx.htmlPath)
	if err != nil {
		r.OK, r.Status, r.Message = false, "fail", err.Error()
		return r
	}
	got := run(pkg, def, string(html), inputs)
	switch {
	case got.safeStop != nil:
		r.Message = "skipped: stopped before side-effect step " + got.safeStop.StepID
	case got.err != nil:
		r.OK, r.Status, r.Message = false, "fail", got.err.Error()
	case outputMatches(got.items, want):
		r.Status, r.Message = "pass", fmt.Sprintf("%d record(s) match %s", len(got.items), fx.expectPath)
	default:
		g, _ := json.Marshal(got.items)
		r.OK, r.Status, r.Message = false, "fail", "output differs from "+fx.expectPath+": got "+truncateStr(string(g), 300)
	}
	return r
}

// fixtureInputs builds the run's params: tests/<name>.inputs.json when
// present, then same-named fields of the first expected record for any
// required input still missing. Returns the required inputs left unset.
func fixtureInputs(fsys fs.FS, name string, def *action.ActionDef, want interface{}) (map[string]interface{}, []string) {
	inputs := map[string]interface{}{}
	if b, err := fs.ReadFile(fsys, "tests/"+name+".inputs.json"); err == nil {
		_ = json.Unmarshal(b, &inputs)
	}
	var first map[string]interface{}
	switch w := want.(type) {
	case []interface{}:
		if len(w) > 0 {
			first, _ = w[0].(map[string]interface{})
		}
	case map[string]interface{}:
		first = w
	}
	var missing []string
	if def.Inputs == nil {
		return inputs, nil
	}
	for _, in := range parseInputs(def.Inputs.Required, true) {
		if _, ok := inputs[in.Name]; ok {
			continue
		}
		if v, ok := first[in.Name]; ok {
			inputs[in.Name] = v
			continue
		}
		missing = append(missing, in.Name)
	}
	return inputs, missing
}

// outputMatches compares through JSON (numbers and nulls as a node's output
// serialises them). The expectation is either the list of records, or one
// record (a single-record run, or the records under their output name, e.g.
// {"items":[…]}).
func outputMatches(items []map[string]interface{}, want interface{}) bool {
	norm := func(v interface{}) interface{} {
		b, _ := json.Marshal(v)
		var out interface{}
		_ = json.Unmarshal(b, &out)
		return out
	}
	w := norm(want)
	if items == nil {
		items = []map[string]interface{}{}
	}
	if reflect.DeepEqual(norm(items), w) {
		return true
	}
	if len(items) == 1 && reflect.DeepEqual(norm(items[0]), w) {
		return true
	}
	if m, ok := w.(map[string]interface{}); ok && len(m) == 1 {
		for _, v := range m {
			return reflect.DeepEqual(norm(items), v)
		}
	}
	return false
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
	return func(pkg *automation.Package, def *action.ActionDef, html string, inputs map[string]interface{}) fixtureRun {
		page, err := b.Page(proto.TargetCreateTarget{URL: "about:blank"})
		if err != nil {
			return fixtureRun{err: err}
		}
		defer page.Close()
		// Every document is the fixture; nothing else leaves the machine.
		router := page.HijackRequests()
		router.MustAdd("*", func(h *rod.Hijack) {
			if h.Request.Type() == proto.NetworkResourceTypeDocument {
				h.Response.SetHeader("Content-Type", "text/html; charset=utf-8")
				h.Response.SetBody(html)
				return
			}
			h.Response.SetBody("")
		})
		go router.Run()
		defer router.Stop()

		ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
		defer cancel()
		ae := action.NewActionExecutor(ctx, browser.NewRodPage(page.Context(ctx)), nil, nil, nil, nil, zerolog.Nop())
		ae.SetPackage(pkg.Context())
		ae.SetSafeMode(true)
		res, err := ae.ExecuteDef(&action.StorageAction{ID: "fixture-test", Type: def.ActionType,
			TargetPlatform: pkg.Manifest.ID, Params: inputs}, def)
		out := fixtureRun{err: err, safeStop: ae.SafeStopped()}
		if res != nil {
			out.items = res.ExtractedItems
		}
		return out
	}, closeFn
}
