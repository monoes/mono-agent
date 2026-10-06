package account_test

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"go/types"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// Every exported function named *ForTest is a test seam, and a seam that runs in a
// release binary is a hole in the gate: SetTrustedKeysForTest would let a process
// replace the signing keys it trusts, SetEnforceFromForTest move the enforcement
// date, InstallForTest swap the guard. Each therefore starts, after t.Helper(), with
// requireTestBinary(<its own name>), which panics outside a test binary, before it
// touches a package-level variable or the lock. Dropping, renaming or delaying that
// call cannot be seen from inside a test binary (testing.Testing() is true in every
// one), so the sources of this package and of accounttest are read as syntax. In
// accounttest, where requireTestBinary is out of reach, the refusal is a call to an
// account.*ForTest hook, which refuses by itself.
func TestEveryForTestHookRefusesToRunInAReleaseBinary(t *testing.T) {
	var found []string
	for _, dir := range []string{".", "accounttest"} {
		sources := parseSources(t, dir)
		vars := packageVars(sources)
		for _, src := range sources {
			for _, d := range src.file.Decls {
				fd, ok := d.(*ast.FuncDecl)
				if !ok || fd.Body == nil || !fd.Name.IsExported() || !strings.HasSuffix(fd.Name.Name, "ForTest") {
					continue
				}
				found = append(found, fd.Name.Name)
				if problem := hookProblem(fd, vars, dir != "."); problem != "" {
					t.Errorf("%s: %s %s", filepath.Join(dir, src.name), fd.Name.Name, problem)
				}
			}
		}
	}
	// A scan that finds nothing proves nothing: the hooks that exist must be seen.
	for _, want := range []string{"SetTrustedKeysForTest", "SetEnforceFromForTest", "StrictForTest", "InstallForTest"} {
		if !slices.Contains(found, want) {
			t.Errorf("the scan did not find %s (found %v): it was renamed or moved, or the scan looks in the wrong place", want, found)
		}
	}
}

type source struct {
	name string
	file *ast.File
}

// parseSources parses the non-test Go files of dir, whatever their build tags.
func parseSources(t *testing.T, dir string) []source {
	t.Helper()
	names, err := filepath.Glob(filepath.Join(dir, "*.go"))
	if err != nil || len(names) == 0 {
		t.Fatalf("no sources in %s (%v)", dir, err)
	}
	fset := token.NewFileSet()
	var out []source
	for _, name := range names {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, source{filepath.Base(name), file})
	}
	return out
}

// packageVars is the name of every package-level variable of the sources, the lock among them.
func packageVars(sources []source) map[string]bool {
	vars := map[string]bool{}
	for _, src := range sources {
		for _, d := range src.file.Decls {
			if gd, ok := d.(*ast.GenDecl); ok && gd.Tok == token.VAR {
				for _, spec := range gd.Specs {
					for _, n := range spec.(*ast.ValueSpec).Names {
						vars[n.Name] = true
					}
				}
			}
		}
	}
	return vars
}

// hookProblem says what is wrong with the start of a hook, or "" when it refuses to
// run outside a test binary before it touches a package-level variable or the lock.
func hookProblem(fd *ast.FuncDecl, vars map[string]bool, viaAccount bool) string {
	want := fmt.Sprintf("must call requireTestBinary(%q) before it touches any package-level variable or the lock", fd.Name.Name)
	if viaAccount {
		want = "must call an account.*ForTest hook, which refuses by itself, before it touches any package-level variable"
	}
	for _, st := range fd.Body.List {
		if refuses(st, fd.Name.Name, viaAccount) {
			return ""
		}
		if v := touches(st, vars); v != "" {
			return want + "; it touches " + v + " first"
		}
	}
	return want + "; it never does"
}

// refuses reports whether st is the call that makes the function refuse to run in a
// release binary: requireTestBinary(name), or mustBeTestBinary(testing.Testing(), name),
// or, from accounttest, a call to an account.*ForTest hook.
func refuses(st ast.Stmt, name string, viaAccount bool) bool {
	es, ok := st.(*ast.ExprStmt)
	if !ok {
		return false
	}
	if !viaAccount {
		got := types.ExprString(es.X)
		return got == fmt.Sprintf("requireTestBinary(%q)", name) || got == fmt.Sprintf("mustBeTestBinary(testing.Testing(), %q)", name)
	}
	call, ok := es.X.(*ast.CallExpr)
	if !ok {
		return false
	}
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	pkg, ok := sel.X.(*ast.Ident)
	return ok && pkg.Name == "account" && strings.HasSuffix(sel.Sel.Name, "ForTest")
}

// touches is the first package-level variable that n uses, or "". The f of x.f is
// not a use of anything named f.
func touches(n ast.Node, vars map[string]bool) string {
	found := ""
	ast.Inspect(n, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.SelectorExpr:
			if found == "" {
				found = touches(n.X, vars)
			}
			return false
		case *ast.Ident:
			if found == "" && vars[n.Name] {
				found = n.Name
			}
		}
		return found == ""
	})
	return found
}
