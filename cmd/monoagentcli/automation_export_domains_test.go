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
