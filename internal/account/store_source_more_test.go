package account_test

import (
	"fmt"
	"go/ast"
	"go/token"
	"go/types"
	"slices"
	"testing"
)

// A tripwire for what only Windows, or a race between two system calls, can see.
// Every job of the CI but account-os runs on Linux, and rename_windows.go and its
// tests are compiled and run on Windows only (account-os runs them, informational
// until it has been green once), so a mutation of the Windows rename (the
// write-through flag dropped, the two paths swapped, os.Rename back in place of
// replaceFile) passes every Linux test. So does a type check made by path
// instead of on the file that was opened: it passes while nobody swaps the file
// between the two calls, and on Linux a FIFO swapped in at that moment makes the
// read wait in the poller for a writer that never comes, which is the hang that
// readStoreFile exists to prevent.
//
// The sources are read as syntax, so formatting and comments change nothing; the
// shape of the calls does. Each check needs its target to exist exactly once, so a
// rename or a move fails the test instead of emptying it.
func TestTheStoreWritesAndReadsThroughTheCallsItsPromisesRestOn(t *testing.T) {
	for _, problem := range storeSourceProblems(parseSources(t, ".")) {
		t.Error(problem)
	}
}

func storeSourceProblems(sources []source) []string {
	var problems []string
	problems = append(problems, windowsRenameProblems(sources)...)
	problems = append(problems, writeThroughProblems(sources)...)
	problems = append(problems, readStoreFileProblems(sources)...)
	return problems
}

// onlyFunc is the one function name declared in the file called file.
func onlyFunc(sources []source, file, name string) (*ast.FuncDecl, string) {
	var found []*ast.FuncDecl
	for _, src := range sources {
		if src.name == file {
			found = funcsNamed([]source{src}, name)
		}
	}
	if len(found) != 1 {
		return nil, fmt.Sprintf("%s must declare exactly one %s (found %d): it was renamed or moved, so update this test", file, name, len(found))
	}
	return found[0], ""
}

// callsIn is the source of the function of every call in n, in order.
func callsIn(n ast.Node) []string {
	var out []string
	ast.Inspect(n, func(n ast.Node) bool {
		if c, ok := n.(*ast.CallExpr); ok {
			out = append(out, types.ExprString(c.Fun))
		}
		return true
	})
	return out
}

// callArgs is the arguments of every call in n to the function called fn.
func callArgs(n ast.Node, fn string) [][]ast.Expr {
	var out [][]ast.Expr
	ast.Inspect(n, func(n ast.Node) bool {
		if c, ok := n.(*ast.CallExpr); ok && types.ExprString(c.Fun) == fn {
			out = append(out, c.Args)
		}
		return true
	})
	return out
}

// varValue is the source of the value that the package variable name is
// initialised to in the file called file, or "" when there is none.
func varValue(sources []source, file, name string) string {
	for _, src := range sources {
		if src.name != file {
			continue
		}
		for _, d := range src.file.Decls {
			gd, ok := d.(*ast.GenDecl)
			if !ok || gd.Tok != token.VAR {
				continue
			}
			for _, spec := range gd.Specs {
				vs := spec.(*ast.ValueSpec)
				if len(vs.Names) == 1 && vs.Names[0].Name == name && len(vs.Values) == 1 {
					return types.ExprString(vs.Values[0])
				}
			}
		}
	}
	return ""
}

// The Windows rename is windows.MoveFileEx from the first path to the second with
// REPLACE_EXISTING|WRITE_THROUGH. Windows has no directory flush (syncDir does
// nothing there), so the write-through flag is all that keeps a power cut just
// after a refresh from bringing back the rotated-away refresh token, or a
// session.json without the marker that had to be on the disk before the grant
// went out.
func windowsRenameProblems(sources []source) []string {
	fd, problem := onlyFunc(sources, "rename_windows.go", "replaceFile")
	if fd == nil {
		return []string{problem}
	}
	var problems []string
	if v := varValue(sources, "rename_windows.go", "moveFileEx"); v != "windows.MoveFileEx" {
		problems = append(problems, fmt.Sprintf("rename_windows.go: moveFileEx must be initialised to windows.MoveFileEx, found %q", v))
	}
	params := paramNames(fd)
	if len(params) != 2 {
		return append(problems, fmt.Sprintf("replaceFile must take exactly the two paths, found %v", params))
	}
	from, to := params[0], params[1]

	// Which local holds the UTF-16 form of which path.
	utf16Of := map[string]string{}
	ast.Inspect(fd.Body, func(n ast.Node) bool {
		as, ok := n.(*ast.AssignStmt)
		if !ok || len(as.Rhs) != 1 || len(as.Lhs) < 1 {
			return true
		}
		if c, ok := as.Rhs[0].(*ast.CallExpr); ok && types.ExprString(c.Fun) == "windows.UTF16PtrFromString" && len(c.Args) == 1 {
			utf16Of[types.ExprString(as.Lhs[0])] = types.ExprString(c.Args[0])
		}
		return true
	})
	calls := callArgs(fd.Body, "moveFileEx")
	if len(calls) != 1 {
		return append(problems, fmt.Sprintf("replaceFile must call moveFileEx exactly once, found %d calls", len(calls)))
	}
	args := calls[0]
	if len(args) != 3 || utf16Of[types.ExprString(args[0])] != from || utf16Of[types.ExprString(args[1])] != to {
		problems = append(problems, fmt.Sprintf("replaceFile must call moveFileEx with the UTF-16 forms of %s and %s, in that order: a swapped pair renames the target over the source", from, to))
	}
	if len(args) == 3 {
		flags := map[string]bool{}
		ast.Inspect(args[2], func(n ast.Node) bool {
			if sel, ok := n.(*ast.SelectorExpr); ok {
				flags[types.ExprString(sel)] = true
			}
			return true
		})
		for _, want := range []string{"windows.MOVEFILE_REPLACE_EXISTING", "windows.MOVEFILE_WRITE_THROUGH"} {
			if !flags[want] {
				problems = append(problems, fmt.Sprintf("replaceFile does not pass %s to moveFileEx: without it a Windows rename is not written through, or does not replace an existing file", want))
			}
		}
	}
	for _, c := range callsIn(fd.Body) {
		if slices.Contains([]string{"os.Rename", "windows.MoveFile", "windows.MoveFileEx"}, c) {
			problems = append(problems, fmt.Sprintf("replaceFile calls %s: the one rename of a write is moveFileEx with the flags above", c))
		}
	}
	return problems
}

