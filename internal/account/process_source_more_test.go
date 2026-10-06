package account_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"go/types"
	"testing"
)

// A tripwire for mutations that no test can see from inside a test binary, because
// testing.Testing() is true in every one of them (a re-executed child is a test
// binary too). Require hands requireNoGuard testing.Testing() and the strict flag it
// read under globalsMu. Hand it true instead, or leave the first argument out, and a
// release binary (testing.Testing() false, strict never set) lets every call through
// when no guard is installed: the whole gate fails open. So does any other way out of
// Require that returns nil for a process that is not strict, for example an inserted
// `if !isStrict { return nil }`: in a test binary a process that is not strict is let
// through anyway, and one that is strict falls through, so nothing fails. The tests of
// requireNoGuard pin its logic for a release binary; only this pins what Require
// passes it and that Require has no other exit. (The *ForTest hooks that must refuse in
// a release binary, InstallForTest among them, are pinned by
// TestEveryForTestHookRefusesToRunInAReleaseBinary.)
//
// Require is exactly: the installed guard's branch, the strict flag read, and the
// final `return requireNoGuard(testing.Testing(), <the strict flag>, <the time>)`.
// The flag is read under globalsMu inline (RLock, :=, RUnlock) or through the
// isStrict() accessor, which does the same, and the argument is that flag and no
// other expression. The proof on a real binary (no guard in a release build is locked
// once the gate is enforced) is B5c's smoke. The source is read as syntax, so
// formatting and comments change nothing; the shape of the statements does.
func TestRequireHandsRequireNoGuardTheTestBinaryFlags(t *testing.T) {
	file, err := parser.ParseFile(token.NewFileSet(), "process.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	require := bodyOf(t, file, "Require")

	returns := 0
	ast.Inspect(require, func(n ast.Node) bool {
		if _, ok := n.(*ast.ReturnStmt); ok {
			returns++
		}
		return true
	})
	if returns != 2 {
		t.Errorf("Require has %d return statements, want exactly 2: `return g.Require(ctx)` for the installed guard and the final `return requireNoGuard(...)`; "+
			"any other exit that can return nil (an `if !isStrict { return nil }`, a nil context let through) fails the gate open in a release binary", returns)
	}

	stmts := require.List
	if len(stmts) < 2 || !isGuardBranch(stmts[0]) {
		t.Fatalf("Require must start with `if g := Current(); g != nil { return g.Require(ctx) }`; found %s", describeStmts(stmts))
	}
	var call *ast.CallExpr
	if last, ok := stmts[len(stmts)-1].(*ast.ReturnStmt); ok && len(last.Results) == 1 {
		call, _ = last.Results[0].(*ast.CallExpr)
	}
	if call == nil || types.ExprString(call.Fun) != "requireNoGuard" || len(call.Args) != 3 {
		t.Fatal("Require must end with `return requireNoGuard(testing.Testing(), <the strict flag>, <the time>)`")
	}
	if got := types.ExprString(call.Args[0]); got != "testing.Testing()" {
		t.Errorf("Require hands requireNoGuard %s as isTest, want testing.Testing()", got)
	}
	// Between the two, Require reads the strict flag, in one of three ways, and does nothing else.
	middle, flag := stmts[1:len(stmts)-1], types.ExprString(call.Args[1])
	_, plain := call.Args[1].(*ast.Ident)
	switch {
	case len(middle) == 0 && flag == "isStrict()":
	case len(middle) == 1 && plain && defineOf(middle[0]) == flag+" := isStrict()":
	case len(middle) == 3 && plain && exprOf(middle[0]) == "globalsMu.RLock()" && defineOf(middle[1]) == flag+" := strict" && exprOf(middle[2]) == "globalsMu.RUnlock()":
	default:
		t.Errorf("between the guard branch and the final return Require may only read the strict flag, under globalsMu (RLock, <flag> := strict, RUnlock) or through the isStrict() accessor "+
			"(inline, or <flag> := isStrict()), and must hand requireNoGuard that flag and no other expression; found %s and the argument %s", describeStmts(middle), flag)
	}
	asked := 0
	ast.Inspect(require, func(n ast.Node) bool {
		if c, ok := n.(*ast.CallExpr); ok && types.ExprString(c.Fun) == "testing.Testing" {
			asked++
		}
		return true
	})
	if asked != 1 {
		t.Errorf("Require calls testing.Testing() %d times, want once, as the first argument of requireNoGuard", asked)
	}
}

// isGuardBranch reports whether st is exactly `if g := Current(); g != nil { return g.Require(ctx) }`.
func isGuardBranch(st ast.Stmt) bool {
	ifs, ok := st.(*ast.IfStmt)
	if !ok || ifs.Else != nil || ifs.Init == nil || types.ExprString(ifs.Cond) != "g != nil" || len(ifs.Body.List) != 1 {
		return false
	}
	init, ok := ifs.Init.(*ast.AssignStmt)
	if !ok || defineOf(init) != "g := Current()" {
		return false
	}
	ret, ok := ifs.Body.List[0].(*ast.ReturnStmt)
	return ok && len(ret.Results) == 1 && types.ExprString(ret.Results[0]) == "g.Require(ctx)"
}

// defineOf is the source of the short variable declaration that st is (`x := y`), or "".
func defineOf(st ast.Stmt) string {
	as, ok := st.(*ast.AssignStmt)
	if !ok || as.Tok != token.DEFINE || len(as.Lhs) != 1 || len(as.Rhs) != 1 {
		return ""
	}
	return types.ExprString(as.Lhs[0]) + " := " + types.ExprString(as.Rhs[0])
}

// bodyOf is the body of the function that file declares under name.
func bodyOf(t *testing.T, file *ast.File, name string) *ast.BlockStmt {
	t.Helper()
	for _, d := range file.Decls {
		if fd, ok := d.(*ast.FuncDecl); ok && fd.Recv == nil && fd.Name.Name == name && fd.Body != nil {
			return fd.Body
		}
	}
	t.Fatalf("process.go declares no function %s", name)
	return nil
}

// exprOf is the source of the expression that st is made of, or "" when st is not an expression statement.
func exprOf(st ast.Stmt) string {
	if es, ok := st.(*ast.ExprStmt); ok {
		return types.ExprString(es.X)
	}
	return ""
}
