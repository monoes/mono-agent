package nodes

import (
	"fmt"
	"testing"

	"github.com/monoes/mono-agent/internal/action"
	"github.com/monoes/mono-agent/internal/workflow"
)

// fakeSource is a DefSource with a fixed action list and no packages.
type fakeSource struct{ list []string }

func (f fakeSource) Load(a, t string) ([]byte, error) {
	return nil, fmt.Errorf("fake: %s/%s", a, t)
}
func (f fakeSource) List() ([]string, error)                         { return f.list, nil }
func (f fakeSource) Package(automation string) action.PackageContext { return nil }

func TestRegisterBrowserNodes_FromDefSource(t *testing.T) {
	action.SetDefSource(fakeSource{list: []string{
		"acme-crm/create_contact",
		"acme-crm/create_contact", // duplicates must not panic
		"local-widgets/scrape",
		"local-acme-crm/create_contact", // "acme-crm.create_contact" is taken
	}})
	t.Cleanup(func() { action.SetDefSource(nil) })

	r := workflow.NewNodeTypeRegistry()
	RegisterBrowserNodes(r)

	for _, nt := range []string{"acme-crm.create_contact", "local-widgets.scrape", "local-acme-crm.create_contact"} {
		if !r.Has(nt) {
			t.Errorf("node type %q not registered", nt)
		}
	}
	// A legacy ~/.monoagent/actions/widgets/scrape.json workflow node keeps
	// resolving after the file was wrapped into local-widgets.
	if !r.Has("widgets.scrape") {
		t.Error("legacy name widgets.scrape does not resolve")
	}
	f, ok := r.Get("widgets.scrape")
	if !ok {
		t.Fatal("widgets.scrape: no factory")
	}
	if got := f().(*BrowserNode).platform; got != "local-widgets" {
		t.Errorf("widgets.scrape runs automation %q, want local-widgets", got)
	}
	// The alias is hidden from the palette.
	for _, nt := range r.Types() {
		if nt == "widgets.scrape" {
			t.Error("alias listed in Types()")
		}
	}
}

// legacyPkg is a local-* package context that knows its original name.
type legacyPkg struct {
	action.PackageContext
	name string
}

func (p legacyPkg) LegacyPlatform() string { return p.name }

type legacySource struct {
	fakeSource
	legacy map[string]string
}

func (s legacySource) Package(id string) action.PackageContext {
	if n, ok := s.legacy[id]; ok {
		return legacyPkg{name: n}
	}
	return nil
}

// An upgraded ~/.monoagent/actions/google_maps/get_place.json becomes
// package local-google-maps; workflows still say google_maps.get_place.
func TestRegisterBrowserNodes_LegacyAliasUsesOriginalName(t *testing.T) {
	action.SetDefSource(legacySource{
		fakeSource: fakeSource{list: []string{"local-google-maps/get_place", "local-widgets/scrape"}},
		legacy:     map[string]string{"local-google-maps": "google_maps"},
	})
	t.Cleanup(func() { action.SetDefSource(nil) })

	r := workflow.NewNodeTypeRegistry()
	RegisterBrowserNodes(r)
	if !r.Has("google_maps.get_place") {
		t.Error("google_maps.get_place (the original name) does not resolve")
	}
	if f, ok := r.Get("google_maps.get_place"); ok {
		if got := f().(*BrowserNode).platform; got != "local-google-maps" {
			t.Errorf("runs automation %q, want local-google-maps", got)
		}
	}
	// No recorded name: the id without "local-", as before.
	if !r.Has("widgets.scrape") {
		t.Error("widgets.scrape does not resolve")
	}
}
