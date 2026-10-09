package account

import (
	"encoding/hex"
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
			if !importAllowed(name, path) {
				t.Errorf("%s imports %s, which the import rule does not allow", name, path)
			}
		}
	}
}

// importAllowed is the import rule for the non-test file called file.
func importAllowed(file, path string) bool {
	first, _, _ := strings.Cut(path, "/")
	switch {
	case !strings.Contains(first, "."): // the standard library
		return true
	case path == "github.com/monoes/mono-agent/internal/secrets":
		return true
	case path == "golang.org/x/sys/windows":
		return file == "lock_windows.go" || file == "rename_windows.go"
	}
	return false
}

// The rule is as strict as it reads: x/sys/windows only in the two files that make
// a Windows system call, and nothing else outside the standard library but
// internal/secrets. A rule that nothing tests could be loosened without a failure.
func TestTheImportRuleForbidsWhatItSays(t *testing.T) {
	const secrets = "github.com/monoes/mono-agent/internal/secrets"
	const windows = "golang.org/x/sys/windows"
	for _, c := range []struct {
		file, path string
		want       bool
	}{
		{"store.go", "os", true},
		{"store.go", "encoding/json", true},
		{"sealer.go", secrets, true},
		{"lock_windows.go", windows, true},
		{"rename_windows.go", windows, true},
		{"store.go", windows, false},
		{"guard_refresh.go", windows, false},
		{"rename_unix.go", windows, false},
		{"readfile_windows.go", windows, false},
		{"lock_unix.go", "golang.org/x/sys/unix", false},
		{"lock_windows.go", "golang.org/x/sys/unix", false},
		{"lock_windows.go", "golang.org/x/sys/windows/registry", false},
		{"store.go", "golang.org/x/sys/cpu", false},
		{"sealer.go", "github.com/monoes/mono-agent/internal/library", false},
		{"sealer.go", "github.com/monoes/mono-agent/internal/secrets/other", false},
		{"store.go", "github.com/stretchr/testify/assert", false},
	} {
		if got := importAllowed(c.file, c.path); got != c.want {
			t.Errorf("importAllowed(%q, %q) = %v, want %v", c.file, c.path, got, c.want)
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

// The production signing key is pinned exactly as monoes.me published it
// (https://monoes.me/api/auth/jwks, 2026-10-09). This fails if the constant is
// edited by accident; a deliberate rotation updates it together with the server.
func TestProductionSigningKeyIsPinned(t *testing.T) {
	const wantKID = "GB6kESA9qO98637VArEGR2EjW6wSyYqO"
	const wantHex = "ca570571b89cde8feb93c0e03581b78968ed75a953ac9bd349f01edf2ee9d26f"
	for _, k := range pinnedKeys {
		if k.KID == wantKID {
			if got := hex.EncodeToString(k.Public); got != wantHex {
				t.Fatalf("pinned key %q has public key %s, want %s", wantKID, got, wantHex)
			}
			return
		}
	}
	t.Fatalf("the production signing key %q is not pinned", wantKID)
}