// Every write of session.json and refresh.enc goes writeFileAtomic ->
// renameReplacingFn -> renameReplacing -> replaceFile, so that the rename that
// makes a write durable on Windows is the one that is written through. On Unix
// replaceFile is os.Rename, so a write that skipped the chain would pass every
// test there.
func writeThroughProblems(sources []source) []string {
	var problems []string
	chain := []struct {
		file, fn, callee string
		forbidden        []string
	}{
		{"store.go", "writeFileAtomic", "renameReplacingFn", []string{"os.Rename", "os.WriteFile", "renameReplacing", "replaceFile"}},
		{"store.go", "renameReplacing", "replaceFile", []string{"os.Rename", "os.WriteFile", "renameReplacingFn"}},
	}
	for _, link := range chain {
		fd, problem := onlyFunc(sources, link.file, link.fn)
		if fd == nil {
			problems = append(problems, problem)
			continue
		}
		calls := callsIn(fd.Body)
		if n := countOf(calls, link.callee); n != 1 {
			problems = append(problems, fmt.Sprintf("%s must call %s exactly once, found %d calls", link.fn, link.callee, n))
		}
		for _, c := range calls {
			if slices.Contains(link.forbidden, c) {
				problems = append(problems, fmt.Sprintf("%s calls %s: a write must reach the disk through %s, whose Windows form is written through", link.fn, c, link.callee))
			}
		}
	}
	if v := varValue(sources, "store.go", "renameReplacingFn"); v != "renameReplacing" {
		problems = append(problems, fmt.Sprintf("store.go: renameReplacingFn must be initialised to renameReplacing, found %q", v))
	}
	return problems
}

func countOf(list []string, want string) int {
	n := 0
	for _, s := range list {
		if s == want {
			n++
		}
	}
	return n
}

// readStoreFile judges the file it opened: it asks the *os.File that openForRead
// returned what it is, once, and never looks at the path again. A stat by path
// would judge another file than the one that is read if the two are swapped
// between the calls.
func readStoreFileProblems(sources []source) []string {
	fd, problem := onlyFunc(sources, "readfile.go", "readStoreFile")
	if fd == nil {
		return []string{problem}
	}
	opened := ""
	ast.Inspect(fd.Body, func(n ast.Node) bool {
		as, ok := n.(*ast.AssignStmt)
		if !ok || len(as.Rhs) != 1 || len(as.Lhs) < 1 {
			return true
		}
		if c, ok := as.Rhs[0].(*ast.CallExpr); ok && types.ExprString(c.Fun) == "openForRead" {
			opened = types.ExprString(as.Lhs[0])
		}
		return true
	})
	if opened == "" {
		return []string{"readStoreFile must open the file with `<f>, err := openForRead(path)`: the open that does not wait for a FIFO's writer"}
	}
	var problems []string
	if n := countOf(callsIn(fd.Body), opened+".Stat"); n != 1 {
		problems = append(problems, fmt.Sprintf("readStoreFile must call %s.Stat exactly once (the type check is on the open file), found %d calls", opened, n))
	}
	for _, c := range callsIn(fd.Body) {
		if slices.Contains([]string{"os.Stat", "os.Lstat", "os.ReadFile", "os.Open", "os.OpenFile"}, c) {
			problems = append(problems, fmt.Sprintf("readStoreFile calls %s: it must open through openForRead and judge the open file, never the path", c))
		}
	}
	return problems
}
