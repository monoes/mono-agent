package automation

import (
	"errors"
	"strings"
	"testing"
)

func TestDomainPatterns(t *testing.T) {
	for _, d := range []string{"app.acme.com", "*.acme.com", "acme.com:8443", "news.ycombinator.com", "127.0.0.1", "*.acme.co.uk"} {
		if err := checkDomainPattern(d); err != nil {
			t.Errorf("%q rejected: %v", d, err)
		}
	}
	for _, d := range []string{"com", "*.com", "co.uk", "*.co.uk", "github.io", "*.github.io", "localhost", "*", "bad domain", "*.*.acme.com"} {
		if err := checkDomainPattern(d); err == nil {
			t.Errorf("%q accepted", d)
		}
	}
}

func TestManifestURLsAndCallActions(t *testing.T) {
	m := Manifest{Schema: SchemaV1, ID: "acme", Name: "A", Version: "1.0.0", Actions: []string{"a"},
		Site:        Site{StartURL: "https://evil.example.org/", Domains: []string{"app.acme.com"}},
		Login:       &Login{URL: "https://login.other.com/"},
		Permissions: Permissions{Steps: []string{"navigate"}, CallActions: []string{"other.run", "bad", "Other.run", "x.a.b"}}}
	codes := map[string]int{}
	for _, is := range validateManifest(m, SourceImported) {
		codes[is.Code]++
	}
	if codes["start_url_off_domain"] != 1 || codes["login_url_off_domain"] != 1 || codes["bad_call_action"] != 3 {
		t.Errorf("issues: %v", codes)
	}
	m.Site.StartURL, m.Login.URL = "https://app.acme.com/home", "javascript:alert(1)"
	codes = map[string]int{}
	for _, is := range validateManifest(m, SourceImported) {
		codes[is.Code]++
	}
	if codes["start_url_off_domain"] != 0 || codes["login_url_off_domain"] != 1 {
		t.Errorf("issues: %v", codes)
	}
}

// demoImported is the acme package renamed to collide with the seeded
// built-in "demo".
func demoImported(t *testing.T) string {
	files := acmeFiles()
	files["automation.json"] = strings.Replace(files["automation.json"], `"id": "acme-crm"`, `"id": "demo"`, 1)
	return writeTree(t, t.TempDir(), files)
}

func TestImportedCannotSilentlyReplaceBuiltin(t *testing.T) {
	r := newReg(t)
	if err := r.Seed(seedFS("1.0.0", "a")); err != nil {
		t.Fatal(err)
	}
	dir := demoImported(t)

	res, err := r.Install(dir, InstallOptions{DryRun: true})
	if err != nil {
		t.Fatalf("dry run: %v", err)
	}
	rp := res.Review.Replaces
	if rp == nil || rp.ID != "demo" || rp.Source != SourceBuiltin || rp.Trust != TrustBuiltin {
		t.Fatalf("Replaces: %+v", rp)
	}
	if !strings.Contains(strings.Join(res.Warnings, "\n"), "REPLACES the installed BUILTIN package") {
		t.Errorf("no loud warning: %v", res.Warnings)
	}

	if _, err := r.Install(dir, InstallOptions{}); !errors.Is(err, ErrReplacesBuiltin) {
		t.Fatalf("want ErrReplacesBuiltin, got %v", err)
	}
	if info, _ := r.Info("demo"); info.Source != SourceBuiltin || info.Version != "1.0.0" {
		t.Fatalf("built-in overwritten: %+v", info)
	}

	res, err = r.Install(dir, InstallOptions{ReplaceBuiltin: true})
	if err != nil || !res.Installed {
		t.Fatalf("with ReplaceBuiltin: %v", err)
	}
	if info, _ := r.Info("demo"); info.Source != SourceImported || info.Trust != TrustImported {
		t.Fatalf("after replace: %+v", info)
	}

	// Same gate for a local package.
	loc := newReg(t)
	if _, err := loc.Install(acmeDir(t), InstallOptions{Source: SourceLocal}); err != nil {
		t.Fatal(err)
	}
	if _, err := loc.Install(packDir(t, acmeDir(t)), InstallOptions{}); !errors.Is(err, ErrReplacesBuiltin) {
		t.Errorf("imported over local: %v", err)
	}
	// Imported over imported is an ordinary update.
	imp := newReg(t)
	imp.Install(acmeDir(t), InstallOptions{})
	if _, err := imp.Install(acmeDir(t), InstallOptions{}); err != nil {
		t.Errorf("imported over imported: %v", err)
	}
}

