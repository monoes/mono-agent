package i18n

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// TestT_LocaleSwitching checks that T() returns the right string per active
// locale, and falls back to English (then the raw key) when a key or locale
// is missing.
func TestT_LocaleSwitching(t *testing.T) {
	t.Cleanup(func() { SetLocale(DefaultLocale) })

	SetLocale("en")
	if got := T("node.short"); got != "Directly invoke or inspect workflow node types" {
		t.Fatalf("en node.short = %q", got)
	}

	SetLocale("es")
	if got := T("node.short"); got != "Invoca o inspecciona directamente tipos de nodo de flujo de trabajo" {
		t.Fatalf("es node.short = %q", got)
	}

	// Unknown locale falls back to English rather than erroring.
	SetLocale("xx-does-not-exist")
	if CurrentLocale() != DefaultLocale {
		t.Fatalf("expected fallback to %q, got %q", DefaultLocale, CurrentLocale())
	}

	// Unknown key falls back to the key itself.
	SetLocale("en")
	if got := T("this.key.does.not.exist"); got != "this.key.does.not.exist" {
		t.Fatalf("missing key fallback = %q", got)
	}
}

// TestT_Format checks that args are applied via fmt.Sprintf when present.
func TestT_Format(t *testing.T) {
	t.Cleanup(func() { SetLocale(DefaultLocale) })
	SetLocale("en")
	if got := T("this.has.%s.and.%d", "text", 3); got != "this.has.%s.and.%d" {
		// Missing key: format is NOT applied to the raw key fallback,
		// only to a resolved locale string. This asserts that contract.
		t.Fatalf("unexpected fallback formatting: %q", got)
	}
}

// keyCallRE matches i18n.T("some.key" calls in Go source, capturing the
// string literal key. It intentionally does not match dynamic (non-literal)
// keys — those can't be statically checked and must be reviewed by hand.
var keyCallRE = regexp.MustCompile(`i18n\.T\(\s*"([^"]+)"`)

// TestLocaleKeys_CoverCodeReferences walks cmd/monoagentcli for i18n.T("key")
// call sites and fails if any referenced key is missing from the embedded
// English locale — this is the drift check called out in docs/i18n.md: it
// catches typos and stale keys, not full coverage (unused keys are fine).
func TestLocaleKeys_CoverCodeReferences(t *testing.T) {
	en, err := loadLocale("en")
	if err != nil {
		t.Fatalf("load en locale: %v", err)
	}

	cmdDir := filepath.Join("..", "..", "cmd", "monoagentcli")
	var missing []string

	err = filepath.WalkDir(cmdDir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".go") {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for _, m := range keyCallRE.FindAllStringSubmatch(string(data), -1) {
			key := m[1]
			if _, ok := en[key]; !ok {
				missing = append(missing, path+": "+key)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", cmdDir, err)
	}

	if len(missing) > 0 {
		t.Fatalf("i18n.T() call(s) reference key(s) missing from locales/en.json:\n%s", strings.Join(missing, "\n"))
	}
}
