package account_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"go/types"
	"testing"
)

// A tripwire for a mutation that no test can see from inside a test binary,
// because testing.Testing() is true in every one of them (a re-executed child is a
// test binary too): Require hands requireNoGuard testing.Testing() and the strict
// flag it read under globalsMu. Hand it true instead, or leave the first argument
// out, and a release binary (testing.Testing() false, strict never set) lets every
// call through when no guard is installed: the whole gate fails open. The tests of
// requireNoGuard pin its logic for a release binary; only this pins what Require
// passes it. (The *ForTest hooks that must refuse in a release binary, InstallForTest
// among them, are pinned by TestEveryForTestHookRefusesToRunInAReleaseBinary.)
//
// The proof on a real binary (no guard in a release build is locked once the gate
// is enforced) is B5c's smoke. The source is read as syntax, so formatting and
// comments change nothing; the shape of the calls does.
func TestRequireHandsRequireNoGuardTheTestBinaryFlags(t *testing.T) {
	file, err := parser.ParseFile(token.NewFileSet(), "process.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}

	// Require reads the strict flag between globalsMu.RLock() and RUnlock(), into a
	// variable, and ends with: return requireNoGuard(testing.Testing(), <that variable>, <the time>).
	require := bodyOf(t, file, "Require")
	flag := ""
	for i := 0; i+2 < len(require.List); i++ {
		if exprOf(require.List[i]) != "globalsMu.RLock()" || exprOf(require.List[i+2]) != "globalsMu.RUnlock()" {
			continue
		}
		if as, ok := require.List[i+1].(*ast.AssignStmt); ok && len(as.Lhs) == 1 && len(as.Rhs) == 1 && types.ExprString(as.Rhs[0]) == "strict" {
			flag = types.ExprString(as.Lhs[0])
		}
	}
	if flag == "" {
		t.Error("Require does not read the strict flag into a variable between globalsMu.RLock() and globalsMu.RUnlock()")
	}
	var call *ast.CallExpr
	if n := len(require.List); n > 0 {
		if last, ok := require.List[n-1].(*ast.ReturnStmt); ok && len(last.Results) == 1 {
			call, _ = last.Results[0].(*ast.CallExpr)
		}
	}
	if call == nil || types.ExprString(call.Fun) != "requireNoGuard" || len(call.Args) != 3 {
		t.Fatal("Require must end with `return requireNoGuard(testing.Testing(), <the strict flag it read>, <the time>)`")
	}
	if got := types.ExprString(call.Args[0]); got != "testing.Testing()" {
		t.Errorf("Require hands requireNoGuard %s as isTest, want testing.Testing()", got)
	}
	if got := types.ExprString(call.Args[1]); flag != "" && got != flag {
		t.Errorf("Require hands requireNoGuard %s as isStrict, want %s, the flag it read under globalsMu", got, flag)
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
