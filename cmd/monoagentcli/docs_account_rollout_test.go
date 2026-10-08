package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/monoes/mono-agent/internal/account"
)

// These shipped claims deliberately describe a dormant rollout. Enabling the
// date or pinning production keys needs a simultaneous release-doc review,
// rather than silently leaving the front doors describing yesterday's build.
func TestAccountDocsMatchDormantRollout(t *testing.T) {
	if !account.EnforceDate().IsZero() {
		t.Fatal("account rollout enabled: update dormant front-door claims and release notes together")
	}
	for _, name := range []string{"README.md", "AGENTS.md", "SECURITY.md", "SUPPORT.md", "docs/COMPARISON.md", "CHANGELOG.md", "internal/i18n/locales/en.json"} {
		data, err := os.ReadFile(filepath.Join("..", "..", name))
		if err != nil {
			t.Fatal(err)
		}
		text := strings.ToLower(string(data))
		if !strings.Contains(text, "dormant") {
			t.Errorf("%s hides current dormant rollout", name)
		}
		for _, stale := range []string{"local-first", "works fully offline", "nothing is sent to us", "no outbound calls on its own behalf"} {
			if strings.Contains(text, stale) {
				t.Errorf("%s contains obsolete product claim %q", name, stale)
			}
		}
	}
	for _, name := range []string{"README.md", "AGENTS.md", "SECURITY.md"} {
		data, err := os.ReadFile(filepath.Join("..", "..", name))
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(data), "production signing keys") && !strings.Contains(string(data), "Production signing") && !strings.Contains(string(data), "production key set is currently empty") {
			t.Errorf("%s omits current production verification limitation", name)
		}
	}
}
