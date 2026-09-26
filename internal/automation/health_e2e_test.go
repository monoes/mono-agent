package automation

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/monoes/mono-agent/internal/action"
	"github.com/monoes/mono-agent/internal/browser"
	"github.com/rs/zerolog"
)

// End to end: a real ActionExecutor runs an installed package's action on
// a fake page where the first candidate of a selector misses and the second
// hits; the HealthObserver (temp DB, booted registry) records the heal and
// promotes the second candidate.

// e2ePage is a minimal page: a set of present CSS selectors and a URL.
// Methods the run does not need panic through the nil embedded interface.
type e2ePage struct {
	browser.PageInterface
	present map[string]bool
	url     string
}

type e2eElement struct{ browser.ElementHandle }

func (p *e2ePage) Element(sel string, _ time.Duration) (browser.ElementHandle, error) {
	if p.present[sel] {
		return e2eElement{}, nil
	}
	return nil, errors.New("element not found: " + sel)
}
func (p *e2ePage) ElementX(xp string, t time.Duration) (browser.ElementHandle, error) {
	return p.Element(xp, t)
}
func (p *e2ePage) Race(sels []string, _ time.Duration) (int, browser.ElementHandle, error) {
	for i, s := range sels {
		if p.present[s] {
			return i, e2eElement{}, nil
		}
	}
	return -1, nil, errors.New("none of the selectors matched")
}
func (p *e2ePage) GetURL() (string, error)                     { return p.url, nil }
func (p *e2ePage) Navigate(u string) error                     { p.url = u; return nil }
func (p *e2ePage) Timeout(time.Duration) browser.PageInterface { return p }
func (p *e2ePage) WaitLoad() error                             { return nil }
func (p *e2ePage) EvalCDP(string) (interface{}, error)         { return true, nil }

func e2eFiles() map[string]string {
	return map[string]string{
		"automation.json": `{
  "schema": "monoagent.automation/v1",
  "id": "heal-e2e",
  "name": "Heal E2E",
  "version": "1.0.0",
  "site": {"startUrl": "https://example.com/", "domains": ["example.com"]},
  "permissions": {"steps": ["find_element"], "scripts": [], "downloads": false},
  "actions": ["find_save"],
  "policy": {"tier": "standard"}
}
`,
		"actions/find_save.json": `{"actionType":"find_save","automation":"heal-e2e","sideEffects":"read","steps":[
  {"id":"save","type":"find_element","configKey":"save.button","timeout":0.2}
]}
`,
		"selectors.json": `{
  "save.button": {"candidates": [{"css": "#save-old"}, {"css": "button.save"}]}
}
`,
	}
}

func runHealE2E(t *testing.T, source string) (*Registry, SelectorHealth) {
	t.Helper()
	r := newReg(t)
	dir := writeTree(t, filepath.Join(t.TempDir(), "heal-e2e"), e2eFiles())
	if _, err := r.Install(dir, InstallOptions{Source: source}); err != nil {
		t.Fatalf("install (%s): %v", source, err)
	}
	// Boot: the observer promotes through the registry behind the DefSource.
	prev := action.CurrentDefSource()
	action.SetDefSource(r.DefSource())
	t.Cleanup(func() { action.SetDefSource(prev) })

	db := healthTestDB(t)
	obs := HealthObserver(db)
	defer obs.(interface{ Close() error }).Close()

	pkg, err := r.Get("heal-e2e")
	if err != nil {
		t.Fatal(err)
	}
	def, err := pkg.Action("find_save")
	if err != nil {
		t.Fatal(err)
	}
	page := &e2ePage{present: map[string]bool{"button.save": true}, url: "https://example.com/"}
	ae := action.NewActionExecutor(context.Background(), page, nil, nil, nil, nil, zerolog.Nop())
	ae.SetPackage(pkg.Context())
	ae.SetSelectorObserver(obs)
	if _, err := ae.ExecuteDef(&action.StorageAction{ID: "e2e", Type: "find_save", TargetPlatform: "heal-e2e"}, def); err != nil {
		t.Fatalf("run (%s): %v", source, err)
	}
	if err := obs.(interface{ Flush() error }).Flush(); err != nil {
		t.Fatal(err)
	}
	return r, healthRow(t, db, "heal-e2e", "save.button")
}

func assertHealedAndPromoted(t *testing.T, r *Registry, h SelectorHealth) {
	t.Helper()
	if h.OK != 1 || h.Healed != 1 || h.Fail != 0 || h.LastCandidateIndex != 1 || h.Recent != "h" {
		t.Fatalf("health row = %+v, want one healed success at index 1", h)
	}
	pkg, err := r.Get("heal-e2e")
	if err != nil {
		t.Fatal(err)
	}
	e, ok := pkg.Context().Selector("save.button")
	if !ok || len(e.Candidates) != 2 || e.Candidates[0].CSS != "button.save" || e.Candidates[1].CSS != "#save-old" {
		t.Fatalf("effective selector after the run = %+v, want button.save first", e)
	}
}

func TestHealthE2EImportedPromotesThroughOverlay(t *testing.T) {
	r, h := runHealE2E(t, SourceImported)
	assertHealedAndPromoted(t, r, h)
	// Package files untouched: the base entry still has the old order.
	pkg, _ := r.Get("heal-e2e")
	base, _, err := readPackageSelector(pkg.Dir, "save.button")
	if err != nil || base.Candidates[0].CSS != "#save-old" {
		t.Fatalf("imported package file changed: %+v %v", base, err)
	}
}

func TestHealthE2ELocalPromotesInPlace(t *testing.T) {
	r, h := runHealE2E(t, SourceLocal)
	assertHealedAndPromoted(t, r, h)
	pkg, _ := r.Get("heal-e2e")
	base, _, err := readPackageSelector(pkg.Dir, "save.button")
	if err != nil || base.Candidates[0].CSS != "button.save" {
		t.Fatalf("local package selectors.json not rewritten: %+v %v", base, err)
	}
}
