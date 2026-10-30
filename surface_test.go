package rotini

import (
	"flag"
	"fmt"
	"go/ast"
	"go/parser"
	"go/printer"
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

// TestExportedSurface is the API freeze: any rename, signature change or newly exported symbol
// fails until the snapshot is updated with -update-surface, so the change shows in review. It
// covers exported funcs and methods with their signatures, types with their type parameters
// (and the underlying type of one that is not a struct or interface), struct fields and
// interface methods with their types, and the names (and declared types) of consts and vars.
func TestExportedSurface(t *testing.T) {
	got := strings.Join(exportedSurface(t, "."), "\n") + "\n"
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

// exportedSurface renders one stable line per exported declaration of the package in dir:
// "func Name(sig)", "type Name", "method Recv.Name(sig)", "field Type.Name T", "const Name",
// "var Name".
func exportedSurface(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := filepath.Glob(filepath.Join(dir, "*.go"))
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
					out = append(out, "func "+decl.Name.Name+signature(fset, decl.Type))
					continue
				}
				if recv := receiverName(decl.Recv); recv != "" && ast.IsExported(recv) {
					out = append(out, "method "+recv+"."+decl.Name.Name+signature(fset, decl.Type))
				}
			case *ast.GenDecl:
				out = append(out, exportedSpecs(fset, decl)...)
			}
		}
	}
	sort.Strings(out)
	return out
}

// source renders an AST node as Go source on one line.
func source(fset *token.FileSet, n ast.Node) string {
	var b strings.Builder
	if err := printer.Fprint(&b, fset, n); err != nil {
		return "?"
	}
	return strings.Join(strings.Fields(b.String()), " ")
}

// signature renders a func type's type parameters, parameters and results as source.
func signature(fset *token.FileSet, ft *ast.FuncType) string {
	return strings.TrimPrefix(source(fset, ft), "func")
}

// exportedSpecs renders the exported type/const/var declarations of one declaration block.
func exportedSpecs(fset *token.FileSet, decl *ast.GenDecl) []string {
	var out []string
	for _, s := range decl.Specs {
		switch spec := s.(type) {
		case *ast.TypeSpec:
			if !spec.Name.IsExported() {
				continue
			}
			out = append(out, "type "+spec.Name.Name+typeHeader(fset, spec))
			out = append(out, exportedMembers(fset, spec)...)
		case *ast.ValueSpec:
			kind := "var"
			if decl.Tok == token.CONST {
				kind = "const"
			}
			typ := ""
			if spec.Type != nil {
				typ = " " + source(fset, spec.Type)
			}
			for _, n := range spec.Names {
				if n.IsExported() {
					out = append(out, kind+" "+n.Name+typ)
				}
			}
		}
	}
	return out
}

// typeHeader renders what follows a type's name: its type parameters, then "= T" for an
// alias, or the underlying type for a type that is not a struct or interface (whose members
// are recorded line by line instead).
func typeHeader(fset *token.FileSet, spec *ast.TypeSpec) string {
	head := ""
	if spec.TypeParams != nil {
		var params []string
		for _, f := range spec.TypeParams.List {
			for _, n := range f.Names {
				params = append(params, n.Name+" "+source(fset, f.Type))
			}
		}
		head = "[" + strings.Join(params, ", ") + "]"
	}
	if spec.Assign.IsValid() {
		return head + " = " + source(fset, spec.Type)
	}
	switch spec.Type.(type) {
	case *ast.StructType:
		return head + " struct"
	case *ast.InterfaceType:
		return head + " interface"
	}
	return head + " " + source(fset, spec.Type)
}

// exportedMembers renders a type's exported struct fields and interface methods, which
// generated code and handlers depend on by name and type. An embedded field is recorded under
// the embedded type's name (Constraints in FlagDef, say).
func exportedMembers(fset *token.FileSet, spec *ast.TypeSpec) []string {
	var out []string
	var list *ast.FieldList
	kind := "field "
	switch t := spec.Type.(type) {
	case *ast.StructType:
		list = t.Fields
	case *ast.InterfaceType:
		list, kind = t.Methods, "method "
	default:
		return nil
	}
	for _, f := range list.List {
		if len(f.Names) == 0 { // embedded field, or an embedded interface or type set
			name := embeddedName(f.Type)
			if name == "" || ast.IsExported(name) {
				out = append(out, kind+spec.Name.Name+"."+source(fset, f.Type))
			}
			continue
		}
		typ := " " + source(fset, f.Type)
		if ft, ok := f.Type.(*ast.FuncType); ok && kind == "method " {
			typ = signature(fset, ft)
		}
		for _, n := range f.Names {
			if n.IsExported() {
				out = append(out, kind+spec.Name.Name+"."+n.Name+typ)
			}
		}
	}
	return out
}

// embeddedName is the bare type name of an embedded field, dropping a pointer or a package
// qualifier: *Constraints and recon.Source both reduce to what a caller would write. A type
// set term such as ~int has none.
func embeddedName(expr ast.Expr) string {
	switch t := expr.(type) {
	case *ast.StarExpr:
		return embeddedName(t.X)
	case *ast.SelectorExpr:
		return t.Sel.Name
	case *ast.Ident:
		return t.Name
	case *ast.IndexExpr:
		return embeddedName(t.X)
	case *ast.IndexListExpr:
		return embeddedName(t.X)
	}
	return ""
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
	switch idx := expr.(type) { // generic receiver, e.g. InputLayer[T]
	case *ast.IndexExpr:
		expr = idx.X
	case *ast.IndexListExpr:
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