func TestTrustFlags(t *testing.T) {
	r := newReg(t)
	if _, err := r.Install(acmeDir(t), InstallOptions{}); err != nil {
		t.Fatal(err)
	}
	type trustCtx interface {
		Trust() string
		CallActions() []string
		ScriptsAllowed() bool
		LiveRunConfirmed() bool
	}
	ctx := func() trustCtx {
		p, err := r.Get("acme-crm")
		if err != nil {
			t.Fatal(err)
		}
		return p.Context().(trustCtx)
	}
	if c := ctx(); c.Trust() != TrustImported || c.ScriptsAllowed() || c.LiveRunConfirmed() {
		t.Fatalf("imported defaults: %s %v %v", c.Trust(), c.ScriptsAllowed(), c.LiveRunConfirmed())
	}
	yes := true
	if err := r.SetTrustFlags("acme-crm", &yes, &yes); err != nil {
		t.Fatal(err)
	}
	if c := ctx(); !c.ScriptsAllowed() || !c.LiveRunConfirmed() {
		t.Fatal("opt-ins not applied")
	}
	if info, _ := r.Info("acme-crm"); !info.ScriptsAllowed || !info.LiveRunConfirmed {
		t.Errorf("info flags: %+v", info)
	}
	// Reinstalling the same bytes keeps the opt-ins; new content resets them.
	r.Install(acmeDir(t), InstallOptions{})
	if c := ctx(); !c.ScriptsAllowed() {
		t.Error("same-bytes reinstall reset opt-ins")
	}
	files := acmeFiles()
	files["scripts/parse.js"] = "return 'changed';\n"
	if _, err := r.Install(writeTree(t, t.TempDir(), files), InstallOptions{}); err != nil {
		t.Fatal(err)
	}
	if c := ctx(); c.ScriptsAllowed() || c.LiveRunConfirmed() {
		t.Error("changed content kept the opt-ins")
	}
	if err := r.SetTrustFlags("nope", &yes, nil); !errors.Is(err, ErrNotInstalled) {
		t.Errorf("unknown id: %v", err)
	}

	// Local: scripts and live runs by default; an explicit no wins.
	loc := newReg(t)
	loc.Install(acmeDir(t), InstallOptions{Source: SourceLocal})
	p, _ := loc.Get("acme-crm")
	c := p.Context().(trustCtx)
	if c.Trust() != TrustLocal || !c.ScriptsAllowed() || !c.LiveRunConfirmed() {
		t.Fatalf("local defaults: %s %v %v", c.Trust(), c.ScriptsAllowed(), c.LiveRunConfirmed())
	}
	no := false
	loc.SetTrustFlags("acme-crm", &no, nil)
	p, _ = loc.Get("acme-crm")
	if p.Context().(trustCtx).ScriptsAllowed() {
		t.Error("--no-scripts ignored for local")
	}

	// Outside a registry: tier from Source / Package.Trust, fail closed.
	p, _ = OpenDir(acmeDir(t))
	p.Trust = "bogus"
	if c := p.Context().(trustCtx); c.Trust() != TrustImported || c.ScriptsAllowed() {
		t.Error("unknown trust not treated as imported")
	}
}

