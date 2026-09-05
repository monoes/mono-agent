package linkedin_test

import (
	"os"
	"testing"

	"github.com/monoes/mono-agent/internal/discovery"
	"github.com/monoes/mono-agent/internal/discovery/sources/linkedin"
)

func TestParseSearchPageExtractsFields(t *testing.T) {
	html, err := os.ReadFile("testdata/search_page.html")
	if err != nil {
		t.Fatalf("reading fixture: %v", err)
	}
	results, err := linkedin.ParseSearchPage(string(html))
	if err != nil {
		t.Fatalf("ParseSearchPage: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 results (unrelated card skipped), got %d", len(results))
	}
	if results[0].Title != "Senior Backend Engineer" || results[0].Company != "Acme Corp" {
		t.Fatalf("unexpected first result: %+v", results[0])
	}
	if results[0].URL != "https://www.linkedin.com/jobs/view/1111" {
		t.Fatalf("expected tracking query param stripped, got %q", results[0].URL)
	}
	if results[0].Location != "Berlin, Germany" {
		t.Fatalf("unexpected location: %q", results[0].Location)
	}
	if results[1].Title != "Platform Engineer" || results[1].Company != "Beta Industries" {
		t.Fatalf("unexpected second result: %+v", results[1])
	}
}

// Note: Source.Search calls the real https://www.linkedin.com/robots.txt
// and guest endpoint by hard-coded URL, so it cannot be redirected to a
// test server without a constructor parameter. These tests exercise the
// exported helpers directly (ParseSearchPage above) and the package's
// pure logic; Source.Search itself is integration-level and is not
// exercised against the live network in this test suite — see the design
// spec's "Known Limitation" section. This test asserts the constructor
// and interface satisfaction instead.
func TestSourceImplementsDiscoverySource(t *testing.T) {
	var _ discovery.Source = linkedin.New()
	if linkedin.New().Name() != "linkedin" {
		t.Fatalf("expected Name() to be \"linkedin\", got %q", linkedin.New().Name())
	}
}
