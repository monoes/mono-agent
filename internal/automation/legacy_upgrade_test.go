package automation

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const longLegacyName = "a_really_long_legacy_platform_name_for_upgrade_tests"

func writeLegacyDirs(t *testing.T, home string) {
	t.Helper()
	root := filepath.Join(home, "actions")
	writeTree(t, filepath.Join(root, "google_maps"), map[string]string{
		"get_place.json": `{"actionType":"get_place","platform":"google_maps","steps":[
  {"id":"open","type":"navigate","url":"https://maps.google.com/?q={{query}}"},
  {"id":"go","type":"navigate","url":"https://maps.google.com/"},
  {"id":"name","type":"extract_text","selector":"h1","variable":"name"}]}`,
	})
	writeTree(t, filepath.Join(root, "google-maps"), map[string]string{
		"other.json": `{"actionType":"other","steps":[{"id":"n","type":"navigate","url":"https://www.google.com/maps"}]}`,
	})
	writeTree(t, filepath.Join(root, longLegacyName), map[string]string{
		"run.json": `{"actionType":"run","steps":[{"id":"n","type":"navigate","url":"https://tool.example.org/run"}]}`,
	})
	writeTree(t, filepath.Join(root, "devtool"), map[string]string{
		"poke.json": `{"actionType":"poke","steps":[{"id":"n","type":"navigate","url":"http://localhost:3000/"},{"id":"w","type":"wait","duration":1}]}`,
	})
}

func legacyByAlias(t *testing.T, r *Registry) map[string]InstalledInfo {
	t.Helper()
	infos, err := r.List(false)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]InstalledInfo{}
	for _, i := range infos {
		if i.LegacyPlatform != "" {
			out[i.LegacyPlatform] = i
		}
	}
	return out
}

func TestLegacyUpgradeAliasesAndValidity(t *testing.T) {
	r := newReg(t)
	writeLegacyDirs(t, r.Home())
	if err := r.Seed(seedFS("1.0.0", "a")); err != nil {
		t.Fatal(err)
	}
	by := legacyByAlias(t, r)
	if len(by) != 4 {
		t.Fatalf("legacy packages by alias: %v", by)
	}
	// google_maps and google-maps slug to the same id: one gets it, the
	// other a hashed id, and both keep their own alias.
	gm := by["google_maps"]
	// Legacy packages run unrestricted: no domains, only a suggestion.
	if !strings.HasPrefix(gm.ID, "local-google-maps") || len(gm.Domains) != 0 || gm.StartURL != "https://maps.google.com/" {
		t.Errorf("google_maps: %+v", gm)
	}
	if other := by["google-maps"]; other.ID == gm.ID || !ValidID(other.ID) {
		t.Errorf("google-maps collided with google_maps: %q", other.ID)
	}
	long := by[longLegacyName]
	if !ValidID(long.ID) || len(long.ID) > 41 {
		t.Errorf("long name id %q", long.ID)
	}

	for alias, info := range by {
		p, err := r.Get(info.ID)
		if err != nil {
			t.Fatal(err)
		}
		if p.LegacyAlias() != alias {
			t.Errorf("%s: LegacyAlias %q", info.ID, p.LegacyAlias())
		}
		if errs := errorsOnly(Validate(p)); len(errs) != 0 {
			t.Errorf("%s (%s) fails its own validation: %+v", info.ID, alias, errs)
		}
	}
	p, _ := r.Get(gm.ID)
	if strings.Join(p.Manifest.Permissions.Steps, ",") != "extract_text,navigate" {
		t.Errorf("derived steps: %v", p.Manifest.Permissions.Steps)
	}
	// Underivable domains (localhost): the documented exception, a warning.
	dev, _ := r.Get(by["devtool"].ID)
	if len(dev.Manifest.Site.Domains) != 0 {
		t.Errorf("devtool domains: %v", dev.Manifest.Site.Domains)
	}
	var warned bool
	for _, is := range Validate(dev) {
		if is.Code == "missing_domains" && is.Severity == "warning" && strings.Contains(is.Message, "legacy") {
			warned = true
		}
	}
	if !warned {
		t.Error("legacy exception not reported as a warning")
	}

	// DefSource resolves the original platform names.
	ds := r.DefSource()
	if c := ds.Package("google_maps"); c == nil || c.ID() != gm.ID {
		t.Errorf("DefSource.Package(google_maps) = %v", c)
	}
	if b, err := ds.Load("google_maps", "get_place"); err != nil || !bytes.Contains(b, []byte("get_place")) {
		t.Errorf("DefSource.Load(google_maps): %v", err)
	}
	if c := ds.Package(longLegacyName); c == nil || c.ID() != long.ID {
		t.Errorf("DefSource.Package(long) = %v", c)
	}

	// Export → install elsewhere works; the imported copy claims no alias.
	var buf bytes.Buffer
	if err := r.Export(gm.ID, &buf, ExportOptions{Domains: []string{"*.google.com"}}); err != nil {
		t.Fatal(err)
	}
	f := filepath.Join(t.TempDir(), "gm.mpkg")
	os.WriteFile(f, buf.Bytes(), 0o644)
	other := newReg(t)
	res, err := other.Install(f, InstallOptions{})
	if err != nil {
		t.Fatalf("install exported legacy package: %v %+v", err, errorsOnly(res.Issues))
	}
	imp, _ := other.Get(gm.ID)
	if imp.LegacyAlias() != "" {
		t.Error("an imported package claimed a legacy alias")
	}
	if other.DefSource().Package("google_maps") != nil {
		t.Error("imported package resolved as a legacy alias")
	}
}

