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
// requireTestBinary(<its own name>), which panics outside a test binary, and nothing
// runs before it: not a package-level variable, not the lock, not t.Setenv (the
// environment is a global too). Dropping, renaming or delaying that call cannot be
// seen from inside a test binary (testing.Testing() is true in every one), so the
// sources of this package and of accounttest are read as syntax. In accounttest, where
// requireTestBinary is out of reach, the refusal is a call to an account.*ForTest hook,
// which refuses by itself.
//
// That the hooks call requireTestBinary proves nothing if its body can change: with
// mustBeTestBinary(true, name) in place of mustBeTestBinary(testing.Testing(), name)
// every hook would run in a release binary and every other test would still pass. So
// the two functions the refusal rests on are pinned too: requireTestBinary is exactly
// mustBeTestBinary(testing.Testing(), <its parameter>), and mustBeTestBinary is exactly
// `if !isTest { panic(...) }` (the panic itself is tried by foundation_test.go), with no
// other way through.
func TestEveryForTestHookRefusesToRunInAReleaseBinary(t *testing.T) {
	var found []string
	for _, dir := range []string{".", "accounttest"} {
		sources := parseSources(t, dir)
		if dir == "." {
			for _, problem := range refusalProblems(sources) {
				t.Error(problem)
			}
		}
		for _, src := range sources {
			for _, d := range src.file.Decls {
				fd, ok := d.(*ast.FuncDecl)
				if !ok || fd.Body == nil || !fd.Name.IsExported() || !strings.HasSuffix(fd.Name.Name, "ForTest") {
					continue
				}
				found = append(found, fd.Name.Name)
				if problem := hookProblem(fd, dir != "."); problem != "" {
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

// refusalProblems checks the two functions that every hook's refusal rests on.
func refusalProblems(sources []source) []string {
	rtb, mtb := funcsNamed(sources, "requireTestBinary"), funcsNamed(sources, "mustBeTestBinary")
	if len(rtb) != 1 || len(mtb) != 1 {
		return []string{fmt.Sprintf("the package declares %d requireTestBinary and %d mustBeTestBinary, want one of each (renamed or moved? update this test)", len(rtb), len(mtb))}
	}
	var problems []string
	params := paramNames(rtb[0])
	body := rtb[0].Body.List
	if len(params) != 1 || len(body) != 1 || exprOf(body[0]) != fmt.Sprintf("mustBeTestBinary(testing.Testing(), %s)", params[0]) {
		problems = append(problems, "requireTestBinary must be exactly one statement, mustBeTestBinary(testing.Testing(), <its parameter>): "+
			"a release binary is refused only because testing.Testing() is false in it, and anything else (true, another argument, a statement before it) lets every hook run there; found "+describeStmts(body))
	}
	params = paramNames(mtb[0])
	body = mtb[0].Body.List
	ok := len(params) == 2 && len(body) == 1
	if ok {
		ifs, isIf := body[0].(*ast.IfStmt)
		ok = isIf && ifs.Init == nil && ifs.Else == nil && types.ExprString(ifs.Cond) == "!"+params[0] && len(ifs.Body.List) == 1 && isPanic(ifs.Body.List[0])
	}
	if !ok {
		problems = append(problems, "mustBeTestBinary must be exactly `if !isTest { panic(...) }`: any other statement, an else or an early return is a second way through; found "+describeStmts(body))
	}
	return problems
}

// funcsNamed is every function (not method) of the sources declared under name.
func funcsNamed(sources []source, name string) []*ast.FuncDecl {
	var out []*ast.FuncDecl
	for _, src := range sources {
		for _, d := range src.file.Decls {
			if fd, ok := d.(*ast.FuncDecl); ok && fd.Recv == nil && fd.Name.Name == name && fd.Body != nil {
				out = append(out, fd)
			}
		}
	}
	return out
}

// paramNames is the name of every parameter of fd, in order.
func paramNames(fd *ast.FuncDecl) []string {
	var names []string
	for _, field := range fd.Type.Params.List {
		for _, n := range field.Names {
			names = append(names, n.Name)
		}
	}
	return names
}

// isPanic reports whether st is a call to the built-in panic.
func isPanic(st ast.Stmt) bool {
	es, ok := st.(*ast.ExprStmt)
	if !ok {
		return false
	}
	call, ok := es.X.(*ast.CallExpr)
	if !ok {
		return false
	}
	id, ok := call.Fun.(*ast.Ident)
	return ok && id.Name == "panic"
}

// describeStmts is a short account of statements, for a failure message.
func describeStmts(list []ast.Stmt) string {
	var parts []string
	for _, st := range list {
		switch st := st.(type) {
		case *ast.ExprStmt:
			parts = append(parts, types.ExprString(st.X))
		case *ast.IfStmt:
			d := "if " + types.ExprString(st.Cond) + " {...}"
			if st.Else != nil {
				d += " else {...}"
			}
			parts = append(parts, d)
		case *ast.AssignStmt:
			if len(st.Lhs) == 1 && len(st.Rhs) == 1 {
				parts = append(parts, types.ExprString(st.Lhs[0])+" "+st.Tok.String()+" "+types.ExprString(st.Rhs[0]))
			} else {
				parts = append(parts, "an assignment")
			}
		case *ast.ReturnStmt:
			parts = append(parts, "a return")
		default:
			parts = append(parts, "a "+strings.TrimSuffix(strings.TrimPrefix(fmt.Sprintf("%T", st), "*ast."), "Stmt")+" statement")
		}
	}
	return "[" + strings.Join(parts, "; ") + "]"
}

// hookProblem says what is wrong with the start of a hook, or "" when its first
// statement, after t.Helper(), is the call that makes it refuse to run outside a test binary.
func hookProblem(fd *ast.FuncDecl, viaAccount bool) string {
	want := fmt.Sprintf("must start, after t.Helper(), with requireTestBinary(%q), and nothing may run before it", fd.Name.Name)
	if viaAccount {
		want = "must start, after t.Helper(), with a call to an account.*ForTest hook, which refuses by itself, and nothing may run before it"
	}
	param := ""
	if names := paramNames(fd); len(names) > 0 {
		param = names[0]
	}
	for _, st := range fd.Body.List {
		if refuses(st, fd.Name.Name, viaAccount) {
			return ""
		}
		if param != "" && exprOf(st) == param+".Helper()" {
			continue
		}
		return want + "; it starts with " + strings.Trim(describeStmts([]ast.Stmt{st}), "[]")
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
