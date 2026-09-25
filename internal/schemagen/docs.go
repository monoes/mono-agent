package schemagen

import (
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// ParseDocs reads the Go sources in dirs (test files excluded) and returns
// struct documentation keyed "TypeName" (the type's doc comment) and
// "TypeName.GoField" (the field's doc or trailing line comment). Whitespace
// is collapsed so each description is one line.
func ParseDocs(dirs ...string) (map[string]string, error) {
	docs := map[string]string{}
	for _, dir := range dirs {
		entries, err := os.ReadDir(dir)
		if err != nil {
			return nil, err
		}
		var files []string
		for _, e := range entries {
			n := e.Name()
			if !e.IsDir() && strings.HasSuffix(n, ".go") && !strings.HasSuffix(n, "_test.go") {
				files = append(files, filepath.Join(dir, n))
			}
		}
		sort.Strings(files)
		fset := token.NewFileSet()
		for _, path := range files {
			f, err := parser.ParseFile(fset, path, nil, parser.ParseComments)
			if err != nil {
				return nil, err
			}
			collectDocs(f, docs)
		}
	}
	return docs, nil
}

func collectDocs(f *ast.File, docs map[string]string) {
	for _, decl := range f.Decls {
		gd, ok := decl.(*ast.GenDecl)
		if !ok || gd.Tok != token.TYPE {
			continue
		}
		for _, spec := range gd.Specs {
			ts := spec.(*ast.TypeSpec)
			st, ok := ts.Type.(*ast.StructType)
			if !ok {
				continue
			}
			doc := ts.Doc
			if doc == nil && len(gd.Specs) == 1 {
				doc = gd.Doc
			}
			if d := clean(doc); d != "" {
				docs[ts.Name.Name] = d
			}
			for _, field := range st.Fields.List {
				d := clean(field.Doc)
				if d == "" {
					d = clean(field.Comment)
				}
				if d == "" {
					continue
				}
				for _, n := range field.Names {
					docs[ts.Name.Name+"."+n.Name] = d
				}
			}
		}
	}
}

func clean(g *ast.CommentGroup) string {
	if g == nil {
		return ""
	}
	text := g.Text()
	// Drop a "--- section ---" marker line some structs use as a divider.
	var keep []string
	for _, line := range strings.Split(text, "\n") {
		if t := strings.TrimSpace(line); strings.HasPrefix(t, "---") && strings.HasSuffix(t, "---") {
			continue
		}
		keep = append(keep, line)
	}
	return strings.Join(strings.Fields(strings.Join(keep, "\n")), " ")
}

// ModuleRoot walks up from dir to the directory holding go.mod.
func ModuleRoot(dir string) (string, error) {
	dir, err := filepath.Abs(dir)
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", errors.New("schemagen: go.mod not found")
		}
		dir = parent
	}
}

// ParseDocTable returns the leading name(s) of each indented line of
// typeName's doc comment in dir (a comma list names several): the table a type such as action.TransformOp keeps of
// its allowed values (e.g. "\tmap  Map: …"). Order is preserved.
func ParseDocTable(dir, typeName string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	fset := token.NewFileSet()
	for _, e := range entries {
		n := e.Name()
		if e.IsDir() || !strings.HasSuffix(n, ".go") || strings.HasSuffix(n, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, filepath.Join(dir, n), nil, parser.ParseComments)
		if err != nil {
			return nil, err
		}
		for _, decl := range f.Decls {
			gd, ok := decl.(*ast.GenDecl)
			if !ok || gd.Tok != token.TYPE {
				continue
			}
			for _, spec := range gd.Specs {
				ts := spec.(*ast.TypeSpec)
				if ts.Name.Name != typeName {
					continue
				}
				doc := ts.Doc
				if doc == nil {
					doc = gd.Doc
				}
				if doc == nil {
					return nil, nil
				}
				var out []string
				for _, line := range strings.Split(doc.Text(), "\n") {
					if !strings.HasPrefix(line, "\t") {
						continue
					}
					// "name  desc", or "a, b, c  desc" for ops sharing a row.
					for _, w := range strings.Fields(line) {
						out = append(out, strings.TrimSuffix(w, ","))
						if !strings.HasSuffix(w, ",") {
							break
						}
					}
				}
				return out, nil
			}
		}
	}
	return nil, nil
}
