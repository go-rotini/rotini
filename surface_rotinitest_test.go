package rotini

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// TestRotinitestSurface is TestExportedSurface for the rotinitest package:
// `go test . -run Surface -update-surface` refreshes both snapshots.
func TestRotinitestSurface(t *testing.T) {
	files, err := filepath.Glob(filepath.Join("rotinitest", "*.go"))
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	var lines []string
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		for _, d := range f.Decls {
			switch decl := d.(type) {
			case *ast.FuncDecl:
				switch {
				case !decl.Name.IsExported():
				case decl.Recv == nil:
					lines = append(lines, "func "+decl.Name.Name+signature(fset, decl.Type))
				case ast.IsExported(receiverName(decl.Recv)):
					lines = append(lines, "method "+receiverName(decl.Recv)+"."+decl.Name.Name+signature(fset, decl.Type))
				}
			case *ast.GenDecl:
				lines = append(lines, exportedSpecs(decl)...)
			}
		}
	}
	sort.Strings(lines)
	got := strings.Join(lines, "\n") + "\n"
	golden := filepath.Join("testdata", "surface_rotinitest.txt")
	if *updateSurface {
		if err := os.WriteFile(golden, []byte(got), 0o600); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatalf("read surface snapshot (run with -update-surface to create it): %v", err)
	}
	if got != string(want) {
		t.Errorf("rotinitest's exported surface changed — review the diff, then re-run with -update-surface if intended:\n%s",
			surfaceDiff(strings.Split(string(want), "\n"), strings.Split(got, "\n")))
	}
}
