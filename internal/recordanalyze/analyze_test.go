package recordanalyze

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/monoes/mono-agent/internal/automation"
)

func TestAnalyzeWritesDraftPackage(t *testing.T) {
	home := t.TempDir()
	rec := loadFixture(t, "form-submit")
	r := &stubRunner{answers: []string{answer(t, "form-submit")}}
	res, err := Analyze(context.Background(), rec, AnalyzeOptions{Home: home, Runner: r})
	if err != nil {
		t.Fatal(err)
	}
	if res.DraftDir != filepath.Join(home, "recording-drafts", "form-submit") {
		t.Errorf("draft dir = %s", res.DraftDir)
	}
	for _, f := range []string{"automation.json", "actions/create_contact.json", "selectors.json", "draft.json"} {
		if _, err := os.Stat(filepath.Join(res.DraftDir, f)); err != nil {
			t.Errorf("missing %s", f)
		}
	}
	d, err := ReadDraft(res.DraftDir)
	if err != nil {
		t.Fatal(err)
	}
	if d.RecordingID != "form-submit" || !d.IsNew || d.TargetAutomation != "acme-crm" || d.Action != "create_contact" ||
		d.SaveAs != SaveAsAction || d.Names.Fragment != "fill_contact_form" || len(d.Segments) != 1 {
		t.Errorf("draft = %+v", d)
	}
	if d.RecordedInputs["email"] != "jane@example.com" {
		t.Errorf("recorded inputs = %v", d.RecordedInputs)
	}
	if _, ok := d.RecordedInputs["account_password"]; ok {
		t.Error("secret leaked into recorded inputs")
	}
	var m automation.Manifest
	b, _ := os.ReadFile(filepath.Join(res.DraftDir, "automation.json"))
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	if m.ID != "acme-crm" || m.Site.StartURL != "https://app.acme-crm.test/contacts/new" ||
		len(m.Site.Domains) != 1 || m.Login != nil || len(m.Permissions.Steps) != 3 {
		t.Errorf("manifest = %+v", m)
	}
	var act map[string]any
	b, _ = os.ReadFile(filepath.Join(res.DraftDir, "actions", "create_contact.json"))
	_ = json.Unmarshal(b, &act)
	prov, _ := act["provenance"].(map[string]any)
	if prov["recording"] != "form-submit" || act["automation"] != "acme-crm" {
		t.Errorf("action = %v", act)
	}
	if p, err := automation.OpenDir(res.DraftDir); err != nil {
		t.Errorf("draft does not open as a package: %v", err)
	} else if issues := automation.Validate(p); hasErrorIssue(issues) {
		t.Errorf("draft package invalid: %+v", issues)
	}
}

func hasErrorIssue(issues []automation.IssueJSON) bool {
	for _, is := range issues {
		if is.Severity == "error" {
			return true
		}
	}
	return false
}

func TestAnalyzeLoginManifest(t *testing.T) {
	home := t.TempDir()
	rec := loadFixture(t, "login-multipage")
	res, err := Analyze(context.Background(), rec, AnalyzeOptions{Home: home, Runner: &stubRunner{answers: []string{answer(t, "login-multipage")}}})
	if err != nil {
		t.Fatal(err)
	}
	p, err := automation.OpenDir(res.DraftDir)
	if err != nil {
		t.Fatal(err)
	}
	m := p.Manifest
	if m.Login == nil || m.Login.URL != "https://shop.example.test/login" || m.Login.LoggedIn == nil ||
		m.Login.LoggedIn.Selector != "nav.account" || m.Login.SessionTTLDays != 30 {
		t.Errorf("login = %+v", m.Login)
	}
	if m.Site.StartURL != "https://shop.example.test/account" {
		t.Errorf("start = %s", m.Site.StartURL)
	}
	if len(res.Draft.Segments) != 4 {
		t.Errorf("segments = %+v", res.Draft.Segments)
	}
}

func TestPromptCarriesAnalysis(t *testing.T) {
	rec := loadFixture(t, "form-submit")
	a := Detect(Normalize(rec.Summary, rec.Events))
	p, err := BuildPrompt(a, &Env{Analysis: a}, rec.Snippets)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"# Record → Action", `"mode": "new-automation"`, `"detectedInputs"`, `data-testid=\"contact-save\"`, "create a contact"} {
		if !strings.Contains(p, want) {
			t.Errorf("prompt lacks %q", want)
		}
	}
}
