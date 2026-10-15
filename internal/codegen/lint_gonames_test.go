package codegen

import (
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"maps"
	"slices"
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
			props[k] = Schema{Type: "string"}
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

// TestCommandPrefixClashesSkipsSameName pins that two siblings with one name, and their
// sub-commands, aren't reported again as a Go name clash; distinct paths still are.
func TestCommandPrefixClashesSkipsSameName(t *testing.T) {
	t.Parallel()
	build := Command{Name: "build", Commands: []Command{{Name: "image"}}}
	spec := &Spec{Command: Command{Name: "app", Commands: []Command{build, build}}}
	if got := commandPrefixClashes(spec); len(got) != 0 {
		t.Errorf("same-named siblings: lint problems %q, want none", got)
	}
	spec = &Spec{Command: Command{Name: "app", Commands: []Command{{Name: "a-b"}, {Name: "a_b"}}}}
	if got := commandPrefixClashes(spec); len(got) != 1 {
		t.Errorf("a-b and a_b: lint problems %q, want one", got)
	}
}

// TestGeneratedFieldNamesAllMatchesOneByOne pins that asking the generator for many objects in
// one document names every object's fields as asking for each object alone does, including
// objects it refuses, repeated key sets and objects without keys.
func TestGeneratedFieldNamesAllMatchesOneByOne(t *testing.T) {
	t.Parallel()
	sets := [][]string{
		{"foo"}, {"Foo", "foo"}, {"1", "field1"}, {"-", "_", "a-b", "a_b"}, {"ID", "id"},
		{"httpURL", "httpUrl"}, {"é", "日本"}, {"", "x"}, {"a\\b", "c"}, {"a\tb", "a\nb", "ok"},
		{"$schema", "gen_file"}, {}, {"foo"}, {"9lives", "v2"}, {"Probe0", "Root"},
	}
	all := generatedFieldNamesAll(sets)
	for i, keys := range sets {
		want := generatedFieldNames(keys)
		if !maps.Equal(all[i], want) {
			t.Errorf("keys %q: together %q, alone %q", keys, all[i], want)
		}
	}
}

// benchKeySets returns n objects of ten properties, a quarter of them repeating a shape.
func benchKeySets(n int) [][]string {
	sets := make([][]string, n)
	for i := range sets {
		shape := i
		if i%4 == 3 {
			shape = i - 1
		}
		keys := make([]string, 10)
		for k := range keys {
			keys[k] = fmt.Sprintf("field_%d_%d", shape, k)
		}
		slices.Sort(keys)
		sets[i] = keys
	}
	return sets
}

func BenchmarkGeneratedFieldNames(b *testing.B) {
	sets := benchKeySets(1000)
	b.Run("together", func(b *testing.B) {
		for b.Loop() {
			generatedFieldNamesAll(sets)
		}
	})
	b.Run("one-by-one", func(b *testing.B) {
		for b.Loop() {
			for _, keys := range sets {
				generatedFieldNames(keys)
			}
		}
	})
}
