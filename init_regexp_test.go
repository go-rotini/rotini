package rotini

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strings"
	"testing"
)

// TestNoRegexpCompiledAtInit pins that no package of the module compiles a regular expression
// in a package-level var's initializer, which every process start would pay for, the companion
// CLI's completion included. A regexp a package needs is built on first use, behind
// sync.OnceValue.
func TestNoRegexpCompiledAtInit(t *testing.T) {
	fset := token.NewFileSet()
	err := filepath.WalkDir(".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if path != "." && (d.Name() == "testdata" || d.Name() == "e2e" || d.Name() == "docs" || strings.HasPrefix(d.Name(), ".")) {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		f, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
		if err != nil {
			return err
		}
		for _, decl := range f.Decls {
			g, ok := decl.(*ast.GenDecl)
			if !ok || g.Tok != token.VAR {
				continue
			}
			for _, spec := range g.Specs {
				for _, v := range spec.(*ast.ValueSpec).Values {
					ast.Inspect(v, func(n ast.Node) bool {
						if _, ok := n.(*ast.FuncLit); ok {
							return false // runs when called, not at init
						}
						if call, ok := n.(*ast.CallExpr); ok {
							if sel, ok := call.Fun.(*ast.SelectorExpr); ok {
								if pkg, ok := sel.X.(*ast.Ident); ok && pkg.Name == "regexp" && strings.Contains(sel.Sel.Name, "Compile") {
									t.Errorf("%s: a package-level var compiles a regexp at init; build it behind sync.OnceValue", fset.Position(call.Pos()))
								}
							}
						}
						return true
					})
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
