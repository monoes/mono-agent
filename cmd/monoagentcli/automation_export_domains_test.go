package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/monoes/mono-agent/internal/automation"
)

// legacyGoogleMapsHome makes a HOME whose ~/.monoagent/actions/google_maps
// is wrapped by the registry into local-google-maps (no site.domains).
func legacyGoogleMapsHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	dir := filepath.Join(home, ".monoagent", "actions", "google_maps")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(dir, "search.json"), []byte(`{"actionType":"search","platform":"GOOGLE_MAPS",
		"steps":[{"id":"open","type":"navigate","url":"https://www.google.com/maps"}]}`), 0o644)
	return home
}

// TestAutomationExportDomains (D8): a legacy package without domains is
// not exportable as is; --domains sets them in the exported copy only.
func TestAutomationExportDomains(t *testing.T) {
	home := legacyGoogleMapsHome(t)
	file := filepath.Join(home, "maps.mpkg")

	out, _, err := runAutomationCLI(t, home, "automation", "export", "local-google-maps", "-o", file, "--json")
	if err == nil || !strings.Contains(out, "cannot export local-google-maps") || !strings.Contains(out, "--domains") {
		t.Fatalf("export without domains: err=%v out=%s", err, out)
	}
	if _, serr := os.Stat(file); !os.IsNotExist(serr) {
		t.Fatal("a refused export left a file behind")
	}

	out, _, err = runAutomationCLI(t, home, "automation", "export", "local-google-maps", "-o", file, "--domains", "com", "--json")
	if err == nil || !strings.Contains(out, `domain \"com\"`) {
		t.Fatalf("public-suffix domain: err=%v out=%s", err, out)
	}

	var res map[string]string
	mustJSON(t, home, &res, "automation", "export", "local-google-maps", "-o", file, "--domains", "www.google.com, google.com")
	p, err := automation.OpenFile(file)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(p.Manifest.Site.Domains, ","); got != "www.google.com,google.com" {
		t.Fatalf("exported domains = %q", got)
	}
	// The installed package is unchanged.
	var show struct {
		Manifest automation.Manifest `json:"manifest"`
	}
	mustJSON(t, home, &show, "automation", "show", "local-google-maps")
	if len(show.Manifest.Site.Domains) != 0 {
		t.Fatalf("installed domains changed: %v", show.Manifest.Site.Domains)
	}
}

// TestAutomationInfoIssuesNotCounted: "info" notes are printed but not
// counted as problems.
func TestAutomationInfoIssuesNotCounted(t *testing.T) {
	issues := []automation.IssueJSON{
		{Severity: "info", Code: "legacy_suggested_domains", Message: "suggest www.google.com"},
	}
	var b strings.Builder
	printIssues(&b, issues)
	if !strings.Contains(b.String(), "notes:") || !strings.Contains(b.String(), "info") || strings.Contains(b.String(), "issue(s)") {
		t.Fatalf("info only:\n%s", b.String())
	}
	b.Reset()
	printIssues(&b, append([]automation.IssueJSON{{Severity: "warning", Code: "w", Message: "m"}}, issues...))
	if !strings.Contains(b.String(), "1 issue(s)") {
		t.Fatalf("warning + info:\n%s", b.String())
	}
	if countProblems(issues) != 0 {
		t.Fatal("info counted as a problem")
	}

	// A legacy package's validate row counts no info note as a warning.
	home := legacyGoogleMapsHome(t)
	var tr struct {
		Results []fixtureResult `json:"results"`
	}
	mustJSON(t, home, &tr, "automation", "test", "local-google-maps")
	var show struct {
		Issues []automation.IssueJSON `json:"issues"`
	}
	mustJSON(t, home, &show, "automation", "show", "local-google-maps")
	infos := 0
	for _, is := range show.Issues {
		if is.Severity == "info" {
			infos++
		}
	}
	if infos == 0 {
		t.Fatalf("expected legacy info notes, got %+v", show.Issues)
	}
	want := fmt.Sprintf("validate: %d error(s), %d warning(s)", countErrors(show.Issues), len(show.Issues)-infos-countErrors(show.Issues))
	if !strings.HasPrefix(tr.Results[0].Message, want) {
		t.Fatalf("validate row %q, want prefix %q (issues %+v)", tr.Results[0].Message, want, show.Issues)
	}
}

