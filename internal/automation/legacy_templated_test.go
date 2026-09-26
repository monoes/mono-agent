package automation

import (
	"bytes"
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

func TestTemplateHost(t *testing.T) {
	cases := []struct{ raw, glob, host, scheme string }{
		{"https://{{lang}}.google.com/maps", "*.google.com", "", ""},
		{"https://maps.google.com/?q={{q}}", "", "maps.google.com", "https"},
		{"http://intranet.example.org/{{page}}", "", "intranet.example.org", "http"},
		{"https://{{a}}.{{b}}.example.co.uk/x", "*.example.co.uk", "", ""},
		{"https://{{domain}}.com/", "", "", ""},
		{"{{url}}", "", "", ""},
		{"{{base}}/path", "", "", ""},
	}
	for _, c := range cases {
		g, h, sch := templateHost(c.raw)
		if g != c.glob || h != c.host || sch != c.scheme {
			t.Errorf("templateHost(%q) = %q,%q,%q want %q,%q,%q", c.raw, g, h, sch, c.glob, c.host, c.scheme)
		}
	}
}

func TestWWWCounterpart(t *testing.T) {
	for h, want := range map[string]string{
		"example.com": "www.example.com", "www.example.com": "example.com", "example.com:8443": "www.example.com:8443",
		"crm.e2e.test": "", "maps.google.com": "", "www.example.co.uk": "example.co.uk",
	} {
		if got := wwwCounterpart(h); got != want {
			t.Errorf("wwwCounterpart(%q) = %q, want %q", h, got, want)
		}
	}
}

// Literal hosts never widen to a wildcard; the start URL keeps its scheme.
func TestLegacySuggestionsStayNarrow(t *testing.T) {
	r := newReg(t)
	writeTree(t, filepath.Join(r.Home(), "actions", "crm"), map[string]string{
		"open.json": `{"actionType":"open","sideEffects":"read","steps":[
  {"id":"a","type":"navigate","url":"http://crm.e2e.test/login"},
  {"id":"b","type":"navigate","url":"https://example.com/"}]}`,
	})
	writeTree(t, filepath.Join(r.Home(), "actions", "maps"), map[string]string{
		"q.json": `{"actionType":"q","sideEffects":"read","steps":[{"id":"a","type":"navigate","url":"https://maps.google.com/?q={{q}}"}]}`,
	})
	if err := r.Seed(seedFS("1.0.0", "a")); err != nil {
		t.Fatal(err)
	}
	crm, _ := r.ResolveLegacyPlatform("crm")
	p, _ := r.Get(crm)
	if s := strings.Join(p.Manifest.Legacy.SuggestedDomains, ","); s != "crm.e2e.test,example.com,www.example.com" {
		t.Errorf("literal suggestions: %s", s)
	}
	if p.Manifest.Site.StartURL != "http://crm.e2e.test/" {
		t.Errorf("startUrl %q (scheme must follow the actions)", p.Manifest.Site.StartURL)
	}
	// The suggestion exports and allows exactly what the actions open.
	var buf bytes.Buffer
	if err := r.Export(crm, &buf, ExportOptions{Domains: p.Manifest.Legacy.SuggestedDomains}); err != nil {
		t.Errorf("export with the suggestion: %v", err)
	}

	// Template only in the path: the literal host, no wildcard; the hint
	// says to add one if the site redirects.
	mid, _ := r.ResolveLegacyPlatform("maps")
	mp, _ := r.Get(mid)
	if s := strings.Join(mp.Manifest.Legacy.SuggestedDomains, ","); s != "maps.google.com" {
		t.Errorf("path-template suggestions: %s", s)
	}
	err := r.Export(mid, &buf, ExportOptions{})
	if err == nil || !strings.Contains(err.Error(), "--domains maps.google.com") ||
		!strings.Contains(err.Error(), "add a wildcard like *.google.com if they redirect to other subdomains") {
		t.Errorf("path-template refusal: %v", err)
	}
}

// N4: templated navigates make the suggestion incomplete; say so, and
// derive the registrable wildcard where the template has a literal domain.
func TestLegacyTemplatedURLHints(t *testing.T) {
	r := newReg(t)
	writeTree(t, filepath.Join(r.Home(), "actions", "google_maps"), map[string]string{
		"open.json": `{"actionType":"open","sideEffects":"read","steps":[
  {"id":"a","type":"navigate","url":"{{url}}"},
  {"id":"b","type":"navigate","url":"https://{{lang}}.google.com/maps"}]}`,
	})
	writeTree(t, filepath.Join(r.Home(), "actions", "anysite"), map[string]string{
		"go.json": `{"actionType":"go","sideEffects":"read","steps":[{"id":"a","type":"navigate","url":"{{url}}"}]}`,
	})
	if err := r.Seed(seedFS("1.0.0", "a")); err != nil {
		t.Fatal(err)
	}

	gm, _ := r.ResolveLegacyPlatform("google_maps")
	p, _ := r.Get(gm)
	if l := p.Manifest.Legacy; !l.TemplatedURLs || strings.Join(l.SuggestedDomains, ",") != "*.google.com" {
		t.Fatalf("legacy info: %+v", l)
	}
	var note string
	for _, is := range Validate(p) {
		if is.Code == "legacy_templated_urls" && is.Severity == "info" {
			note = is.Message
		}
	}
	if !strings.Contains(note, "built at run time") || !strings.Contains(note, "*.google.com") {
		t.Errorf("info note: %q", note)
	}
	var buf bytes.Buffer
	err := r.Export(gm, &buf, ExportOptions{})
	if !errors.Is(err, ErrNotExportable) || !strings.Contains(err.Error(), "--domains *.google.com") ||
		!strings.Contains(err.Error(), "list every site they may reach — prefer a wildcard like *.google.com over exact hosts") {
		t.Errorf("export refusal: %v", err)
	}

	// Nothing literal at all: the hint uses a neutral example.
	any, _ := r.ResolveLegacyPlatform("anysite")
	err = r.Export(any, &buf, ExportOptions{})
	if !errors.Is(err, ErrNotExportable) || !strings.Contains(err.Error(), "URLs built at run time") || !strings.Contains(err.Error(), "*.example.com") {
		t.Errorf("templated-only refusal: %v", err)
	}
	// A package with only literal URLs gets no templated hint.
	lit := newReg(t)
	writeTree(t, filepath.Join(lit.Home(), "actions", "plain"), map[string]string{
		"go.json": `{"actionType":"go","sideEffects":"read","steps":[{"id":"a","type":"navigate","url":"https://example.org/"}]}`,
	})
	lit.Seed(seedFS("1.0.0", "a"))
	pid, _ := lit.ResolveLegacyPlatform("plain")
	if err := lit.Export(pid, &buf, ExportOptions{}); err == nil || strings.Contains(err.Error(), "built at run time") {
		t.Errorf("literal-only refusal: %v", err)
	}
}
