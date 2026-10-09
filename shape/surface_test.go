package shape

import (
	"bytes"
	"flag"
	"go/ast"
	"go/parser"
	"go/printer"
	"go/token"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// updateSurface refreshes the exported-surface snapshot:
// `go test ./shape -run Surface -update-surface`.
var updateSurface = flag.Bool("update-surface", false, "update the exported API surface snapshot")

// TestExportedSurface fails on any rename, signature change or newly exported symbol until the
// snapshot is updated with -update-surface, so the change shows in review.
func TestExportedSurface(t *testing.T) {
	got := strings.Join(exportedSurface(t), "\n") + "\n"
	golden := filepath.Join("testdata", "surface.txt")
	if *updateSurface {
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatal(err)
		}
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
		t.Errorf("exported surface changed; review it, then re-run with -update-surface if intended:\n--- want\n%s--- got\n%s", want, got)
	}
}

// exportedSurface renders one line per exported declaration, functions and methods with their
// signatures.
func exportedSurface(t *testing.T) []string {
	t.Helper()
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	node := func(n ast.Node) string {
		var b bytes.Buffer
		if err := printer.Fprint(&b, fset, n); err != nil {
			t.Fatal(err)
		}
		return b.String()
	}
	var out []string
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, d := range f.Decls {
			switch decl := d.(type) {
			case *ast.FuncDecl:
				if !decl.Name.IsExported() {
					continue
				}
				recv := ""
				if decl.Recv != nil {
					recv = node(decl.Recv.List[0].Type)
					if !ast.IsExported(strings.TrimPrefix(recv, "*")) {
						continue
					}
					recv = "(" + recv + ") "
				}
				decl.Doc, decl.Body = nil, nil
				out = append(out, "func "+recv+decl.Name.Name+strings.TrimPrefix(node(decl.Type), "func"))
			case *ast.GenDecl:
				for _, spec := range decl.Specs {
					switch s := spec.(type) {
					case *ast.TypeSpec:
						if s.Name.IsExported() {
							out = append(out, "type "+s.Name.Name)
						}
					case *ast.ValueSpec:
						for _, n := range s.Names {
							if n.IsExported() {
								out = append(out, decl.Tok.String()+" "+n.Name)
							}
						}
					}
				}
			}
		}
	}
	slices.Sort(out)
	return out
}
