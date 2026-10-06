package account_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"go/types"
	"testing"
)

// Two lines of process.go cannot be tried from inside a test binary, because
// testing.Testing() is true in every one of them (a re-executed child is a test
// binary too), and a mutation of either would go unseen:
//
//   - Require's one way out for a process with no guard is
//     `testing.Testing() && !isStrict`. Without the first operand a release
//     binary, where strict is never set, would let every ungated call through:
//     the whole gate would fail open.
//   - InstallForTest must refuse to run outside a test binary before it touches
//     any global.
//
// So the source is read, and the two lines are pinned as they are.
func TestTheTestBinaryExceptionsOfProcessGoAreWhereTheyMustBe(t *testing.T) {
	file, err := parser.ParseFile(token.NewFileSet(), "process.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	funcs := map[string]*ast.FuncDecl{}
	for _, d := range file.Decls {
		if fd, ok := d.(*ast.FuncDecl); ok {
			funcs[fd.Name.Name] = fd
		}
	}

	require := funcs["Require"]
	if require == nil {
		t.Fatal("process.go has no Require")
	}
	exceptions, testingCalls := 0, 0
	ast.Inspect(require.Body, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.IfStmt:
			if types.ExprString(n.Cond) == "testing.Testing() && !isStrict" {
				exceptions++
				if !returnsNil(n.Body) {
					t.Error("Require's test-binary exception must do one thing: return nil")
				}
			}
		case *ast.CallExpr:
			if types.ExprString(n.Fun) == "testing.Testing" {
				testingCalls++
			}
		}
		return true
	})
	if exceptions != 1 || testingCalls != 1 {
		t.Errorf("Require has %d exceptions for a test binary and %d calls to testing.Testing, want exactly one of each: `if testing.Testing() && !isStrict { return nil }`", exceptions, testingCalls)
	}

	install := funcs["InstallForTest"]
	if install == nil {
		t.Fatal("process.go has no InstallForTest")
	}
	refuses := false
	for _, st := range install.Body.List {
		es, ok := st.(*ast.ExprStmt)
		if !ok {
			continue
		}
		switch types.ExprString(es.X) {
		case `requireTestBinary("InstallForTest")`:
			refuses = true
		case "globalsMu.Lock()":
			if !refuses {
				t.Error("InstallForTest takes the lock before it has checked that it runs in a test binary")
			}
		}
		if refuses {
			break
		}
	}
	if !refuses {
		t.Error(`InstallForTest does not call requireTestBinary("InstallForTest")`)
	}
}

// returnsNil reports whether block is exactly `return nil`.
func returnsNil(block *ast.BlockStmt) bool {
	if len(block.List) != 1 {
		return false
	}
	ret, ok := block.List[0].(*ast.ReturnStmt)
	return ok && len(ret.Results) == 1 && types.ExprString(ret.Results[0]) == "nil"
}
