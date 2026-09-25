package recordanalyze

import (
	"strings"
	"testing"

	"github.com/monoes/mono-agent/internal/action"
	"github.com/monoes/mono-agent/internal/automation"
)

func lintFixture(t *testing.T, fixture string, mutate func(*Output)) []automation.IssueJSON {
	t.Helper()
	rec := loadFixture(t, fixture)
	env := &Env{Analysis: Detect(Normalize(rec.Summary, rec.Events))}
	out, probs := parseAndCheck(answer(t, fixture), env)
	if len(probs) > 0 {
		t.Fatalf("answer problems: %v", probs)
	}
	if mutate != nil {
		mutate(out)
	}
	return Lint(out, env, BuildManifest(out, env), rec.Snippets)
}

func codes(issues []automation.IssueJSON, sev string) map[string]string {
	m := map[string]string{}
	for _, is := range issues {
		if is.Severity == sev {
			m[is.Code] = is.StepID + ": " + is.Message
		}
	}
	return m
}

func TestLintCleanFixtures(t *testing.T) {
	for _, f := range []string{"form-submit", "list-scrape", "login-multipage"} {
		if errs := codes(lintFixture(t, f, nil), "error"); len(errs) > 0 {
			t.Errorf("%s: unexpected errors %v", f, errs)
		}
	}
}

func TestLintSelectorResolution(t *testing.T) {
	issues := lintFixture(t, "form-submit", func(o *Output) {
		o.Selectors["contact.name_input"] = action.SelectorEntry{Candidates: []action.SelectorCandidate{{CSS: "input"}}}
		o.Selectors["contact.email_input"] = action.SelectorEntry{Candidates: []action.SelectorCandidate{{CSS: "#does-not-exist"}}}
		o.Selectors["contact.save_button"] = action.SelectorEntry{Candidates: []action.SelectorCandidate{{XPath: "//button"}}}
	})
	errs, warns := codes(issues, "error"), codes(issues, "warning")
	if !strings.HasPrefix(errs["selector_ambiguous"], "name:") {
		t.Errorf("ambiguous: %v", errs)
	}
	if !strings.HasPrefix(errs["selector_unresolved"], "email:") {
		t.Errorf("unresolved: %v", errs)
	}
	if !strings.HasPrefix(warns["selector_unverified"], "save:") {
		t.Errorf("unverified: %v", warns)
	}
}

func TestLintAriaAndTextCandidates(t *testing.T) {
	issues := lintFixture(t, "form-submit", func(o *Output) {
		o.Selectors["contact.email_input"] = action.SelectorEntry{Candidates: []action.SelectorCandidate{{Aria: &action.AriaSelector{Role: "textbox", Name: "Password"}}}}
		o.Selectors["contact.save_button"] = action.SelectorEntry{Candidates: []action.SelectorCandidate{{Text: "Save"}}}
	})
	if errs := codes(issues, "error"); len(errs) > 0 {
		t.Errorf("aria/text candidates should resolve: %v", errs)
	}
}

func TestLintLiteralSecret(t *testing.T) {
	issues := lintFixture(t, "form-submit", func(o *Output) {
		o.Action.Steps[3].Value = "hunter2"
	})
	if !strings.HasPrefix(codes(issues, "error")["literal_secret"], "password:") {
		t.Errorf("masked literal not caught: %v", issues)
	}
	issues = lintFixture(t, "form-submit", func(o *Output) {
		o.Action.Steps[2].Value = "sk-abcdefghijklmnopqrstuvwxyz"
	})
	if !strings.HasPrefix(codes(issues, "error")["literal_secret"], "name:") {
		t.Errorf("token literal not caught: %v", issues)
	}
}

func TestLintOffDomain(t *testing.T) {
	issues := lintFixture(t, "form-submit", func(o *Output) {
		o.Action.Steps[0].URL = "https://evil.test/phish"
	})
	if !strings.HasPrefix(codes(issues, "error")["off_domain"], "open:") {
		t.Errorf("off-domain navigate not caught: %v", issues)
	}
	issues = lintFixture(t, "form-submit", func(o *Output) {
		o.Action.Steps[0].URL = "{{contact_url}}"
	})
	if _, ok := codes(issues, "error")["off_domain"]; ok {
		t.Error("templated URL flagged")
	}
}

func TestLintScriptWarning(t *testing.T) {
	issues := lintFixture(t, "list-scrape", func(o *Output) {
		o.Scripts = map[string]string{"parse.js": "return 1"}
		o.Action.Steps = append(o.Action.Steps, action.StepDef{ID: "js", Type: "page_script", Script: "parse.js"})
	})
	if _, ok := codes(issues, "warning")["script_used"]; !ok {
		t.Errorf("script warning missing: %v", issues)
	}
}

func TestDomainAllowed(t *testing.T) {
	d := []string{"app.x.com", "*.y.com"}
	for h, want := range map[string]bool{"app.x.com": true, "x.com": false, "y.com": true, "a.b.y.com": true, "evil.com": false} {
		if domainAllowed(h, d) != want {
			t.Errorf("%s: want %v", h, want)
		}
	}
}
