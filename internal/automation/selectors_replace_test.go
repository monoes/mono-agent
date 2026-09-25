package automation

import (
	"os"
	"strings"
	"testing"

	"github.com/monoes/mono-agent/internal/action"
)

func TestReplaceSelector(t *testing.T) {
	fresh := action.SelectorEntry{
		Candidates: []action.SelectorCandidate{{CSS: "[data-testid=email]"}, {Aria: &action.AriaSelector{Role: "textbox", Name: "Email"}}},
		Intent:     "Email field", VerifiedAt: "2026-09-25T21:00:00Z",
	}
	selector := func(r *Registry, key string) *action.SelectorEntry {
		p, err := r.Get("acme-crm")
		if err != nil {
			t.Fatal(err)
		}
		e, _ := p.Context().Selector(key)
		return e
	}

	t.Run("local package rewritten in place", func(t *testing.T) {
		r := newReg(t)
		if _, err := r.Install(acmeDir(t), InstallOptions{Source: SourceLocal}); err != nil {
			t.Fatal(err)
		}
		// A stale promotion for the key must not reorder the new entry.
		r.PromoteOverlayCandidate("acme-crm", "contact.email", action.SelectorCandidate{Aria: &action.AriaSelector{Role: "textbox", Name: "Email"}})
		g := generation(t, r)
		where, err := r.ReplaceSelector("acme-crm", "contact.email", fresh)
		if err != nil || where != WherePackage {
			t.Fatalf("where=%q err=%v", where, err)
		}
		if e := selector(r, "contact.email"); e.Candidates[0].CSS != "[data-testid=email]" || e.VerifiedAt != fresh.VerifiedAt {
			t.Errorf("not replaced: %+v", e)
		}
		info, _ := r.Info("acme-crm")
		if info.Version != "1.2.0" {
			t.Errorf("version changed: %s", info.Version)
		}
		p, _ := r.Get("acme-crm")
		h, _ := fsHash(p.FS)
		idx, _ := r.readIndex()
		if idx.Packages["acme-crm"].InstalledSha256 != h {
			t.Error("installed hash not refreshed")
		}
		if generation(t, r) == g {
			t.Error("generation not bumped")
		}
		if _, err := os.Stat(r.overlayPath("acme-crm")); !os.IsNotExist(err) {
			t.Error("overlay item for the key not dropped")
		}
		// Other entries keep their bytes.
		b, _ := os.ReadFile(info.Dir + "/selectors.json")
		if !strings.Contains(string(b), `"//tr[1]"`) {
			t.Errorf("other selectors lost: %s", b)
		}
	})

	t.Run("imported package goes to the overlay", func(t *testing.T) {
		r := newReg(t)
		if _, err := r.Install(acmeDir(t), InstallOptions{}); err != nil {
			t.Fatal(err)
		}
		before, _ := os.ReadFile(r.versionDir("acme-crm", "1.2.0") + "/selectors.json")
		where, err := r.ReplaceSelector("acme-crm", "contact.email", fresh)
		if err != nil || where != WhereOverlay {
			t.Fatalf("where=%q err=%v", where, err)
		}
		after, _ := os.ReadFile(r.versionDir("acme-crm", "1.2.0") + "/selectors.json")
		if string(before) != string(after) {
			t.Error("imported package files touched")
		}
		if e := selector(r, "contact.email"); e.Candidates[0].CSS != "[data-testid=email]" {
			t.Errorf("overlay not applied: %+v", e)
		}
		if info, _ := r.Info("acme-crm"); info.Modified {
			t.Error("overlay counted as a modification")
		}
		// A package update that changes the entry wins over the overlay.
		files := acmeFiles()
		files["automation.json"] = strings.Replace(files["automation.json"], `"1.2.0"`, `"1.3.0"`, 1)
		files["selectors.json"] = strings.Replace(files["selectors.json"], `input[name=email]`, `input#new-email`, 1)
		r.Install(writeTree(t, t.TempDir(), files), InstallOptions{})
		if e := selector(r, "contact.email"); e.Candidates[0].CSS != "input#new-email" {
			t.Errorf("stale re-record shadows the update: %+v", e)
		}
	})

	t.Run("built-in with local trust goes to the overlay", func(t *testing.T) {
		r := newReg(t)
		r.Seed(seedFS("1.0.0", "a"))
		src, _ := OpenDir(acmeDir(t))
		if _, err := r.AddAction("demo", src, "create_contact", InstallOptions{}); err != nil {
			t.Fatal(err)
		}
		if where, err := r.ReplaceSelector("demo", "contact.email", fresh); err != nil || where != WhereOverlay {
			t.Errorf("where=%q err=%v", where, err)
		}
	})

	t.Run("errors", func(t *testing.T) {
		r := newReg(t)
		r.Install(acmeDir(t), InstallOptions{})
		if _, err := r.ReplaceSelector("acme-crm", "no.such.key", fresh); err == nil {
			t.Error("unknown key accepted")
		}
		if _, err := r.ReplaceSelector("nope", "contact.email", fresh); err == nil {
			t.Error("unknown package accepted")
		}
		if _, err := r.ReplaceSelector("acme-crm", "contact.email", action.SelectorEntry{}); err == nil {
			t.Error("empty entry accepted")
		}
		bad := action.SelectorEntry{Candidates: []action.SelectorCandidate{{CSS: "a", Text: "b"}}}
		if _, err := r.ReplaceSelector("acme-crm", "contact.email", bad); err == nil {
			t.Error("two-kind candidate accepted")
		}
	})
}
