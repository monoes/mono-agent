package account_test

import (
	"fmt"
	"go/ast"
	"go/types"
	"strings"
	"testing"
	"time"

	"github.com/monoes/mono-agent/internal/account"
)

// The test seams change process-wide state (the trusted keys, the enforcement date,
// the strict flag, the installed guard), so a test that uses one must not run in
// parallel with another. A rule written in a comment is found out by the first
// parallel test that breaks it, as a flake that depends on test order. So every seam
// also sets a marker with t.Setenv, which the testing package refuses to combine with
// t.Parallel, before or after: the test fails at once, with testing's own message.
func TestEveryGlobalStateSeamPanicsWhenMixedWithTParallel(t *testing.T) {
	seams := []struct {
		name string
		use  func(testing.TB)
	}{
		{"SetTrustedKeysForTest", func(t testing.TB) { account.SetTrustedKeysForTest(t, nil) }},
		{"SetEnforceFromForTest", func(t testing.TB) { account.SetEnforceFromForTest(t, time.Time{}) }},
		{"StrictForTest", account.StrictForTest},
		{"InstallForTest", func(t testing.TB) { account.InstallForTest(t, nil) }},
	}
	for _, s := range seams {
		t.Run(s.name+" then t.Parallel", func(t *testing.T) {
			s.use(t)
			expectParallelConflict(t, "t.Parallel after "+s.name, panicOf(t.Parallel))
		})
	}
	// One parallel subtest, so that a seam that failed to panic could not race another test.
	t.Run("t.Parallel then each seam", func(t *testing.T) {
		t.Parallel()
		for _, s := range seams {
			expectParallelConflict(t, s.name+" after t.Parallel", panicOf(func() { s.use(t) }))
		}
	})
}

// panicOf is what fn panics with, or nil.
func panicOf(fn func()) (v any) {
	defer func() { v = recover() }()
	fn()
	return nil
}

func expectParallelConflict(t *testing.T, what string, v any) {
	t.Helper()
	if msg := fmt.Sprint(v); v == nil || !strings.Contains(msg, "t.Setenv") || !strings.Contains(msg, "t.Parallel") {
		t.Errorf("%s: panicked with %v, want the testing package's refusal to combine t.Setenv with t.Parallel", what, v)
	}
}

// The marker is only ever set: a test seam that read it would be a way to change what
// a binary does with an environment variable, which no build may have (nothing in the
// gate relaxes with the environment). The constant that names it is used only as the
// key of a Setenv, and its text appears once, in that constant.
func TestTheTestStateMarkerIsOnlyEverSet(t *testing.T) {
	const marker = `"MONOAGENT_ACCOUNT_TEST_STATE"`
	literals, uses, setenvs := 0, 0, 0
	for _, dir := range []string{".", "accounttest"} {
		for _, src := range parseSources(t, dir) {
			ast.Inspect(src.file, func(n ast.Node) bool {
				switch n := n.(type) {
				case *ast.BasicLit:
					if n.Value == marker {
						literals++
					}
				case *ast.Ident:
					if n.Name == "testStateEnv" {
						uses++
					}
				case *ast.CallExpr:
					if sel, ok := n.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == "Setenv" && len(n.Args) == 2 {
						if id, ok := n.Args[0].(*ast.Ident); ok && id.Name == "testStateEnv" {
							setenvs++
						}
					} else {
						for _, arg := range n.Args {
							if id, ok := arg.(*ast.Ident); ok && id.Name == "testStateEnv" {
								t.Errorf("%s: testStateEnv is passed to %s: the marker must only ever be set, with t.Setenv", src.name, types.ExprString(n.Fun))
							}
						}
					}
				}
				return true
			})
		}
	}
	if setenvs < 4 {
		t.Errorf("the marker is set by %d calls of t.Setenv(testStateEnv, ...), want one in each of the four hooks of this package", setenvs)
	}
	// every mention of the constant is the declaration or a Setenv, so none reads it
	if uses != setenvs+1 {
		t.Errorf("testStateEnv is mentioned %d times, want %d: its declaration and each t.Setenv(testStateEnv, ...): something else uses it", uses, setenvs+1)
	}
	if literals != 1 {
		t.Errorf("the marker's name appears %d times in the sources, want once, in the constant that declares it", literals)
	}
}
