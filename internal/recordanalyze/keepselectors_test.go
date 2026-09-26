package recordanalyze

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/monoes/mono-agent/internal/action"
)

// olderDraft is the form-submit draft with a different Save selector than
// the package now has (the package's was re-recorded after this draft).
func olderDraft(t *testing.T) string {
	ans := strings.Replace(answer(t, "form-submit"),
		`"contact.save_button": {"intent": "Save button", "candidates": [{"css": "[data-testid=\"contact-save\"]", "score": 0.95}`,
		`"contact.save_button": {"intent": "Save button", "candidates": [{"css": "form#new-contact button[type=submit]", "score": 0.9}`, 1)
	if !strings.Contains(ans, "form#new-contact button[type=submit]") {
		t.Fatal("fixture replace failed")
	}
	return draftFrom(t, t.TempDir(), "form-submit", ans)
}

func TestSaveKeepPackageSelectors(t *testing.T) {
	home := t.TempDir()
	reg := openReg(t, home)
	if _, err := Save(context.Background(), reg, draftFrom(t, home, "form-submit", answer(t, "form-submit")), SaveOptions{}); err != nil {
		t.Fatal(err)
	}
	for _, as := range []string{SaveAsAction, SaveAsFragment} {
		old := olderDraft(t)
		opts := SaveOptions{As: as, Automation: "acme-crm", Name: "older_" + as}

		_, err := Save(context.Background(), reg, old, opts)
		if err == nil || !strings.Contains(err.Error(), "contact.save_button") || !strings.Contains(err.Error(), "--keep-package-selectors") {
			t.Fatalf("%s without flag: err = %v", as, err)
		}

		opts.KeepPackageSelectors = true
		res, err := Save(context.Background(), reg, old, opts)
		if err != nil {
			t.Fatalf("%s with flag: %v", as, err)
		}
		if !strings.Contains(strings.Join(res.Warnings, " "), "kept the package's current selector(s): contact.save_button") {
			t.Errorf("%s warnings = %v", as, res.Warnings)
		}
		p, _ := reg.Get("acme-crm")
		sels, _ := p.Selectors()
		if got := sels["contact.save_button"].Candidates[0].CSS; got != `[data-testid="contact-save"]` {
			t.Errorf("%s: package selector replaced by the draft's: %q", as, got)
		}
		// The draft on disk is untouched.
		draftSels := map[string]action.SelectorEntry{}
		if err := readJSON(filepath.Join(old, "selectors.json"), &draftSels); err != nil {
			t.Fatal(err)
		}
		if got := draftSels["contact.save_button"].Candidates[0].CSS; got != "form#new-contact button[type=submit]" {
			t.Errorf("%s: draft rewritten: %q", as, got)
		}
	}
}
