package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/monoes/mono-agent/internal/account"
)

// enforcementClaims are phrases that would present the dormant gate or the
// unpinned signature check as active today.
var enforcementClaims = []string{"gate is enforced", "account is required", "signatures are verified", "verified by ed25519 signature", "verified by sha256 and ed25519 signature"}

// assertNoEnforcementClaims fails for each phrase in text that claims enforcement.
func assertNoEnforcementClaims(t *testing.T, name, text string) {
	t.Helper()
	text = strings.ToLower(text)
	for _, bad := range enforcementClaims {
		if strings.Contains(text, bad) {
			t.Errorf("%s claims enforcement or active verification: %q", name, bad)
		}
	}
}

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
		assertNoEnforcementClaims(t, name, text)
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
