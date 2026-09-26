package automation

import (
	"errors"
	"strings"
	"testing"

	"github.com/monoes/mono-agent/internal/action"
)

// V2: replacing the user's own package needs confirmation, and a
// same-version replacement keeps the old copy for rollback.
func TestReplaceOwnPackageKeepsRollbackCopy(t *testing.T) {
	r := newReg(t)
	if _, err := r.Install(acmeDir(t), InstallOptions{Trust: TrustLocal}); err != nil {
		t.Fatal(err)
	}

	// Identical content: a no-op, no confirmation, no write.
	g := generation(t, r)
	res, err := r.Install(acmeDir(t), InstallOptions{Trust: TrustLocal})
	if err != nil || !res.Installed || !strings.Contains(strings.Join(res.Warnings, "\n"), "no changes") {
		t.Fatalf("identical reinstall: %v %+v", err, res)
	}
	if generation(t, r) != g {
		t.Error("identical reinstall wrote")
	}

	// Same version, different content.
	files := acmeFiles()
	files["scripts/parse.js"] = "return 'v2';\n"
	dir := writeTree(t, t.TempDir(), files)
	res, err = r.Install(dir, InstallOptions{Trust: TrustLocal, DryRun: true})
	if err != nil || !strings.Contains(strings.Join(res.Warnings, "\n"), "REPLACES your own package") {
		t.Fatalf("dry run: %v %v", err, res.Warnings)
	}
	if !res.Review.ReplaceRequired {
		t.Error("ReplaceRequired not set")
	}
	if _, err := r.Install(dir, InstallOptions{Trust: TrustLocal}); !errors.Is(err, ErrReplaces) || !errors.Is(err, ErrReplacesBuiltin) {
		t.Fatalf("unconfirmed replace: %v", err)
	}
	if _, err := r.Install(dir, InstallOptions{Trust: TrustLocal, Replace: true}); err != nil {
		t.Fatal(err)
	}
	info, _ := r.Info("acme-crm")
	if info.Version != "1.2.0" || info.PreviousVersion != "1.2.0+replaced.1" {
		t.Fatalf("after replace: %+v", info)
	}
	script := func() string {
		p, err := r.Get("acme-crm")
		if err != nil {
			t.Fatal(err)
		}
		s, _ := p.Script("parse.js")
		return s
	}
	if script() != "return 'v2';\n" {
		t.Error("new content not installed")
	}
	if err := r.Rollback("acme-crm"); err != nil {
		t.Fatalf("rollback: %v", err)
	}
	if info, _ := r.Info("acme-crm"); info.Version != "1.2.0+replaced.1" || info.Trust != TrustLocal || strings.Contains(script(), "v2") {
		t.Fatalf("rollback did not restore the old copy: %+v", info)
	}
	if err := r.Rollback("acme-crm"); err != nil || script() != "return 'v2';\n" {
		t.Fatalf("roll forward: %v", err)
	}

	// The old name ReplaceBuiltin still confirms a same-version replace.
	files["scripts/parse.js"] = "return 'v3';\n"
	dir = writeTree(t, t.TempDir(), files)
	if _, err := r.Install(dir, InstallOptions{Trust: TrustLocal, ReplaceBuiltin: true}); err != nil {
		t.Errorf("ReplaceBuiltin alias: %v", err)
	}

	// Imported over imported stays an ordinary update.
	imp := newReg(t)
	imp.Install(acmeDir(t), InstallOptions{})
	if _, err := imp.Install(dir, InstallOptions{}); err != nil {
		t.Errorf("imported update: %v", err)
	}
}

