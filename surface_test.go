package rotini

import (
	"flag"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// updateSurface refreshes the exported-surface snapshot:
// `go test . -run Surface -update-surface`.
var updateSurface = flag.Bool("update-surface", false, "update the exported API surface snapshot")

// TestExportedSurface is the API freeze. Every generated CLI and every handler written
// against rotini depends on these symbols, so the surface is the one thing that must not
// drift by accident: a rename, a signature change, or a newly exported helper fails here
// until the snapshot is updated DELIBERATELY (with -update-surface), which puts the
// change in the diff where a reviewer sees it.
//
// It is a snapshot, not a policy — it says nothing about whether a change is good, only
// that it was intended.
func TestExportedSurface(t *testing.T) {
	got := strings.Join(exportedSurface(t), "\n") + "\n"
	golden := filepath.Join("testdata", "surface.txt")

	if *updateSurface {
		if err := os.WriteFile(golden, []byte(got), 0o600); err != nil {
			t.Fatal(err)
		}
		t.Logf("updated %s", golden)
		return
	}

	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatalf("read surface snapshot (run with -update-surface to create it): %v", err)
	}
	if got != string(want) {
		t.Errorf("exported surface changed — review the diff, then re-run with -update-surface if intended:\n%s",
			surfaceDiff(strings.Split(string(want), "\n"), strings.Split(got, "\n")))
	}
}

// exportedSurface renders one stable line per exported declaration of the package:
// "func Name", "type Name", "method Recv.Name", "const Name", "var Name".
func exportedSurface(t *testing.T) []string {
	t.Helper()
	entries, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	var out []string
	for _, name := range entries {
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
				if !decl.Name.IsExported() {
					continue
				}
				if decl.Recv == nil {
					out = append(out, "func "+decl.Name.Name)
					continue
				}
				if recv := receiverName(decl.Recv); recv != "" && ast.IsExported(recv) {
					out = append(out, "method "+recv+"."+decl.Name.Name)
				}
			case *ast.GenDecl:
				out = append(out, exportedSpecs(decl)...)
			}
		}
	}
	sort.Strings(out)
	return out
}

// exportedSpecs renders the exported type/const/var names of one declaration block.
func exportedSpecs(decl *ast.GenDecl) []string {
	var out []string
	for _, s := range decl.Specs {
		switch spec := s.(type) {
		case *ast.TypeSpec:
			if spec.Name.IsExported() {
				out = append(out, "type "+spec.Name.Name)
			}
		case *ast.ValueSpec:
			kind := "var"
			if decl.Tok == token.CONST {
				kind = "const"
			}
			for _, n := range spec.Names {
				if n.IsExported() {
					out = append(out, kind+" "+n.Name)
				}
			}
		}
	}
	return out
}

// receiverName returns the bare type name of a method receiver, dropping any pointer
// and type-parameter decoration, so `func (p *Program) X` reports "Program".
func receiverName(recv *ast.FieldList) string {
	if len(recv.List) == 0 {
		return ""
	}
	expr := recv.List[0].Type
	if star, ok := expr.(*ast.StarExpr); ok {
		expr = star.X
	}
	if idx, ok := expr.(*ast.IndexExpr); ok { // generic receiver, e.g. Layer[T]
		expr = idx.X
	}
	if id, ok := expr.(*ast.Ident); ok {
		return id.Name
	}
	return ""
}

// surfaceDiff renders the added/removed lines between two sorted snapshots.
func surfaceDiff(want, got []string) string {
	in := func(list []string) map[string]bool {
		m := make(map[string]bool, len(list))
		for _, s := range list {
			if s != "" {
				m[s] = true
			}
		}
		return m
	}
	oldSet, newSet := in(want), in(got)
	var b strings.Builder
	for _, s := range want {
		if s != "" && !newSet[s] {
			fmt.Fprintf(&b, "  - %s\n", s)
		}
	}
	for _, s := range got {
		if s != "" && !oldSet[s] {
			fmt.Fprintf(&b, "  + %s\n", s)
		}
	}
	return b.String()
}
