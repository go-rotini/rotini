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

// doc.go makes a promise about validation that is easy to state and easy to break:
//
//	Nothing schema-accepted is silently ignored — a key either has a consumer or
//	validation rejects it.
//
// The reference files enforced it by hand, with a ✘ marker for any key that had no consumer
// yet. A marker only works while someone is looking, and seven keys were added to the schemas
// in one sitting — `variable:` had in fact been accepted and ignored on three channels for
// some time, documented as env-only and enforced nowhere.
//
// So the promise is a test. Every field of every generated schema type must be READ somewhere
// in non-test source: by codegen, by the runtime, or by a lint rule that rejects it. Rejecting
// a key is a consumer — that is exactly what `timeout` on a local command is for.

// unreadSchemaFields lists fields that are deliberately never read, with the reason. It should
// stay empty; an entry is a decision, not a waiver.
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

// readFieldNames collects every selector ".Name" appearing in the package's non-test source,
// in the runtime, and in the codegen TEMPLATES — the three places a spec key ends up being
// acted on.
//
// The templates matter and were missed at first. A key can reach codegen by a whole-struct
// CONVERSION — `templateDocExitRow(e)` copies every field of an ExitStatusEntry in one
// expression — and then be read only by `{{.Code}}` in man.txt.tmpl. No Go selector exists
// anywhere for it. `exit_status.code` is exactly that shape, and it passed this test for
// months on an unrelated `.Code` in the JSON-RPC server that used to live in the runtime;
// deleting that server is what exposed the blind spot.
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
			// The generated schema types DECLARE the fields; they do not read them.
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
	collect(filepath.Join("..", "..")) // the runtime, where several keys are finally acted on
	collectTemplates(t, read)          // the output stage, where a converted struct is finally read
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
