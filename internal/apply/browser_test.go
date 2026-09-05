// internal/apply/browser_test.go
package apply_test

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/monoes/mono-agent/internal/apply"
)

func TestOpenForApplicationLaunchesBrowser(t *testing.T) {
	err := apply.OpenForApplication(context.Background(), "about:blank")
	if err != nil {
		t.Fatalf("OpenForApplication: %v (requires a headless Chrome/Chromium binary reachable by go-rod's launcher — if none is available in this environment, this is an environmental limitation, not a code defect; report it as such rather than treating it as a logic bug)", err)
	}
}

// TestBrowserFileNeverClicksAnything is a literal source-grep, not a
// behavioral test: it asserts internal/apply/browser.go's source text
// contains none of the substrings a click/submit call would use. This is
// the mechanical enforcement of this phase's core safety invariant — see
// docs/mastermind/specs/2026-09-05-apply-automation-design.md. If this
// test ever needs to change, that is a deliberate, reviewed decision to
// weaken the invariant, not a test to "fix" in passing.
func TestBrowserFileNeverClicksAnything(t *testing.T) {
	src, err := os.ReadFile("browser.go")
	if err != nil {
		t.Fatalf("reading browser.go: %v", err)
	}
	forbidden := []string{"Click", "MustClick", ".Submit", "Keyboard.Type", "MustSubmit"}
	for _, f := range forbidden {
		if strings.Contains(string(src), f) {
			t.Fatalf("internal/apply/browser.go must never call anything resembling %q — found it in the source. This file's only job is to navigate to a URL and leave the window open for a human.", f)
		}
	}
}
