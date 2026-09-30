package monomind

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// Free text sent to `monomind org answer|gate-approve|gate-reject` goes after
// "--": an answer that starts with a dash must reach monomind as the answer,
// not as a flag (#267 follow-up).
func TestOrgAnswerAndGateTextAfterDashDash(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("needs sh")
	}
	dir := t.TempDir()
	argsFile := filepath.Join(dir, "args")
	bin := filepath.Join(dir, "monomind")
	script := "#!/bin/sh\n" +
		`if [ "$1" = "--version" ]; then echo '{"v":1,"version":"9.0.0","min_caller":"1.0.0","capabilities":["agent-exec","agent-scan","org-json-v1","` + CapOrgDecisionAttribution + `"]}'; exit 0; fi` + "\n" +
		`for a in "$@"; do printf '%s\n' "$a"; done > '` + argsFile + "'\necho '{\"ok\":true}'\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv(EnvOverride, bin)
	ResetCapabilityCache()
	t.Cleanup(ResetCapabilityCache)
	ctx := context.Background()
	got := func() string { b, _ := os.ReadFile(argsFile); return strings.TrimSpace(string(b)) }
	lines := func(a ...string) string { return strings.Join(a, "\n") }

	for _, tc := range []struct {
		name string
		run  func() error
		want string
	}{
		{"answer", func() error { _, err := OrgAnswer(ctx, dir, "acme", "q-1", "--by=rule"); return err },
			lines("org", "answer", "--format", "json", "--", "acme", "q-1", "--by=rule")},
		{"answer attributed", func() error {
			_, err := OrgAnswerWith(ctx, dir, "acme", "q-1", "-rf everything", ResolveOptions{By: "human"})
			return err
		}, lines("org", "answer", "--by", "human", "--format", "json", "--", "acme", "q-1", "-rf everything")},
		{"gate attributed", func() error {
			_, err := OrgGateResolveWith(ctx, dir, "acme", "gate-1", true, "--format=text", ResolveOptions{By: "human"})
			return err
		}, lines("org", "gate-approve", "--by", "human", "--format", "json", "--", "acme", "gate-1", "--format=text")},
		{"gate reject", func() error { _, err := OrgGateReject(ctx, dir, "acme", "gate-1", "--by=rule"); return err },
			lines("org", "gate-reject", "--format", "json", "--", "acme", "gate-1", "--by=rule")},
	} {
		if err := tc.run(); err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if g := got(); g != tc.want {
			t.Errorf("%s: monomind got\n%s\nwant\n%s", tc.name, g, tc.want)
		}
	}
}
