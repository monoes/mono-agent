package health

import (
	"context"
	"errors"
	"testing"
)

func autoEnv(info *AutomationsInfo, err error) *Env {
	return &Env{Automations: func(context.Context) (*AutomationsInfo, error) { return info, err }}
}

func TestAutomationPackagesCheck(t *testing.T) {
	ctx := context.Background()
	if r := checkAutomationPackages(ctx, &Env{}); r.Status != StatusSkip {
		t.Fatalf("no hook: %+v", r)
	}
	if r := checkAutomationPackages(ctx, autoEnv(&AutomationsInfo{}, nil)); r.Status != StatusInfo {
		t.Fatalf("none installed: %+v", r)
	}
	r := checkAutomationPackages(ctx, autoEnv(&AutomationsInfo{Installed: 3,
		Unavailable: []UnavailableAutomation{{ID: "hn", Reason: "needs a newer engine"}}}, nil))
	if r.Status != StatusWarn || r.Summary != "1 of 3 unavailable" || len(r.Children) != 1 ||
		r.Children[0].ID != "automations.packages.hn" || r.Children[0].Summary != "needs a newer engine" {
		t.Fatalf("unavailable: %+v", r)
	}
	if r := checkAutomationPackages(ctx, autoEnv(nil, errors.New("boom"))); r.Status != StatusSkip || r.Detail != "boom" {
		t.Fatalf("error: %+v", r)
	}
}

func TestAutomationSelectorsCheck(t *testing.T) {
	ctx := context.Background()
	if r := checkAutomationSelectors(ctx, autoEnv(&AutomationsInfo{Installed: 2}, nil)); r.Status != StatusOK {
		t.Fatalf("clean: %+v", r)
	}
	r := checkAutomationSelectors(ctx, autoEnv(&AutomationsInfo{Installed: 2, Selectors: []SelectorProblem{
		{AutomationID: "linkedin", Key: "post.send", Status: "broken"},
		{AutomationID: "x", Key: "reply.box", Status: "decaying"},
	}}, nil))
	if r.Status != StatusWarn || r.Summary != "1 broken, 1 decaying" || len(r.Children) != 2 {
		t.Fatalf("problems: %+v", r)
	}
	c := r.Children[0]
	if c.ID != "automations.selectors.linkedin.post_send" || c.Status != StatusWarn || c.FixID != FixRerecordSelector ||
		c.FixCommand != "monoagentcli automation rerecord linkedin post.send" {
		t.Fatalf("broken child: %+v", c)
	}
	if r.Children[1].Status != StatusInfo {
		t.Fatalf("decaying child: %+v", r.Children[1])
	}
	only := checkAutomationSelectors(ctx, autoEnv(&AutomationsInfo{Selectors: []SelectorProblem{{AutomationID: "x", Key: "k", Status: "decaying"}}}, nil))
	if only.Status != StatusInfo {
		t.Fatalf("decaying only should not warn: %+v", only)
	}
}

func TestAutomationChecksRegistered(t *testing.T) {
	reg := Default()
	found := map[string]bool{}
	for _, c := range reg.Checks() {
		found[c.ID] = true
	}
	if !found[CheckAutomationPackages] || !found[CheckAutomationSelectors] {
		t.Fatal("automation checks are not in the default registry")
	}
}