func TestLegacyUnderscoreNameGetsPlainID(t *testing.T) {
	r := newReg(t)
	writeTree(t, filepath.Join(r.Home(), "actions", "google_maps"), map[string]string{
		"get_place.json": `{"actionType":"get_place","steps":[{"id":"n","type":"navigate","url":"https://maps.google.com/"}]}`,
	})
	r.Seed(seedFS("1.0.0", "a"))
	info, err := r.Info("local-google-maps")
	if err != nil || info.LegacyPlatform != "google_maps" {
		t.Fatalf("%+v %v", info, err)
	}
	if c := r.DefSource().Package("google_maps"); c == nil || c.ID() != "local-google-maps" {
		t.Errorf("alias not resolved: %v", c)
	}
}

// A registry folded by an older build (no legacy field, empty domains and
// steps) is refolded once; an uninstalled one stays gone.
func TestLegacyRefoldAfterUpgrade(t *testing.T) {
	r := newReg(t)
	writeLegacyDirs(t, r.Home())
	legacy := scanLegacy(filepath.Join(r.Home(), "actions"))
	err := r.update(func(idx *indexFile) (bool, error) {
		idx.LegacyHashes = map[string]string{}
		for platform, ld := range legacy {
			idx.LegacyHashes[platform] = ld.hash
			if platform == "devtool" {
				continue // pretend the user uninstalled it
			}
			m := Manifest{Schema: SchemaV1, ID: legacyID(ld.name), Name: "Local " + ld.name, Version: "1.0.0",
				Site: Site{Domains: []string{}}, Permissions: Permissions{Steps: []string{}, Scripts: []string{}},
				Policy: Policy{Tier: "standard"}}
			files := map[string][]byte{}
			for n, b := range ld.actions {
				files["actions/"+n+".json"] = b
				m.Actions = append(m.Actions, n)
			}
			b, _ := json.MarshalIndent(m, "", "  ")
			files[ManifestFile] = b
			if platform == "google-maps" {
				continue // collided with google_maps under the old ids
			}
			p, _ := OpenFS(mapFS(files), SourceLocal)
			if _, err := r.commitLocked(idx, p, files, false); err != nil {
				return false, err
			}
		}
		return true, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	rep, err := r.SeedWithReport(seedFS("1.0.0", "a"))
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.LegacyWrapped) == 0 {
		t.Fatalf("no refold: %+v", rep)
	}
	info, err := r.Info("local-google-maps")
	if err != nil || info.LegacyPlatform != "google_maps" || info.Version != "1.0.1" || len(info.Domains) != 0 {
		t.Fatalf("refolded google_maps: %+v %v", info, err)
	}
	p, _ := r.Get("local-google-maps")
	if errs := errorsOnly(Validate(p)); len(errs) != 0 {
		t.Errorf("refolded package invalid: %+v", errs)
	}
	if _, err := r.Info(legacyID("devtool")); err == nil {
		t.Error("uninstalled legacy package came back")
	}
	// Once only.
	if rep, _ := r.SeedWithReport(seedFS("1.0.0", "a")); rep.Changed() {
		t.Errorf("refolded twice: %+v", rep)
	}
}

func TestLegacyAPIForRuntimeAndBundles(t *testing.T) {
	if LegacyPackageID("google_maps") != "local-google-maps" || len(LegacyPackageID(longLegacyName)) > 41 || !ValidID(LegacyPackageID(longLegacyName)) {
		t.Errorf("LegacyPackageID: %q %q", LegacyPackageID("google_maps"), LegacyPackageID(longLegacyName))
	}

	// Only google_maps installed: both spellings find it; the context
	// reports the original name.
	r := newReg(t)
	writeTree(t, filepath.Join(r.Home(), "actions", "google_maps"), map[string]string{
		"get_place.json": `{"actionType":"get_place","steps":[{"id":"n","type":"navigate","url":"https://maps.google.com/"}]}`,
	})
	r.Seed(seedFS("1.0.0", "a"))
	ds := r.DefSource()
	for _, name := range []string{"google_maps", "google-maps", "GOOGLE_MAPS"} {
		c := ds.Package(name)
		if c == nil || c.ID() != "local-google-maps" {
			t.Fatalf("DefSource.Package(%q) = %v", name, c)
		}
		lp, ok := c.(interface{ LegacyPlatform() string })
		if !ok || lp.LegacyPlatform() != "google_maps" {
			t.Errorf("LegacyPlatform() via %q", name)
		}
	}
	if c := ds.Package("gemini"); c != nil {
		t.Errorf("non-installed id resolved: %v", c)
	}

	// Disabled: DefSource hides it, ResolveLegacyPlatform still finds it.
	r.SetEnabled("local-google-maps", false)
	if ds.Package("google_maps") != nil {
		t.Error("disabled package visible through DefSource")
	}
	if id, ok := r.ResolveLegacyPlatform("google_maps"); !ok || id != "local-google-maps" {
		t.Errorf("ResolveLegacyPlatform: %q %v", id, ok)
	}

	// Both spellings installed: the exact original name wins.
	both := newReg(t)
	writeLegacyDirs(t, both.Home())
	both.Seed(seedFS("1.0.0", "a"))
	by := legacyByAlias(t, both)
	for _, name := range []string{"google_maps", "google-maps"} {
		if id, _ := both.ResolveLegacyPlatform(name); id != by[name].ID {
			t.Errorf("ResolveLegacyPlatform(%q) = %q, want %q", name, id, by[name].ID)
		}
	}

	// A package that opens localhost cannot be exported.
	var buf bytes.Buffer
	err := both.Export(by["devtool"].ID, &buf, ExportOptions{})
	if !errors.Is(err, ErrNotExportable) || !strings.Contains(err.Error(), "localhost:3000") || !strings.Contains(err.Error(), "can't be exported") {
		t.Errorf("export of a domainless legacy package: %v", err)
	}
}