// (b) Merging imported content into a recorded package lowers its trust:
// that needs confirmation and the review says so.
func TestMergeIntoRecordedNeedsConfirmation(t *testing.T) {
	r := newReg(t)
	src := mustOpenDir(t, acmeDir(t))
	if _, err := r.AddAction("acme-crm", src, "create_contact", InstallOptions{Trust: TrustRecorded}); err != nil {
		t.Fatal(err)
	}
	imp, err := OpenFile(packDir(t, acmeDir(t)))
	if err != nil {
		t.Fatal(err)
	}
	res, err := r.AddAction("acme-crm", imp, "list_deals", InstallOptions{DryRun: true})
	if err != nil {
		t.Fatalf("dry run: %v", err)
	}
	if tc := res.Review.TrustChange; tc == nil || tc.From != TrustRecorded || tc.To != TrustImported {
		t.Errorf("TrustChange: %+v", res.Review.TrustChange)
	}
	if w := strings.Join(res.Warnings, "\n"); !strings.Contains(w, "trust drops from recorded to imported") || !strings.Contains(w, "requires confirmation") {
		t.Errorf("warnings: %s", w)
	}
	if _, err := r.AddAction("acme-crm", imp, "list_deals", InstallOptions{}); !errors.Is(err, ErrReplaces) {
		t.Fatalf("unconfirmed trust drop: %v", err)
	}
	if info, _ := r.Info("acme-crm"); info.Trust != TrustRecorded {
		t.Fatalf("trust changed without confirmation: %s", info.Trust)
	}
	if _, err := r.AddAction("acme-crm", imp, "list_deals", InstallOptions{Replace: true}); err != nil {
		t.Fatal(err)
	}
	if info, _ := r.Info("acme-crm"); info.Trust != TrustImported {
		t.Errorf("after confirmed merge: %s", info.Trust)
	}

	// Recorded into local: allowed, but the review reports the drop.
	loc := newReg(t)
	loc.Install(acmeDir(t), InstallOptions{Trust: TrustLocal})
	f := acmeFiles()
	f["actions/extra.json"] = `{"actionType":"extra","sideEffects":"read","steps":[{"id":"n","type":"navigate","url":"https://app.acme.com/x"}]}`
	f["automation.json"] = strings.Replace(f["automation.json"], `"create_contact"]`, `"create_contact", "extra"]`, 1)
	res, err = loc.AddAction("acme-crm", mustOpenDir(t, writeTree(t, t.TempDir(), f)), "extra", InstallOptions{Trust: TrustRecorded})
	if err != nil {
		t.Fatal(err)
	}
	if tc := res.Review.TrustChange; tc == nil || tc.From != TrustLocal || tc.To != TrustRecorded {
		t.Errorf("local→recorded TrustChange: %+v", res.Review.TrustChange)
	}
}

// (a) Visibility kinds appear in plain language, once per kind.
func TestReviewVisibilityCapabilities(t *testing.T) {
	files := acmeFiles()
	files["actions/create_contact.json"] = strings.Replace(files["actions/create_contact.json"], `"sideEffects":"write",`,
		`"sideEffects":"write","visibility":["profile_view_visible_to_owner","search_may_be_saved"],`, 1)
	files["actions/list_deals.json"] = strings.Replace(files["actions/list_deals.json"], `"sideEffects":"read",`,
		`"sideEffects":"read","visibility":["profile_view_visible_to_owner"],`, 1)
	p := mustOpenDir(t, writeTree(t, t.TempDir(), files))
	rv := buildReview(p, nil)
	caps := strings.Join(rv.Capabilities, "\n")
	if n := strings.Count(caps, "visits profiles"); n != 1 {
		t.Errorf("profile visits listed %d times:\n%s", n, caps)
	}
	if !strings.Contains(caps, "visits profiles — the profile's owner can see your visit (create_contact, list_deals)") {
		t.Errorf("profile line:\n%s", caps)
	}
	if !strings.Contains(caps, "runs searches — the site may keep them in your account's search history (create_contact)") {
		t.Errorf("search line:\n%s", caps)
	}
	if got := rv.Visibility["profile_view_visible_to_owner"]; len(got) != 2 {
		t.Errorf("Visibility map: %v", rv.Visibility)
	}
	for _, k := range action.VisibilityKinds {
		if visibilityPhrases[k] == "" {
			t.Errorf("visibility kind %q has no plain-language phrase", k)
		}
	}
}
