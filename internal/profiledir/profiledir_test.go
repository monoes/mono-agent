package profiledir

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestValidProfileID(t *testing.T) {
	valid := []string{"default", "work", "personal-2", "org_1", "a.b"}
	for _, id := range valid {
		if !ValidProfileID(id) {
			t.Errorf("ValidProfileID(%q) = false, want true", id)
		}
	}
	invalid := []string{"", "..", "a/b", `a\b`, "../evil", "evil/..", "a..b", "..a"}
	for _, id := range invalid {
		if ValidProfileID(id) {
			t.Errorf("ValidProfileID(%q) = true, want false", id)
		}
	}
}

func TestRoot_DefaultJoin(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	got := Root(nil, "work")
	want := filepath.Join(home, ".monoagent", "profiles", "work")
	if got != want {
		t.Errorf("Root(nil, \"work\") = %q, want %q", got, want)
	}
	if VaultDir(nil, "work") != filepath.Join(want, "vault") {
		t.Errorf("VaultDir mismatch: %q", VaultDir(nil, "work"))
	}
	if MonomindDir(nil, "work") != filepath.Join(want, ".monomind") {
		t.Errorf("MonomindDir mismatch: %q", MonomindDir(nil, "work"))
	}
}

// TestRoot_RejectsTraversal is the F1-7 guard: a profileID containing path
// separators or ".." must never produce a path outside the profiles root —
// Root returns the dead path "" for such IDs.
func TestRoot_RejectsTraversal(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	for _, id := range []string{"", "..", "../evil", "evil/..", `..\evil`, "a..b"} {
		if got := Root(nil, id); got != "" {
			t.Errorf("Root(nil, %q) = %q, want \"\" (rejected)", id, got)
		}
		// Derived dirs must not escape the home dir either.
		for name, dir := range map[string]string{
			"VaultDir":    VaultDir(nil, id),
			"MonomindDir": MonomindDir(nil, id),
		} {
			if strings.HasPrefix(dir, "..") {
				t.Errorf("%s(nil, %q) = %q escapes via relative ..", name, id, dir)
			}
		}
	}
}

func TestEnsureLayout(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	if err := EnsureLayout(nil, "../evil"); err == nil {
		t.Error("EnsureLayout with traversal ID should fail, got nil")
	}
	if err := EnsureLayout(nil, ""); err == nil {
		t.Error("EnsureLayout with empty ID should fail, got nil")
	}

	if err := EnsureLayout(nil, "work"); err != nil {
		t.Fatalf("EnsureLayout: %v", err)
	}
	if !Exists(nil, "work") {
		t.Error("Exists should report true after EnsureLayout")
	}
	if Exists(nil, "../evil") {
		t.Error("Exists should report false for a rejected ID")
	}

	// Idempotent.
	if err := EnsureLayout(nil, "work"); err != nil {
		t.Fatalf("EnsureLayout (repeat): %v", err)
	}
}
