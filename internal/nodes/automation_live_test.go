package nodes

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/monoes/mono-agent/internal/action"
	"github.com/monoes/mono-agent/internal/browser"
	"github.com/monoes/mono-agent/internal/workflow"
)

// livePkg is a package context with a trust tier and live-run flag.
type livePkg struct {
	action.PackageContext
	trust     string
	confirmed bool
}

func (p livePkg) Trust() string            { return p.trust }
func (p livePkg) LiveRunConfirmed() bool   { return p.confirmed }
func (p livePkg) ID() string               { return "acme" }
func (p livePkg) StartURL() string         { return "" }
func (p livePkg) Domains() []string        { return nil }
func (p livePkg) PermittedSteps() []string { return nil }

// liveSource serves actions of one package "acme" with the given effects.
type liveSource struct {
	pkg     action.PackageContext
	effects map[string]string // action → sideEffects ("-" = undeclared)
}

func (s liveSource) Load(a, t string) ([]byte, error) {
	e, ok := s.effects[t]
	if a != "acme" || !ok {
		return nil, fmt.Errorf("not found %s/%s", a, t)
	}
	if e == "-" {
		return []byte(`{"actionType":"` + t + `","steps":[]}`), nil
	}
	return []byte(`{"actionType":"` + t + `","sideEffects":"` + e + `","steps":[]}`), nil
}
func (s liveSource) List() ([]string, error) { return nil, nil }
func (s liveSource) Package(a string) action.PackageContext {
	if a == "acme" {
		return s.pkg
	}
	return nil
}

var liveEffects = map[string]string{"list": "read", "post": "write", "dm": "message", "wipe": "destructive", "old": "-"}

func TestCheckLiveRun(t *testing.T) {
	t.Cleanup(func() { action.SetDefSource(nil) })
	cases := []struct {
		pkg     action.PackageContext
		act     string
		blocked bool
	}{
		{livePkg{trust: "imported"}, "post", true},
		{livePkg{trust: "imported"}, "dm", true},
		{livePkg{trust: "imported"}, "wipe", true},
		{livePkg{trust: "imported"}, "old", true}, // undeclared counts as write
		{livePkg{trust: "imported"}, "list", false},
		{livePkg{trust: "imported", confirmed: true}, "post", false},
		{livePkg{trust: "builtin"}, "post", false},
		{livePkg{trust: "local"}, "post", false},
		{livePkg{trust: "recorded"}, "post", false},
		{livePkg{trust: ""}, "post", true}, // unreadable trust: fail closed
	}
	for _, c := range cases {
		action.SetDefSource(liveSource{pkg: c.pkg, effects: liveEffects})
		err := checkLiveRun("acme", c.act)
		if (err != nil) != c.blocked {
			t.Errorf("%+v %s: err = %v, want blocked=%v", c.pkg, c.act, err, c.blocked)
		}
		if err != nil && !strings.Contains(err.Error(), "monoagentcli automation trust acme --live") {
			t.Errorf("message should name the trust command: %v", err)
		}
	}
}

// countingProvider records GetPage calls; the gate must fire before any tab.
type countingProvider struct{ calls int }

func (p *countingProvider) GetPage(context.Context, string, string) (browser.PageInterface, error) {
	p.calls++
	return nil, fmt.Errorf("no browser in tests")
}

func TestBrowserNode_LiveRunGateBeforeBrowser(t *testing.T) {
	action.SetDefSource(liveSource{pkg: livePkg{trust: "imported"}, effects: liveEffects})
	prev := globalSessionProvider
	p := &countingProvider{}
	SetGlobalSessionProvider(p)
	t.Cleanup(func() { action.SetDefSource(nil); SetGlobalSessionProvider(prev) })

	_, err := NewBrowserNode("acme", "post").Execute(context.Background(), workflow.NodeInput{}, map[string]interface{}{})
	if err == nil || !strings.Contains(err.Error(), "--live") {
		t.Fatalf("unconfirmed imported write action ran: %v", err)
	}
	if p.calls != 0 {
		t.Fatalf("a browser tab was requested before the live-run check (%d calls)", p.calls)
	}
}

// A legacy action (no package, no registry) is never gated.
func TestCheckLiveRun_LegacyUngated(t *testing.T) {
	action.SetDefSource(nil)
	if err := checkLiveRun("instagram", "send_dms"); err != nil {
		t.Fatalf("legacy action gated: %v", err)
	}
}