func countErrors(issues []automation.IssueJSON) int {
	n := 0
	for _, is := range issues {
		if is.Severity == "error" {
			n++
		}
	}
	return n
}

// TestAutomationExportSuggestedDomains: --use-suggested-domains on
// automation export and action export.
func TestAutomationExportSuggestedDomains(t *testing.T) {
	home := legacyGoogleMapsHome(t)
	var show struct {
		Manifest automation.Manifest `json:"manifest"`
	}
	mustJSON(t, home, &show, "automation", "show", "local-google-maps")
	if show.Manifest.Legacy == nil || len(show.Manifest.Legacy.SuggestedDomains) == 0 {
		t.Fatalf("no suggestion: %+v", show.Manifest.Legacy)
	}
	want := strings.Join(show.Manifest.Legacy.SuggestedDomains, ",")

	for _, args := range [][]string{
		{"automation", "export", "local-google-maps"},
		{"action", "export", "local-google-maps.search"},
	} {
		file := filepath.Join(t.TempDir(), "x.mpkg")
		var res map[string]string
		mustJSON(t, home, &res, append(args, "-o", file, "--use-suggested-domains")...)
		p, err := automation.OpenFile(file)
		if err != nil {
			t.Fatal(err)
		}
		if got := strings.Join(p.Manifest.Site.Domains, ","); got != want {
			t.Fatalf("%v: domains %q, want %q", args, got, want)
		}
		// action export takes --domains too.
		if args[0] == "action" {
			mustJSON(t, home, &res, append(args, "-o", file, "--domains", "www.google.com")...)
			if p, _ := automation.OpenFile(file); strings.Join(p.Manifest.Site.Domains, ",") != "www.google.com" {
				t.Fatalf("action export --domains: %v", p.Manifest.Site.Domains)
			}
		}
	}

	out, _, err := runAutomationCLI(t, home, "automation", "export", "local-google-maps", "-o", filepath.Join(home, "y.mpkg"),
		"--use-suggested-domains", "--domains", "a.test", "--json")
	if err == nil || !strings.Contains(out, "mutually exclusive") {
		t.Fatalf("both flags: %v %s", err, out)
	}
	out, _, err = runAutomationCLI(t, home, "automation", "export", "hackernews", "-o", filepath.Join(home, "hn.mpkg"),
		"--use-suggested-domains", "--json")
	if err == nil || !strings.Contains(out, "not a generated legacy package") {
		t.Fatalf("non-legacy: %v %s", err, out)
	}
}

// TestAutomationExportSuggestedDomainsLocalOnly: a legacy package that only
// opens a local address has nothing to suggest.
func TestAutomationExportSuggestedDomainsLocalOnly(t *testing.T) {
	home := t.TempDir()
	dir := filepath.Join(home, ".monoagent", "actions", "devsite")
	os.MkdirAll(dir, 0o755)
	os.WriteFile(filepath.Join(dir, "ping.json"), []byte(`{"actionType":"ping","platform":"DEVSITE",
		"steps":[{"id":"open","type":"navigate","url":"http://localhost:3000/"}]}`), 0o644)
	out, _, err := runAutomationCLI(t, home, "automation", "export", "local-devsite", "-o", filepath.Join(home, "d.mpkg"),
		"--use-suggested-domains", "--json")
	if err == nil || !strings.Contains(out, "only opens local addresses") {
		t.Fatalf("local-only: %v %s", err, out)
	}
}
