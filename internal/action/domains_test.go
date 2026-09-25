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
