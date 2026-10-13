package codegen

import (
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"strings"
	"testing"

	"github.com/go-rotini/jsonschema/gogen"
)

// generatorFields returns the struct field names the schema generator emits for an object
// with the given property keys, in order, or an error when it refuses the schema.
func generatorFields(t *testing.T, keys ...string) ([]string, error) {
	t.Helper()
	props := map[string]any{}
	for _, k := range keys {
		props[k] = map[string]any{"type": "string"}
	}
	doc, err := json.Marshal(map[string]any{
		"$schema": "http://json-schema.org/draft-07/schema#", "type": "object", "properties": props,
	})
	if err != nil {
		t.Fatal(err)
	}
	src, err := gogen.Generate(doc, gogen.WithPackage("p"), gogen.WithRootType("Root"))
	if err != nil {
		return nil, err
	}
	f, err := parser.ParseFile(token.NewFileSet(), "x.go", src, 0)
	if err != nil {
		t.Fatalf("parse %q: %v\n%s", keys, err, src)
	}
	var out []string
	ast.Inspect(f, func(n ast.Node) bool {
		if fl, ok := n.(*ast.Field); ok && len(fl.Names) == 1 {
			out = append(out, fl.Names[0].Name)
		}
		return true
	})
	return out, nil
}

// TestPropertyLintFollowsGenerator pins that lintGoNames reports a property exactly when the
// schema generator rotini is built with gives it no field of its own: it refuses the object,
// emits a field without a name, or gives two properties one field.
func TestPropertyLintFollowsGenerator(t *testing.T) {
	t.Parallel()
	for _, keys := range [][]string{
		{"foo"}, {"gen_file"}, {"$schema"}, {"x.y z"}, {"httpURL"}, {"v2"}, {"123"}, {"9lives"}, {"-"},
		{"foo", "Foo"}, {"a_b", "a-b"}, {"foo", "bar"},
	} {
		fields, err := generatorFields(t, keys...)
		bad := err != nil || len(fields) < len(keys)
		seen := map[string]bool{}
		for _, f := range fields {
			bad = bad || seen[f]
			seen[f] = true
		}
		props := map[string]Schema{}
		for _, k := range keys {
			props[k] = Schema{BaseSchema: BaseSchema{Type: "string"}}
		}
		spec := &Spec{Command: Command{Name: "app", Schemas: map[string]Schema{
			"Item": {BaseSchema: BaseSchema{Type: "object", Properties: props}},
		}}}
		if got := propertyNameProblems(spec); (len(got) > 0) != bad {
			t.Errorf("properties %q: generator fields %q (err %v), lint problems %q", keys, fields, err, got)
		}
	}
}

// TestPropertyLintRejectsUntaggable pins that a property whose name breaks its json tag is
// rejected whatever the generator does with it.
func TestPropertyLintRejectsUntaggable(t *testing.T) {
	t.Parallel()
	for _, k := range []string{"x,omitempty", `a"b`, "a`b"} {
		spec := &Spec{Command: Command{Name: "app", Schemas: map[string]Schema{
			"Item": {BaseSchema: BaseSchema{Type: "object", Properties: map[string]Schema{
				k: {BaseSchema: BaseSchema{Type: "string"}}, "ok": {BaseSchema: BaseSchema{Type: "string"}},
			}}},
		}}}
		if got := propertyNameProblems(spec); len(got) != 1 || !strings.Contains(got[0].Error(), "json tag") {
			t.Errorf("property %q: lint problems %q, want one about its json tag", k, got)
		}
	}
}
