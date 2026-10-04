package rotini

import (
	"go/ast"
	"go/doc"
	"go/parser"
	"go/token"
	"os"
	"strings"
	"testing"
)

// Program and Context open their doc with a map of their surface, grouped by job. A map is read
// as exhaustive, so these tests fail when a method is missing from it or a mapped name no
// longer exists.

// packageDoc parses this package's non-test sources into go/doc's view of it. It walks the
// files itself because parser.ParseDir is deprecated and its replacement,
// golang.org/x/tools/go/packages, would add a dependency outside the go-rotini family.
func packageDoc(t *testing.T) *doc.Package {
	t.Helper()
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	var files []*ast.File
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, name, nil, parser.ParseComments)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		files = append(files, f)
	}
	if len(files) == 0 {
		t.Fatal("no non-test sources found")
	}
	d, err := doc.NewFromFiles(fset, files, "github.com/go-rotini/rotini", doc.PreserveAST)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

// mappedTypes are the types whose doc claims to survey their own surface.
var mappedTypes = []string{"Program", "Context"}

func docType(t *testing.T, d *doc.Package, name string) *doc.Type {
	t.Helper()
	for _, c := range d.Types {
		if c.Name == name {
			return c
		}
	}
	t.Fatalf("type %s not found", name)
	return nil
}

func TestTypeDocsMapEveryMethod(t *testing.T) {
	d := packageDoc(t)
	for _, typeName := range mappedTypes {
		t.Run(typeName, func(t *testing.T) {
			dt := docType(t, d, typeName)
			if len(dt.Methods) == 0 {
				t.Fatalf("%s has no exported methods — the fixture is wrong", typeName)
			}

			var missing []string
			for _, m := range dt.Methods {
				// A doc link or the bare qualified name in the prose both count as placed.
				if !strings.Contains(dt.Doc, typeName+"."+m.Name) {
					missing = append(missing, m.Name)
				}
			}
			if len(missing) > 0 {
				t.Errorf("%s's doc surveys its surface but does not place: %v\n"+
					"Put each in the right group — a map that omits one still reads as exhaustive.",
					typeName, missing)
			}
		})
	}
}

// TestTypeDocsNameNothingImaginary pins the reverse direction: every method a doc map links to
// exists.
func TestTypeDocsNameNothingImaginary(t *testing.T) {
	d := packageDoc(t)
	for _, typeName := range mappedTypes {
		t.Run(typeName, func(t *testing.T) {
			dt := docType(t, d, typeName)

			declared := map[string]bool{}
			for _, m := range dt.Methods {
				declared[m.Name] = true
			}
			// Exported fields are legitimate link targets too.
			if ts, ok := dt.Decl.Specs[0].(*ast.TypeSpec); ok {
				if st, ok := ts.Type.(*ast.StructType); ok {
					for _, f := range st.Fields.List {
						for _, n := range f.Names {
							declared[n.Name] = true
						}
					}
				}
			}

			for _, ref := range docLinkTargets(dt.Doc, typeName) {
				if !declared[ref] {
					t.Errorf("%s's doc links [%s.%s], which does not exist", typeName, typeName, ref)
				}
			}
		})
	}
}

// docLinkTargets returns each X named by a "[Type.X]" doc link in text.
func docLinkTargets(text, typeName string) []string {
	var out []string
	needle := "[" + typeName + "."
	for i := 0; ; {
		j := strings.Index(text[i:], needle)
		if j < 0 {
			return out
		}
		start := i + j + len(needle)
		end := strings.IndexByte(text[start:], ']')
		if end < 0 {
			return out
		}
		out = append(out, text[start:start+end])
		i = start + end
	}
}
