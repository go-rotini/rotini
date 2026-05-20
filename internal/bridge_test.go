package internal_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"reflect"
	"sort"
	"testing"

	"github.com/go-rotini/rotini/internal"
	"github.com/go-rotini/rotini/rtk"
)

// TestBridgeNamesByFullPathAvoidCollisions guards the handlers.gen.go
// (bridge) generator against leaf-name collisions. rotini is "commands
// all the way down", so the same leaf name can appear under different
// parents — e.g. `mycli cmd1 cmd1` and `mycli cmd2 cmd1`. The bridge
// derives every struct field, accessor method, and *HandlerImpl type
// from the full hyphen-joined command path (via toPascalCase /
// camelCasePath), not the leaf, so those overlapping commands map to
// distinct Go identifiers — matching the per-path Handlers interface in
// rotini.gen.go.
//
// A regression to leaf-based naming would emit two `cmd1` fields (a
// duplicate-field compile error in user projects). This test fails
// earlier and more clearly by asserting the field set is exactly the
// per-path set.
func TestBridgeNamesByFullPathAvoidCollisions(t *testing.T) {
	t.Parallel()
	spec := rtk.ProgramSpec{
		Name: "mycli",
		Commands: []rtk.CommandSpec{
			{Name: "cmd1", Commands: []rtk.CommandSpec{{Name: "cmd1"}}},
			{Name: "cmd2", Commands: []rtk.CommandSpec{{Name: "cmd1"}}},
		},
	}
	in := internal.NewRenderInput("handlers", spec).
		WithFramework("github.com/me/mycli/internal/rotini", "rotini")
	out, err := internal.Render("handlers.go.tmpl", in)
	if err != nil {
		t.Fatalf("render handlers.go.tmpl: %v", err)
	}

	got := bridgeStructFields(t, out)
	sort.Strings(got)
	// Full-path field names, root included; the two overlapping "cmd1"
	// leaves become cmd1Cmd1 and cmd2Cmd1 — no collision.
	want := []string{"cmd1", "cmd1Cmd1", "cmd2", "cmd2Cmd1", "root"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("handlers struct fields = %v, want %v\n--- generated ---\n%s", got, want, out)
	}
}

// bridgeStructFields parses generated bridge source and returns the
// field names of the `handlers` struct.
func bridgeStructFields(t *testing.T, src []byte) []string {
	t.Helper()
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "handlers.gen.go", src, 0)
	if err != nil {
		t.Fatalf("parse generated bridge: %v\n%s", err, src)
	}
	var fields []string
	ast.Inspect(f, func(n ast.Node) bool {
		ts, ok := n.(*ast.TypeSpec)
		if !ok || ts.Name.Name != "handlers" {
			return true
		}
		st, ok := ts.Type.(*ast.StructType)
		if !ok {
			return true
		}
		for _, fld := range st.Fields.List {
			for _, name := range fld.Names {
				fields = append(fields, name.Name)
			}
		}
		return false
	})
	if len(fields) == 0 {
		t.Fatalf("no `handlers` struct fields found in:\n%s", src)
	}
	return fields
}
