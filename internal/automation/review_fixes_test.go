package automation

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/monoes/mono-agent/internal/action"
)

func generation(t *testing.T, r *Registry) string {
	t.Helper()
	return r.DefSource().(interface{ Generation() string }).Generation()
}

// H2: a user change to a built-in keeps it a built-in and never hides the
// next release.
func TestAddActionToBuiltinKeepsSeedLineage(t *testing.T) {
	r := newReg(t)
	if err := r.Seed(seedFS("1.0.0", "a")); err != nil {
		t.Fatal(err)
	}
	src, _ := OpenDir(acmeDir(t))
	// action template install passes Source local: it must not flip the source.
	if _, err := r.AddAction("demo", src, "create_contact", InstallOptions{Source: SourceLocal}); err != nil {
		t.Fatal(err)
	}
	info, _ := r.Info("demo")
	if info.Source != SourceBuiltin || info.Version != "1.0.0+local.1" || !info.Modified {
		t.Fatalf("after AddAction: %+v", info)
	}
	if _, err := r.AddAction("demo", src, "list_deals", InstallOptions{}); err != nil {
		t.Fatal(err)
	}
	if info, _ := r.Info("demo"); info.Version != "1.0.0+local.2" || info.Actions != 3 {
		t.Fatalf("second AddAction: %+v", info)
	}
	// Same seed again: nothing to do.
	if rep, _ := r.SeedWithReport(seedFS("1.0.0", "a")); rep.Changed() {
		t.Errorf("same seed changed things: %+v", rep)
	}
	rep, err := r.SeedWithReport(seedFS("1.0.1", "b"))
	if err != nil || len(rep.Pending) != 1 {
		t.Fatalf("newer seed: %+v %v", rep, err)
	}
	info, _ = r.Info("demo")
	if info.Version != "1.0.0+local.2" || info.PendingUpdate != "1.0.1" {
		t.Fatalf("user change lost or release dropped: %+v", info)
	}
	if err := r.Restore("demo", seedFS("1.0.1", "b")); err != nil {
		t.Fatalf("restore: %v", err)
	}
	info, _ = r.Info("demo")
	if info.Version != "1.0.1" || info.Source != SourceBuiltin || info.Trust != TrustBuiltin || info.Modified || info.PendingUpdate != "" {
		t.Fatalf("after restore: %+v", info)
	}
	// And the lineage continues: an unmodified built-in updates normally.
	if rep, _ := r.SeedWithReport(seedFS("1.0.2", "c")); len(rep.Updated) != 1 {
		t.Errorf("update after restore: %+v", rep)
	}
}

// M1: overlay items never shadow a changed package entry.
func TestOverlayDoesNotShadowNewVersions(t *testing.T) {
	r := newReg(t)
	if _, err := r.Install(acmeDir(t), InstallOptions{}); err != nil {
		t.Fatal(err)
	}
	sel := func(key string) *action.SelectorEntry {
		p, err := r.Get("acme-crm")
		if err != nil {
			t.Fatal(err)
		}
		e, _ := p.Context().Selector(key)
		return e
	}
	healed := action.SelectorEntry{Candidates: []action.SelectorCandidate{{CSS: "tr.deal"}}}
	if err := r.WriteOverlaySelector("acme-crm", "deal.row", healed); err != nil {
		t.Fatal(err)
	}
	aria := action.SelectorCandidate{Aria: &action.AriaSelector{Role: "textbox", Name: "Email"}}
	if err := r.PromoteOverlayCandidate("acme-crm", "contact.email", aria); err != nil {
		t.Fatal(err)
	}
	if e := sel("deal.row"); e.Candidates[0].CSS != "tr.deal" {
		t.Fatalf("overlay entry not applied: %+v", e)
	}
	if e := sel("contact.email"); e.Candidates[0].Aria == nil {
		t.Fatalf("promotion not applied: %+v", e)
	}
	g := generation(t, r)
	if err := r.PromoteOverlayCandidate("acme-crm", "contact.email", aria); err != nil {
		t.Fatal(err)
	}
	if generation(t, r) != g {
		t.Error("repeated promotion wrote")
	}

	// 1.3.0 keeps contact.email's candidates but changes deal.row.
	files := acmeFiles()
	files["automation.json"] = strings.Replace(files["automation.json"], `"1.2.0"`, `"1.3.0"`, 1)
	files["selectors.json"] = strings.Replace(files["selectors.json"], `//tr[1]`, `//tbody/tr[1]`, 1)
	if _, err := r.Install(writeTree(t, t.TempDir(), files), InstallOptions{}); err != nil {
		t.Fatal(err)
	}
	if e := sel("deal.row"); e.Candidates[0].XPath != "//tbody/tr[1]" {
		t.Errorf("stale overlay shadows the new package entry: %+v", e)
	}
	if e := sel("contact.email"); e.Candidates[0].Aria == nil {
		t.Errorf("promotion of a still-present candidate lost: %+v", e)
	}
	if _, stale := r.loadOverlay("acme-crm")["deal.row"]; stale {
		t.Error("stale overlay entry not pruned")
	}

	// 1.4.0 drops the aria candidate: the promotion lapses and is pruned.
	files["automation.json"] = strings.Replace(files["automation.json"], `"1.3.0"`, `"1.4.0"`, 1)
	files["selectors.json"] = strings.Replace(files["selectors.json"], `, {"aria": {"role": "textbox", "name": "Email"}}`, ``, 1)
	if _, err := r.Install(writeTree(t, t.TempDir(), files), InstallOptions{}); err != nil {
		t.Fatal(err)
	}
	if e := sel("contact.email"); len(e.Candidates) != 1 || e.Candidates[0].CSS == "" {
		t.Errorf("lapsed promotion applied: %+v", e)
	}
	if _, err := os.Stat(r.overlayPath("acme-crm")); !os.IsNotExist(err) {
		t.Error("empty overlay not removed")
	}
}

