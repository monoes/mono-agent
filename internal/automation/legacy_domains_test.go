package automation

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/monoes/mono-agent/internal/action"
)

// D8: legacy actions behave exactly as before the upgrade — templated
// navigates and redirects to other hosts are not off-domain.
func TestLegacyActionsRunUnrestricted(t *testing.T) {
	r := newReg(t)
	writeTree(t, filepath.Join(r.Home(), "actions", "search_tool"), map[string]string{
		"open.json": `{"actionType":"open","sideEffects":"read","steps":[
  {"id":"a","type":"navigate","url":"https://google.com/"},
  {"id":"b","type":"navigate","url":"{{url}}"}]}`,
	})
	if err := r.Seed(seedFS("1.0.0", "a")); err != nil {
		t.Fatal(err)
	}
	ctx := r.DefSource().Package("search_tool")
	if ctx == nil {
		t.Fatal("legacy package not resolvable")
	}
	if len(ctx.Domains()) != 0 {
		t.Fatalf("legacy package restricted to %v", ctx.Domains())
	}
	// google.com redirects to www.google.com; {{url}} can be anything.
	for _, u := range []string{"https://www.google.com/", "https://example.org/page", "https://google.com/"} {
		if err := action.URLAllowed(u, ctx.Domains()); err != nil {
			t.Errorf("%s: %v", u, err)
		}
	}
	p, _ := r.Get(ctx.ID())
	def, _ := p.Action("open")
	for _, is := range action.Validate(def, ctx) {
		if is.Code == "off_domain" {
			t.Errorf("off_domain issue: %+v", is)
		}
	}
	if s := p.Manifest.Legacy.SuggestedDomains; strings.Join(s, ",") != "*.google.com,google.com" {
		t.Errorf("suggested domains: %v", s)
	}
	var info bool
	for _, is := range Validate(p) {
		if is.Code == "legacy_suggested_domains" && is.Severity == "info" {
			info = true
		}
	}
	if !info {
		t.Error("suggestion not shown as info")
	}
}

// Users who got v0.74's derived domains are refolded to unrestricted;
// domains they added by hand stay.
func TestLegacyRefoldClearsDerivedDomains(t *testing.T) {
	for _, tc := range []struct {
		name    string
		domains []string
		want    []string
	}{
		{"derived", []string{"google.com"}, []string{}},
		{"hand-added kept", []string{"google.com", "*.corp.example"}, []string{"*.corp.example"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := newReg(t)
			ld := map[string]string{"open.json": `{"actionType":"open","steps":[{"id":"a","type":"navigate","url":"https://google.com/"}]}`}
			writeTree(t, filepath.Join(r.Home(), "actions", "search_tool"), ld)
			legacy := scanLegacy(filepath.Join(r.Home(), "actions"))
			err := r.update(func(idx *indexFile) (bool, error) {
				m := Manifest{Schema: SchemaV1, ID: "local-search-tool", Name: "Local search_tool", Version: "1.0.0",
					Site: Site{StartURL: "https://google.com/", Domains: tc.domains}, Permissions: Permissions{Steps: []string{"navigate"}, Scripts: []string{}},
					Actions: []string{"open"}, Policy: Policy{Tier: "standard"}, Legacy: &LegacyInfo{Platform: "search_tool"}}
				b, _ := json.MarshalIndent(m, "", "  ")
				files := map[string][]byte{ManifestFile: b, "actions/open.json": []byte(ld["open.json"])}
				p, _ := OpenFS(mapFS(files), SourceLocal)
				if _, err := r.commitLocked(idx, p, files, false); err != nil {
					return false, err
				}
				idx.LegacyHashes = map[string]string{"search_tool": legacy["search_tool"].hash}
				idx.LegacyFormat = 2 // what v0.74 wrote
				return true, nil
			})
			if err != nil {
				t.Fatal(err)
			}
			if err := r.Seed(seedFS("1.0.0", "a")); err != nil {
				t.Fatal(err)
			}
			info, _ := r.Info("local-search-tool")
			if strings.Join(info.Domains, ",") != strings.Join(tc.want, ",") || info.Version != "1.0.1" {
				t.Fatalf("after refold: domains=%v version=%s", info.Domains, info.Version)
			}
		})
	}
}

func TestLegacyExport(t *testing.T) {
	r := newReg(t)
	writeTree(t, filepath.Join(r.Home(), "actions", "search_tool"), map[string]string{
		"open.json": `{"actionType":"open","sideEffects":"read","steps":[{"id":"a","type":"navigate","url":"https://google.com/"}]}`,
	})
	writeTree(t, filepath.Join(r.Home(), "actions", "templ"), map[string]string{
		"go.json": `{"actionType":"go","sideEffects":"read","steps":[{"id":"a","type":"navigate","url":"{{url}}"}]}`,
	})
	r.Seed(seedFS("1.0.0", "a"))
	id, _ := r.ResolveLegacyPlatform("search_tool")

	// Without domains: refused, naming the suggestion and the flag.
	var buf bytes.Buffer
	err := r.Export(id, &buf, ExportOptions{})
	if !errors.Is(err, ErrNotExportable) || !strings.Contains(err.Error(), "--domains *.google.com,google.com") {
		t.Fatalf("export without domains: %v", err)
	}
	if buf.Len() != 0 {
		t.Error("refused export wrote bytes")
	}
	// Bad domains are rejected with the manifest rules.
	for _, d := range []string{"com", "*.co.uk", "localhost"} {
		if err := r.Export(id, &buf, ExportOptions{Domains: []string{d}}); err == nil {
			t.Errorf("--domains %s accepted", d)
		}
	}
	// A domain list that misses a literal navigate host would not install.
	if err := r.Export(id, &buf, ExportOptions{Domains: []string{"example.org"}}); !errors.Is(err, ErrNotExportable) {
		t.Errorf("export with wrong domains: %v", err)
	}
	// With domains: exported copy installs elsewhere; the local package is
	// untouched.
	buf.Reset()
	if err := r.Export(id, &buf, ExportOptions{Domains: []string{"*.google.com"}}); err != nil {
		t.Fatalf("export --domains: %v", err)
	}
	f := filepath.Join(t.TempDir(), "x.mpkg")
	os.WriteFile(f, buf.Bytes(), 0o644)
	if res, err := newReg(t).Install(f, InstallOptions{}); err != nil {
		t.Fatalf("install exported: %v %+v", err, errorsOnly(res.Issues))
	}
	if info, _ := r.Info(id); len(info.Domains) != 0 {
		t.Errorf("export changed the installed package: %v", info.Domains)
	}
	// No literal URL at all: the message asks for --domains.
	tid, _ := r.ResolveLegacyPlatform("templ")
	if err := r.Export(tid, &buf, ExportOptions{}); !errors.Is(err, ErrNotExportable) || !strings.Contains(err.Error(), "--domains <site") {
		t.Errorf("templated-only export: %v", err)
	}
}
