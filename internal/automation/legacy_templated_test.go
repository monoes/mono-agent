package automation

import (
	"bytes"
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

func TestTemplateHost(t *testing.T) {
	cases := []struct{ raw, glob, host string }{
		{"https://{{lang}}.google.com/maps", "*.google.com", ""},
		{"https://maps.google.com/?q={{q}}", "", "maps.google.com"},
		{"https://{{a}}.{{b}}.example.co.uk/x", "*.example.co.uk", ""},
		{"https://{{domain}}.com/", "", ""},
		{"{{url}}", "", ""},
		{"{{base}}/path", "", ""},
	}
	for _, c := range cases {
		g, h := templateHost(c.raw)
		if g != c.glob || h != c.host {
			t.Errorf("templateHost(%q) = %q,%q want %q,%q", c.raw, g, h, c.glob, c.host)
		}
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