func TestLegacyOverlayFileReadAsPromotion(t *testing.T) {
	r := newReg(t)
	r.Install(acmeDir(t), InstallOptions{})
	os.MkdirAll(filepath.Dir(r.overlayPath("acme-crm")), 0o755)
	os.WriteFile(r.overlayPath("acme-crm"), []byte(`{"contact.email":{"candidates":[{"aria":{"role":"textbox","name":"Email"}},{"css":"input[name=email]"}]}}`), 0o644)
	p, _ := r.Get("acme-crm")
	if e, _ := p.Context().Selector("contact.email"); e.Candidates[0].Aria == nil {
		t.Errorf("legacy overlay not honoured: %+v", e)
	}
}

// M4: colliding content used by other actions is an error; the action's
// own items may be replaced; concurrent merges keep both.
func TestAddActionConflicts(t *testing.T) {
	r := newReg(t)
	if _, err := r.Install(acmeDir(t), InstallOptions{Source: SourceLocal}); err != nil {
		t.Fatal(err)
	}
	// A source whose "dismiss" fragment differs, pulled in by a new action.
	files := acmeFiles()
	files["fragments/dismiss.json"] = `{"name":"dismiss","steps":[{"id":"y","type":"click","configKey":"banner.close"},{"id":"z","type":"click","configKey":"banner.close"}]}`
	files["actions/other.json"] = `{"actionType":"other","sideEffects":"read","steps":[{"id":"f","type":"call_fragment","fragment":"dismiss"}]}`
	files["automation.json"] = strings.Replace(files["automation.json"], `"actions": ["list_deals", "create_contact"]`, `"actions": ["list_deals", "create_contact", "other"]`, 1)
	src, err := OpenDir(writeTree(t, t.TempDir(), files))
	if err != nil {
		t.Fatal(err)
	}
	_, err = r.AddAction("acme-crm", src, "other", InstallOptions{})
	if !errors.Is(err, ErrConflict) || !strings.Contains(err.Error(), "fragments/dismiss.json") {
		t.Fatalf("want conflict on the fragment, got %v", err)
	}
	// A changed contact.email selector: merging list_deals (which does not
	// use it) or create_contact (its only user, being replaced) is fine.
	files2 := acmeFiles()
	files2["selectors.json"] = strings.Replace(files2["selectors.json"], `input[name=email]`, `input#email`, 1)
	src2, _ := OpenDir(writeTree(t, t.TempDir(), files2))
	if _, err := r.AddAction("acme-crm", src2, "list_deals", InstallOptions{}); err != nil {
		t.Errorf("list_deals does not use contact.email, merge should pass: %v", err)
	}
	if _, err := r.AddAction("acme-crm", src2, "create_contact", InstallOptions{}); err != nil {
		t.Errorf("replacing create_contact with its own new selector should pass: %v", err)
	}
	p, _ := r.Get("acme-crm")
	if e, _ := p.Context().Selector("contact.email"); e.Candidates[0].CSS != "input#email" {
		t.Errorf("own selector not replaced: %+v", e)
	}

	// Concurrent merges into one package both land.
	r2 := newReg(t)
	r2.Install(acmeDir(t), InstallOptions{Source: SourceLocal})
	var wg sync.WaitGroup
	for _, name := range []string{"a1", "a2", "a3"} {
		f := acmeFiles()
		f["actions/"+name+".json"] = `{"actionType":"` + name + `","sideEffects":"read","steps":[{"id":"n","type":"navigate","url":"https://app.acme.com/` + name + `"}]}`
		f["automation.json"] = strings.Replace(f["automation.json"], `"create_contact"]`, `"create_contact", "`+name+`"]`, 1)
		s, err := OpenDir(writeTree(t, t.TempDir(), f))
		if err != nil {
			t.Fatal(err)
		}
		wg.Add(1)
		go func(name string, s *Package) {
			defer wg.Done()
			other, _ := Open(r2.Home())
			if _, err := other.AddAction("acme-crm", s, name, InstallOptions{}); err != nil {
				t.Error(err)
			}
		}(name, s)
	}
	wg.Wait()
	p, _ = r2.Get("acme-crm")
	if p.Manifest.Version != "1.2.3" || len(p.Manifest.Actions) != 5 {
		t.Errorf("concurrent merges lost work: %s %v", p.Manifest.Version, p.Manifest.Actions)
	}
}