func TestAddActionTrust(t *testing.T) {
	src, _ := OpenDir(acmeDir(t))

	// A recording saved as a new package is recorded: no scripts by default.
	r := newReg(t)
	if _, err := r.AddAction("acme-crm", src, "list_deals", InstallOptions{Trust: TrustRecorded}); err != nil {
		t.Fatal(err)
	}
	info, _ := r.Info("acme-crm")
	if info.Trust != TrustRecorded || info.ScriptsAllowed || info.Source != SourceLocal {
		t.Fatalf("recorded package: %+v", info)
	}
	if _, err := r.AddAction("acme-crm", src, "list_deals", InstallOptions{Trust: "root"}); err == nil {
		t.Error("invalid trust accepted")
	}

	// Recorded merged into a built-in: allowed, trust drops to recorded.
	r = newReg(t)
	r.Seed(seedFS("1.0.0", "a"))
	if _, err := r.AddAction("demo", src, "create_contact", InstallOptions{Trust: TrustRecorded}); err != nil {
		t.Fatal(err)
	}
	if info, _ := r.Info("demo"); info.Trust != TrustRecorded {
		t.Errorf("merged trust %s", info.Trust)
	}

	// Imported content merged into a built-in needs ReplaceBuiltin.
	r = newReg(t)
	r.Seed(seedFS("1.0.0", "a"))
	imp, err := OpenFile(packDir(t, acmeDir(t)))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.AddAction("demo", imp, "create_contact", InstallOptions{}); !errors.Is(err, ErrReplacesBuiltin) {
		t.Errorf("imported into built-in: %v", err)
	}
	// An imported action into a new id stays imported.
	if _, err := r.AddAction("fresh", imp, "create_contact", InstallOptions{}); err != nil {
		t.Fatal(err)
	}
	if info, _ := r.Info("fresh"); info.Trust != TrustImported || info.Source != SourceImported {
		t.Errorf("imported action import: %+v", info)
	}
}

func TestReviewSecurityFields(t *testing.T) {
	files := acmeFiles()
	files["automation.json"] = strings.NewReplacer(
		`"extract_*"]`, `"extract_*", "upload", "download", "http_fetch_in_page"]`,
		`"downloads": false`, `"downloads": true, "callActions": ["other.run"]`,
		`"actions":`, `"login": {"url": "https://app.acme.com/login"}, "requires": {"native": "acme"}, "actions":`,
	).Replace(files["automation.json"])
	p, err := OpenDir(writeTree(t, t.TempDir(), files))
	if err != nil {
		t.Fatal(err)
	}
	p.Source = SourceImported
	rv := buildReview(p, map[string][]byte{})
	if rv.Source != SourceImported || rv.Trust != TrustImported || rv.Native != "acme" ||
		rv.LoginURL != "https://app.acme.com/login" || strings.Join(rv.CallActions, ",") != "other.run" ||
		rv.ComputedTier != "standard" {
		t.Errorf("review: %+v", rv)
	}
	if !strings.Contains(rv.ScriptSources["parse.js"], "querySelectorAll") {
		t.Errorf("script source missing: %v", rv.ScriptSources)
	}
	caps := strings.Join(rv.Capabilities, "\n")
	for _, want := range []string{"can run scripts in the page that can read site data and send it anywhere",
		"can upload local files", "can download files", "logged-in session", "can create or change content as you",
		"other automations: other.run", "app.acme.com"} {
		if !strings.Contains(caps, want) {
			t.Errorf("capability %q missing:\n%s", want, caps)
		}
	}

	// A "standard" manifest on a social domain is computed as social.
	p.Manifest.Site.Domains = []string{"www.instagram.com"}
	p.Manifest.Policy.Tier = "standard"
	if rv := buildReview(p, nil); rv.ComputedTier != "social" || rv.SocialPlatform != "instagram" {
		t.Errorf("computed tier: %s %s", rv.ComputedTier, rv.SocialPlatform)
	}
}
