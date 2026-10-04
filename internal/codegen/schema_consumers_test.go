package codegen

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// These tests enforce doc.go's promise that nothing schema-accepted is silently ignored:
// every field of every generated schema type must be read in non-test source, by codegen,
// the runtime, a template, or a lint rule that rejects it.

// unreadSchemaFields lists fields that are deliberately never read, with the reason. It
// should stay empty.
var unreadSchemaFields = map[string]string{}

// TestEverySchemaKeyHasAConsumer walks the generated schema types and fails on any field no
// non-test file mentions.
func TestEverySchemaKeyHasAConsumer(t *testing.T) {
	fields := schemaFields(t, "schema_spec.go", "schema_conf.go")
	if len(fields) == 0 {
		t.Fatal("found no schema fields to check — the parse is broken, not the schema")
	}

	read := readFieldNames(t)
	for qualified, name := range fields {
		if why, ok := unreadSchemaFields[qualified]; ok {
			if read[name] {
				t.Errorf("%s is listed as deliberately unread (%s) but something reads it — remove the entry", qualified, why)
			}
			continue
		}
		if !read[name] {
			t.Errorf("%s has no consumer: no non-test file reads .%s.\n"+
				"Every schema key must be read by codegen, by the runtime, or by a lint rule that "+
				"rejects it — a key nothing consumes is silently ignored, which doc.go promises cannot happen.",
				qualified, name)
		}
	}
}

// schemaFields returns every exported field of every type declared in the given generated
// files, keyed "Type.Field".
func schemaFields(t *testing.T, files ...string) map[string]string {
	t.Helper()
	out := map[string]string{}
	fset := token.NewFileSet()
	for _, name := range files {
		f, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		ast.Inspect(f, func(n ast.Node) bool {
			ts, ok := n.(*ast.TypeSpec)
			if !ok {
				return true
			}
			st, ok := ts.Type.(*ast.StructType)
			if !ok || st.Fields == nil {
				return true
			}
			for _, field := range st.Fields.List {
				for _, id := range field.Names {
					if id.IsExported() {
						out[ts.Name.Name+"."+id.Name] = id.Name
					}
				}
			}
			return true
		})
	}
	return out
}

// readFieldNames collects every ".Name" selector in this package's non-test source, the
// runtime, and the codegen templates. Templates count because a field copied by whole-struct
// conversion may be read only there (e.g. `{{.Code}}` in man.txt.tmpl).
func readFieldNames(t *testing.T) map[string]bool {
	t.Helper()
	read := map[string]bool{}
	fset := token.NewFileSet()

	collect := func(dir string) {
		entries, err := os.ReadDir(dir)
		if err != nil {
			t.Fatalf("read %s: %v", dir, err)
		}
		for _, e := range entries {
			name := e.Name()
			if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
				continue
			}
			// The generated schema types declare the fields rather than read them.
			if name == "schema_spec.go" || name == "schema_conf.go" {
				continue
			}
			f, err := parser.ParseFile(fset, filepath.Join(dir, name), nil, 0)
			if err != nil {
				t.Fatalf("parse %s: %v", name, err)
			}
			ast.Inspect(f, func(n ast.Node) bool {
				if sel, ok := n.(*ast.SelectorExpr); ok {
					read[sel.Sel.Name] = true
				}
				return true
			})
		}
	}

	collect(".")                       // internal/codegen
	collect(filepath.Join("..", "..")) // the runtime
	collectTemplates(t, read)
	return read
}

// collectTemplates records every "{{...}}" field selector in the codegen templates, so a key
// rendered straight into a page counts as consumed.
func collectTemplates(t *testing.T, read map[string]bool) {
	t.Helper()
	dir := filepath.Join("templates")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read %s: %v", dir, err)
	}
	selector := regexp.MustCompile(`\.([A-Z][A-Za-z0-9_]*)`)
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		b, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			t.Fatalf("read %s: %v", e.Name(), err)
		}
		for _, m := range selector.FindAllStringSubmatch(string(b), -1) {
			read[m[1]] = true
		}
	}
}