// M8: the legacy directory is rescanned on every Seed.
func TestLegacyRescan(t *testing.T) {
	r := newReg(t)
	legacy := filepath.Join(r.Home(), "actions", "acme")
	writeTree(t, legacy, map[string]string{"one.json": `{"actionType":"one","steps":[]}`})
	rep, _ := r.SeedWithReport(seedFS("1.0.0", "a"))
	if strings.Join(rep.LegacyWrapped, ",") != "local-acme" {
		t.Fatalf("first wrap: %+v", rep)
	}
	if rep, _ := r.SeedWithReport(seedFS("1.0.0", "a")); rep.Changed() {
		t.Errorf("unchanged legacy dir refolded: %+v", rep)
	}
	writeTree(t, legacy, map[string]string{"two.json": `{"actionType":"two","steps":[]}`})
	rep, _ = r.SeedWithReport(seedFS("1.0.0", "a"))
	info, _ := r.Info("local-acme")
	if len(rep.LegacyWrapped) != 1 || info.Actions != 2 || info.Version != "1.0.1" {
		t.Fatalf("new file not folded: %+v %+v", rep, info)
	}
	writeTree(t, legacy, map[string]string{"one.json": `{"actionType":"one","steps":[{"id":"x","type":"log"}]}`})
	r.Seed(seedFS("1.0.0", "a"))
	p, _ := r.Get("local-acme")
	if b, _ := p.ActionJSON("one"); !strings.Contains(string(b), `"log"`) || p.Manifest.Version != "1.0.2" {
		t.Errorf("changed file not folded: %s %s", b, p.Manifest.Version)
	}
}

// L8: rollback restores the rolled-back-to version's source and trust.
func TestRollbackRestoresSourceAndTrust(t *testing.T) {
	r := newReg(t)
	if _, err := r.Install(acmeDir(t), InstallOptions{Source: SourceLocal}); err != nil {
		t.Fatal(err)
	}
	files := acmeFiles()
	files["automation.json"] = strings.Replace(files["automation.json"], `"1.2.0"`, `"1.3.0"`, 1)
	if _, err := r.Install(writeTree(t, t.TempDir(), files), InstallOptions{ReplaceBuiltin: true}); err != nil {
		t.Fatal(err)
	}
	if info, _ := r.Info("acme-crm"); info.Trust != TrustImported {
		t.Fatalf("replaced: %+v", info)
	}
	r.Rollback("acme-crm")
	if info, _ := r.Info("acme-crm"); info.Version != "1.2.0" || info.Source != SourceLocal || info.Trust != TrustLocal {
		t.Fatalf("rollback: %+v", info)
	}
	r.Rollback("acme-crm")
	if info, _ := r.Info("acme-crm"); info.Version != "1.3.0" || info.Source != SourceImported || info.Trust != TrustImported {
		t.Fatalf("roll forward: %+v", info)
	}
}

