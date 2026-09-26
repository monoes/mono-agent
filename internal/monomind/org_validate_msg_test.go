package monomind

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestOrgValidateKeepsMessage: a failing `monomind org validate` returns
// its printed problems, not just "exit status 1" (CombinedOutput leaves
// ExitError.Stderr empty).
func TestOrgValidateKeepsMessage(t *testing.T) {
	script := `#!/bin/sh
if [ "$1" = "--version" ] && [ "$2" = "--json" ]; then
  echo '{"v":1,"version":"2.10.0","min_caller":"1.0.0","capabilities":["agent-exec","agent-scan","org-json-v1"]}'
  exit 0
fi
echo "role writer reports to missing role lead" >&2
exit 1
`
	p := filepath.Join(t.TempDir(), "monomind")
	if err := os.WriteFile(p, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv(EnvOverride, p)
	_, err := OrgValidate(context.Background(), t.TempDir(), "growth")
	if err == nil || !strings.Contains(err.Error(), "reports to missing role lead") {
		t.Fatalf("err = %v", err)
	}
}
