package action

import (
	"errors"
	"testing"
)

func TestHostAllowed(t *testing.T) {
	domains := []string{"news.ycombinator.com", "*.example.com"}
	cases := []struct {
		host string
		want bool
	}{
		{"news.ycombinator.com", true},
		{"NEWS.YCombinator.com", true},
		{"news.ycombinator.com:443", true},
		{"ycombinator.com", false},
		{"evil-news.ycombinator.com", false},
		{"example.com", true},
		{"a.b.example.com", true},
		{"example.com.evil.net", false},
		{"notexample.com", false},
		{"", false},
	}
	for _, c := range cases {
		if got := HostAllowed(c.host, domains); got != c.want {
			t.Errorf("HostAllowed(%q) = %v, want %v", c.host, got, c.want)
		}
	}
	if !HostAllowed("anything.net", nil) {
		t.Error("empty list must allow everything")
	}
}

func TestURLAllowed(t *testing.T) {
	d := []string{"*.x.com"}
	if err := URLAllowed("https://x.com/home", d); err != nil {
		t.Errorf("x.com: %v", err)
	}
	if err := URLAllowed("https://evil.com/", d); !errors.Is(err, ErrOffDomain) {
		t.Errorf("evil.com: got %v, want ErrOffDomain", err)
	}
	if err := URLAllowed("about:blank", d); err != nil {
		t.Errorf("about:blank: %v", err)
	}
	if err := URLAllowed("https://evil.com/", nil); err != nil {
		t.Errorf("no domains: %v", err)
	}
}

func TestNavigateOffDomainFailsRun(t *testing.T) {
	page := &markPage{url: "https://x.com/"}
	ae := newPkgExecutor(page, &fakePkg{id: "x", domains: []string{"x.com"}})
	def := &ActionDef{ActionType: "t", SideEffects: "read", Steps: []StepDef{
		{ID: "go", Type: "navigate", URL: "{{target}}", OnError: &ErrorHandlerDef{Action: ErrorActionContinue}},
		{ID: "after", Type: "log", Value: "should not run"},
	}}
	ae.SetVariable("target", "https://evil.com/")
	_, err := ae.ExecuteDef(&StorageAction{ID: "a", TargetPlatform: "x", Type: "t"}, def)
	if !errors.Is(err, ErrOffDomain) || !errors.Is(err, ErrAbort) {
		t.Fatalf("err = %v, want off_domain abort", err)
	}
	if len(page.navs) != 0 {
		t.Fatalf("navigated to %v", page.navs)
	}
	if ae.execCtx.GetStepResult("after") != nil {
		t.Fatal("step after an off-domain navigate ran")
	}
}

func TestPostStepDomainCheck(t *testing.T) {
	// The page drifts off-domain (e.g. a redirect) during a harmless step.
	page := &markPage{url: "https://evil.com/landing"}
	ae := newPkgExecutor(page, &fakePkg{id: "x", domains: []string{"x.com"}})
	def := &ActionDef{ActionType: "t", SideEffects: "read", Steps: []StepDef{{ID: "l", Type: "log", Value: "hi"}}}
	_, err := ae.ExecuteDef(&StorageAction{ID: "a", TargetPlatform: "x", Type: "t"}, def)
	if !errors.Is(err, ErrOffDomain) {
		t.Fatalf("err = %v, want off_domain", err)
	}
}

func TestHostAllowedPorts(t *testing.T) {
	d := []string{"localhost:8080", "*.acme.com"}
	for host, want := range map[string]bool{
		"localhost:8080": true, "localhost:9090": false, "localhost": false,
		"a.acme.com:444": true, "acme.com": true,
	} {
		if got := HostAllowed(host, d); got != want {
			t.Errorf("HostAllowed(%q) = %v, want %v", host, got, want)
		}
	}
	if URLAllowed("http://localhost:8080/x", d) != nil || URLAllowed("http://localhost:9999/", d) == nil {
		t.Error("URL port matching")
	}
	if URLAllowed("https://secure.test/", []string{"secure.test:443"}) != nil {
		t.Error("scheme default port must match an explicit :443 entry")
	}
}

func TestGlobalHostDeny(t *testing.T) {
	SetGlobalHostDeny(func(h string) (bool, string) { return h == "instagram.com", "social build required" })
	t.Cleanup(func() { SetGlobalHostDeny(nil) })
	if err := URLAllowed("https://instagram.com/p/1", nil); !errors.Is(err, ErrOffDomain) {
		t.Errorf("denied host with no domains: %v", err)
	}
	ae := newPkgExecutor(nil, nil)
	if err := ae.CheckURLAllowed("https://instagram.com/"); err == nil {
		t.Error("deny must apply without a package")
	}
	if err := ae.CheckURLAllowed("https://example.com/"); err != nil {
		t.Errorf("other host: %v", err)
	}
}

type urlErrPage struct{ markPage }

func (p *urlErrPage) GetURL() (string, error) { return "", errors.New("tab gone") }

func TestPageDomainFailsClosed(t *testing.T) {
	ae := newPkgExecutor(&urlErrPage{}, &fakePkg{id: "x", domains: []string{"x.com"}})
	if err := ae.checkPageDomain(); !errors.Is(err, ErrOffDomain) {
		t.Fatalf("unreadable URL: %v", err)
	}
}

type blankPage struct{ markPage }

func (p *blankPage) GetURL() (string, error) { return "", nil }

func TestPageDomainAllowsFreshTab(t *testing.T) {
	for _, page := range []interface{ GetURL() (string, error) }{&blankPage{}, &markPage{url: "about:blank"}} {
		var ae *ActionExecutor
		switch p := page.(type) {
		case *blankPage:
			ae = newPkgExecutor(p, &fakePkg{id: "x", domains: []string{"x.com"}})
		case *markPage:
			ae = newPkgExecutor(p, &fakePkg{id: "x", domains: []string{"x.com"}})
		}
		if err := ae.checkPageDomain(); err != nil {
			t.Errorf("%T: %v", page, err)
		}
	}
}