func TestGenerationTracksChanges(t *testing.T) {
	r := newReg(t)
	r.Seed(seedFS("1.0.0", "a"))
	g1 := generation(t, r)
	r.Seed(seedFS("1.0.0", "a"))
	if generation(t, r) != g1 {
		t.Error("no-op seed changed the generation")
	}
	r.SetEnabled("demo", false)
	g2 := generation(t, r)
	if g2 == g1 {
		t.Error("SetEnabled did not change the generation")
	}
	r.WriteOverlaySelector("demo", "k", action.SelectorEntry{Candidates: []action.SelectorCandidate{{CSS: "x"}}})
	if generation(t, r) == g2 {
		t.Error("overlay write did not change the generation")
	}
}

// N2: metadata-only differences are not conflicts.
func TestAddActionIgnoresMetadataOnlyDifferences(t *testing.T) {
	r := newReg(t)
	base := acmeFiles()
	base["selectors.json"] = strings.Replace(base["selectors.json"],
		`"contact.email": {"candidates": [{"css": "input[name=email]"}, {"aria": {"role": "textbox", "name": "Email"}}]}`,
		`"contact.email": {"candidates": [{"css": "input[name=email]"}, {"aria": {"role": "textbox", "name": "Email"}}], "verifiedAt": "2026-09-01T10:00:00Z"}`, 1)
	if _, err := r.Install(writeTree(t, t.TempDir(), base), InstallOptions{Source: SourceLocal}); err != nil {
		t.Fatal(err)
	}
	// The source re-verified contact.email later, and its dismiss fragment
	// is the same JSON with different key order and whitespace.
	src := acmeFiles()
	src["selectors.json"] = strings.Replace(src["selectors.json"],
		`"contact.email": {"candidates": [{"css": "input[name=email]"}, {"aria": {"role": "textbox", "name": "Email"}}]}`,
		`"contact.email": {"candidates": [{"css": "input[name=email]"}, {"aria": {"role": "textbox", "name": "Email"}}], "verifiedAt": "2026-09-20T10:00:00Z"}`, 1)
	src["fragments/dismiss.json"] = "{\n  \"steps\": [ {\"type\":\"click\", \"id\":\"x\", \"configKey\":\"banner.close\"} ],\n  \"name\": \"dismiss\"\n}\n"
	src["actions/other.json"] = `{"actionType":"other","sideEffects":"write","steps":[
  {"id":"f","type":"call_fragment","fragment":"dismiss"},
  {"id":"e","type":"type","configKey":"contact.email","value":"x"},
  {"id":"s","type":"click","configKey":"contact.save","sideEffect":true}]}`
	src["automation.json"] = strings.Replace(src["automation.json"], `"actions": ["list_deals", "create_contact"]`, `"actions": ["list_deals", "create_contact", "other"]`, 1)
	sp, err := OpenDir(writeTree(t, t.TempDir(), src))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.AddAction("acme-crm", sp, "other", InstallOptions{}); err != nil {
		t.Fatalf("metadata-only differences reported as conflicts: %v", err)
	}
	p, _ := r.Get("acme-crm")
	if e, _ := p.Context().Selector("contact.email"); e.VerifiedAt != "2026-09-20T10:00:00Z" {
		t.Errorf("newer verifiedAt not kept: %q", e.VerifiedAt)
	}
	if b, _ := fs.ReadFile(p.FS, "fragments/dismiss.json"); string(b) != base["fragments/dismiss.json"] {
		t.Errorf("equal fragment was rewritten: %s", b)
	}

	// A real difference in a used selector is still a conflict.
	src["selectors.json"] = strings.Replace(src["selectors.json"], `input[name=email]`, `input#mail`, 1)
	sp, _ = OpenDir(writeTree(t, t.TempDir(), src))
	if _, err := r.AddAction("acme-crm", sp, "other", InstallOptions{}); !errors.Is(err, ErrConflict) {
		t.Errorf("real selector change not a conflict: %v", err)
	}
}
