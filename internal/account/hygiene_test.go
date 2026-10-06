package account

import (
	"go/parser"
	"go/token"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// The import rule (index §3.1): this package imports the standard library,
// golang.org/x/sys (the Windows lock and rename) and internal/secrets, and nothing
// else of this repository, so that every other package may import it without a
// cycle.
func TestImportsOnlyWhatTheImportRuleAllows(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil || len(files) == 0 {
		t.Fatalf("no source files found (%v)", err)
	}
	fset := token.NewFileSet()
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, name, nil, parser.ImportsOnly)
		if err != nil {
			t.Fatal(err)
		}
		for _, imp := range f.Imports {
			path, _ := strconv.Unquote(imp.Path.Value)
			first, _, _ := strings.Cut(path, "/")
			switch {
			case !strings.Contains(first, "."): // the standard library
			case path == "github.com/monoes/mono-agent/internal/secrets":
			case path == "golang.org/x/sys/windows" && (name == "lock_windows.go" || name == "rename_windows.go"):
			default:
				t.Errorf("%s imports %s, which the import rule does not allow", name, path)
			}
		}
	}
}

// B5a sets the enforcement date. A build that enforces but pins no key would
// refuse every real token, so the date and the first pinned key must arrive
// together.
func TestEnforcedBuildPinsAKey(t *testing.T) {
	if !EnforceDate().IsZero() && len(pinnedKeys) == 0 {
		t.Fatal("the enforcement date is set but no signing key is pinned: every token would be refused as key_unknown")
	}
}

// Every pinned key must be a well-formed Ed25519 key with a unique kid.
func TestPinnedKeysAreWellFormed(t *testing.T) {
	seen := map[string]bool{}
	for _, k := range TrustedKeys() {
		if k.KID == "" || seen[k.KID] || len(k.Public) != 32 {
			t.Errorf("malformed or duplicate pinned key %q (%d bytes)", k.KID, len(k.Public))
		}
		seen[k.KID] = true
	}
}
