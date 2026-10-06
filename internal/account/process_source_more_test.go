package account_test

import (
	"fmt"
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
// final `return requireNoGuard(testing.Testing(), <the strict flag>, time.Now())`.
// The flag is read under globalsMu inline (RLock, :=, RUnlock) or through the
// isStrict() accessor, which does the same, and the argument is that flag and no
// other expression. The proof on a real binary (no guard in a release build is locked
// once the gate is enforced) is B5c's smoke. The source is read as syntax, so
// formatting and comments change nothing; the shape of the statements does.
func TestRequireHandsRequireNoGuardTheTestBinaryFlags(t *testing.T) {
	decls := funcsNamed(parseProcess(t), "Require")
	if len(decls) != 1 || len(paramNames(decls[0])) != 1 {
		t.Fatal("process.go must declare exactly one Require, taking exactly its context")
	}
	require, ctx := decls[0].Body, paramNames(decls[0])[0]

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
	if len(stmts) < 2 || !isGuardBranch(stmts[0], ctx) {
		t.Fatalf("Require must start with `if <g> := Current(); <g> != nil { return <g>.Require(%s) }`; found %s", ctx, describeStmts(stmts))
	}
	var call *ast.CallExpr
	if last, ok := stmts[len(stmts)-1].(*ast.ReturnStmt); ok && len(last.Results) == 1 {
		call, _ = last.Results[0].(*ast.CallExpr)
	}
	if call == nil || types.ExprString(call.Fun) != "requireNoGuard" || len(call.Args) != 3 {
		t.Fatal("Require must end with `return requireNoGuard(testing.Testing(), <the strict flag>, time.Now())`")
	}
	if got := types.ExprString(call.Args[0]); got != "testing.Testing()" {
		t.Errorf("Require hands requireNoGuard %s as isTest, want testing.Testing()", got)
	}
	// The moment is the real one, as a bare call: an offset behind a condition that only a
	// test binary meets (flag.Parsed(), say) would leave every test passing and make a
	// release binary judge another moment than now.
	if got := types.ExprString(call.Args[2]); got != "time.Now()" {
		t.Errorf("Require hands requireNoGuard %s as the time, want exactly time.Now()", got)
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

// parseProcess is process.go as syntax, as the one source it is (the pins below read it).
func parseProcess(t *testing.T) []source {
	t.Helper()
	file, err := parser.ParseFile(token.NewFileSet(), "process.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	return []source{{"process.go", file}}
}

// The other half of the no-guard path. Whatever Require hands it, requireNoGuard does
// nothing but what the matrix test pins, and noGuardStatus nothing but judge the process
// as not logged in at the moment it is given. Anything else in either passes every test
// and changes what a release binary does when no guard is installed: `if !flag.Parsed()
// { return nil }`, an offset or a constant clock behind a condition that only a test
// binary meets (flag.Parsed() is true in every one, so the condition is never the one a
// release binary meets), or another way out.
//
// requireNoGuard is exactly: `if <isTest> && !<strict> { return nil }`, then
// `if st := noGuardStatus(<now>); !st.Allowed() { return &LoginRequiredError{Status: st} }`,
// then `return nil`; noGuardStatus is exactly `return judge(nil, nil, nil, <now>)`. The
// parameter names, and the name of st, are read from the source; formatting and comments
// change nothing.
func TestRequireNoGuardAndNoGuardStatusAreNothingButTheVerdict(t *testing.T) {
	sources := parseProcess(t)
	for _, problem := range []string{requireNoGuardProblem(sources), noGuardStatusProblem(sources)} {
		if problem != "" {
			t.Error(problem)
		}
	}
}

func requireNoGuardProblem(sources []source) string {
	decls := funcsNamed(sources, "requireNoGuard")
	if len(decls) != 1 || len(paramNames(decls[0])) != 3 {
		return "process.go must declare exactly one requireNoGuard(isTest, strict bool, now time.Time) error (renamed or moved? update this test)"
	}
	names := paramNames(decls[0])
	isTest, strict, now := names[0], names[1], names[2]
	body := decls[0].Body.List
	ok := len(body) == 3 && isEarlyNil(body[0], isTest+" && !"+strict) && isRefusal(body[1], now) && isReturnNil(body[2])
	if !ok {
		return fmt.Sprintf("requireNoGuard must be exactly `if %s && !%s { return nil }`, then `if st := noGuardStatus(%s); !st.Allowed() { return &LoginRequiredError{Status: st} }`, then `return nil`: "+
			"anything else, such as `if !flag.Parsed() { return nil }`, an offset or a constant clock, or another way out, can change what a release binary does and no test binary can see it; found %s",
			isTest, strict, now, describeStmts(body))
	}
	return ""
}

func noGuardStatusProblem(sources []source) string {
	decls := funcsNamed(sources, "noGuardStatus")
	if len(decls) != 1 || len(paramNames(decls[0])) != 1 {
		return "process.go must declare exactly one noGuardStatus(now time.Time) Status (renamed or moved? update this test)"
	}
	now := paramNames(decls[0])[0]
	body := decls[0].Body.List
	ok := len(body) == 1
	if ok {
		ret, isRet := body[0].(*ast.ReturnStmt)
		ok = isRet && len(ret.Results) == 1 && types.ExprString(ret.Results[0]) == "judge(nil, nil, nil, "+now+")"
	}
	if !ok {
		return fmt.Sprintf("noGuardStatus must be exactly `return judge(nil, nil, nil, %s)`: anything else (an offset or a constant clock, a status returned early) can change what a release binary reports and refuses and no test binary can see it; found %s",
			now, describeStmts(body))
	}
	return ""
}

// isEarlyNil reports whether st is exactly `if <cond> { return nil }`.
func isEarlyNil(st ast.Stmt, cond string) bool {
	ifs, ok := st.(*ast.IfStmt)
	return ok && ifs.Init == nil && ifs.Else == nil && types.ExprString(ifs.Cond) == cond && len(ifs.Body.List) == 1 && isReturnNil(ifs.Body.List[0])
}

// isRefusal reports whether st is exactly `if <v> := noGuardStatus(<now>); !<v>.Allowed() { return &LoginRequiredError{Status: <v>} }`.
func isRefusal(st ast.Stmt, now string) bool {
	ifs, ok := st.(*ast.IfStmt)
	if !ok || ifs.Else != nil || ifs.Init == nil || len(ifs.Body.List) != 1 {
		return false
	}
	init, ok := ifs.Init.(*ast.AssignStmt)
	if !ok || init.Tok != token.DEFINE || len(init.Lhs) != 1 || len(init.Rhs) != 1 || types.ExprString(init.Rhs[0]) != "noGuardStatus("+now+")" {
		return false
	}
	v := types.ExprString(init.Lhs[0])
	ret, ok := ifs.Body.List[0].(*ast.ReturnStmt)
	return types.ExprString(ifs.Cond) == "!"+v+".Allowed()" && ok && len(ret.Results) == 1 && isRefusalOf(ret.Results[0], v)
}

// isRefusalOf reports whether e is exactly `&LoginRequiredError{Status: <v>}` (types.ExprString elides
// the elements of a composite literal, so the literal is read as syntax).
func isRefusalOf(e ast.Expr, v string) bool {
	u, ok := e.(*ast.UnaryExpr)
	if !ok || u.Op != token.AND {
		return false
	}
	lit, ok := u.X.(*ast.CompositeLit)
	if !ok || types.ExprString(lit.Type) != "LoginRequiredError" || len(lit.Elts) != 1 {
		return false
	}
	kv, ok := lit.Elts[0].(*ast.KeyValueExpr)
	return ok && types.ExprString(kv.Key) == "Status" && types.ExprString(kv.Value) == v
}

// isReturnNil reports whether st is exactly `return nil`.
func isReturnNil(st ast.Stmt) bool {
	ret, ok := st.(*ast.ReturnStmt)
	return ok && len(ret.Results) == 1 && types.ExprString(ret.Results[0]) == "nil"
}

// isGuardBranch reports whether st is exactly `if <g> := Current(); <g> != nil { return <g>.Require(<ctx>) }`,
// with whatever names Require gives the guard variable and its context parameter.
func isGuardBranch(st ast.Stmt, ctx string) bool {
	ifs, ok := st.(*ast.IfStmt)
	if !ok || ifs.Else != nil || ifs.Init == nil || len(ifs.Body.List) != 1 {
		return false
	}
	init, ok := ifs.Init.(*ast.AssignStmt)
	if !ok || init.Tok != token.DEFINE || len(init.Lhs) != 1 || len(init.Rhs) != 1 || types.ExprString(init.Rhs[0]) != "Current()" {
		return false
	}
	g := types.ExprString(init.Lhs[0])
	if types.ExprString(ifs.Cond) != g+" != nil" {
		return false
	}
	ret, ok := ifs.Body.List[0].(*ast.ReturnStmt)
	return ok && len(ret.Results) == 1 && types.ExprString(ret.Results[0]) == g+".Require("+ctx+")"
}

// defineOf is the source of the short variable declaration that st is (`x := y`), or "".
func defineOf(st ast.Stmt) string {
	as, ok := st.(*ast.AssignStmt)
	if !ok || as.Tok != token.DEFINE || len(as.Lhs) != 1 || len(as.Rhs) != 1 {
		return ""
	}
	return types.ExprString(as.Lhs[0]) + " := " + types.ExprString(as.Rhs[0])
}

// exprOf is the source of the expression that st is made of, or "" when st is not an expression statement.
func exprOf(st ast.Stmt) string {
	if es, ok := st.(*ast.ExprStmt); ok {
		return types.ExprString(es.X)
	}
	return ""
}
