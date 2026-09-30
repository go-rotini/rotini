package codegen

import (
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// TestRotiniTypeAliasesMatchTheResolver keeps rotiniTypeAliases and jsonSchemaTypeToGo's switch
// in step. The list is what lintSchemaTypes uses to tell an alias from a typo, so a name added
// to the switch but not the list is rejected as a typo, and a name left in the list after its
// case is removed is accepted and then generates an undefined identifier.
func TestRotiniTypeAliasesMatchTheResolver(t *testing.T) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "generate_inputs.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	var cases []string
	ast.Inspect(f, func(n ast.Node) bool {
		fn, ok := n.(*ast.FuncDecl)
		if !ok || fn.Name.Name != "jsonSchemaTypeToGo" {
			return true
		}
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			if cc, ok := n.(*ast.CaseClause); ok {
				for _, e := range cc.List {
					if lit, ok := e.(*ast.BasicLit); ok && lit.Kind == token.STRING {
						s, _ := strconv.Unquote(lit.Value)
						cases = append(cases, s)
					}
				}
			}
			return true
		})
		return false
	})
	if len(cases) == 0 {
		t.Fatal("found no cases in jsonSchemaTypeToGo — did it move or change shape?")
	}
	for _, c := range cases {
		if strings.ContainsAny(c, "[]") {
			continue // a Go spelling the switch passes through, not a name
		}
		if !slices.Contains(rotiniTypeAliases, c) && !slices.Contains(goPredeclaredTypes, c) {
			t.Errorf("jsonSchemaTypeToGo resolves %q, which rotiniTypeAliases does not list — lint would call it a typo", c)
		}
	}
	for _, a := range rotiniTypeAliases {
		if !slices.Contains(cases, a) {
			t.Errorf("rotiniTypeAliases lists %q, which jsonSchemaTypeToGo does not resolve", a)
		}
	}
}

// A Go-style list or map spelling resolves the names inside it, and carries their imports.
func TestJSONSchemaTypeToGo_listAndMapSpellings(t *testing.T) {
	for in, want := range map[string]string{
		"[]bytesize":              "[]rotini.ByteSize",
		"[]url":                   "[]*url.URL",
		"map[string]duration":     "map[string]time.Duration",
		"map[string][]ip":         "map[string][]netip.Addr",
		"[]map[string]integer":    "[]map[string]int",
		"[]string":                "[]string",
		"map[string]uuid.UUID":    "map[string]uuid.UUID",
		"[]Widget":                "[]Widget",
		"map[string]map[int]date": "map[string]map[int]time.Time",
	} {
		if got := jsonSchemaTypeToGo(in); got != want {
			t.Errorf("jsonSchemaTypeToGo(%q) = %q, want %q", in, got, want)
		}
	}
	for in, want := range map[string]string{
		"[]bytesize":          "", // rotini itself: generated code always imports it
		"map[string]duration": "time",
		"[]hostport":          "net/netip",
		"[]string":            "",
	} {
		if got := builtinImport(in); got != want {
			t.Errorf("builtinImport(%q) = %q, want %q", in, got, want)
		}
	}
}

// Help names a value type the way the spec wrote it — what to type, not how it is stored.
func TestFlagDisplayType_valueAliases(t *testing.T) {
	for _, tc := range []struct {
		schema *InputSchema
		want   string
	}{
		{&InputSchema{Type: "duration"}, "duration"},
		{&InputSchema{Type: "bytesize"}, "bytesize"},
		{&InputSchema{Type: "[]url"}, "[]url"},
		{&InputSchema{Type: "map[string]duration"}, "map[string]duration"},
		{&InputSchema{Type: "array", Items: &Schema{Type: "ip"}}, "[]ip"},
		{&InputSchema{Type: "integer"}, "int"}, // a shape alias still reads as its Go type
		{&InputSchema{Type: "array"}, "[]string"},
		{&InputSchema{Type: "bytesize", Placeholder: "SIZE"}, "SIZE"}, // the author's word wins
		{&InputSchema{Type: "bool"}, ""},
	} {
		if got := flagDisplayType(tc.schema); got != tc.want {
			t.Errorf("flagDisplayType(%+v) = %q, want %q", *tc.schema, got, tc.want)
		}
	}
}

// TestValueTypes_generateCompilingCode is the end-to-end check for the value-type vocabulary:
// every alias — bare, as a list, and as a map value — generates a field whose type and import
// compile. A wrong import path or an unresolved `[]alias` fails here as a build error.
func TestValueTypes_generateCompilingCode(t *testing.T) {
	skipUnlessCompiling(t)
	var flags strings.Builder
	for _, a := range valueTypeAliases {
		for i, typ := range []string{a, "[]" + a, "map[string]" + a} {
			name := a + strconv.Itoa(i)
			flags.WriteString("    - name: " + name + "\n      identifiers: [--" + name + "]\n      schema: {type: '" + typ + "'}\n")
		}
	}
	spec := "version: 0.0.0\ncommand:\n  name: vt\n  flags:\n" + flags.String()

	repoRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	writeTestFile(t, dir, "go.mod", "module example.com/vt\n\ngo 1.26\n\nrequire github.com/go-rotini/rotini v0.0.0\n\nreplace github.com/go-rotini/rotini => "+filepath.ToSlash(repoRoot)+"\n")
	writeTestFile(t, dir, ".rotini.spec.yaml", spec)
	writeTestFile(t, dir, ".rotini.conf.yaml", "version: 0.0.0\n")
	t.Chdir(dir)
	var problems []error
	if err := NewProcessor("0.0.0").Generate(".rotini.spec.yaml", ".rotini.conf.yaml", false,
		func(string, error) {}, func(ps []error) { problems = ps }); err != nil {
		t.Fatalf("Generate: %v %v", err, problems)
	}
	if out, err := goBuild(t, dir); err != nil {
		t.Fatalf("generated code does not compile:\n%s", out)
	}
}

// Every constraint tag must read back through reflect exactly as declared. A pattern was once
// written raw, and ^\d+$ is an invalid escape to strconv.Unquote — reflect read the tag as
// absent and the pattern was never enforced on env or config inputs.
func TestConstraintTags_readBackThroughReflect(t *testing.T) {
	schema := &InputSchema{
		BaseSchema: BaseSchema{
			Pattern: `^\d+\.\w+ "quoted" ` + "`tick`$",
			Enum:    []string{"fast", `a "b"`, `c\d`, "e`f"},
		},
		IgnoreCase: true,
	}
	schema.Minimum = new(1.0)
	tag := inputFieldTag(fieldDef{Tag: "x", Constraint: constraintTags(schema)})
	lit, err := strconv.Unquote(tag)
	if err != nil {
		t.Fatalf("the tag %s is not a valid Go string literal: %v", tag, err)
	}
	st := reflect.StructTag(lit)
	if got, ok := st.Lookup("pattern"); !ok || got != schema.Pattern {
		t.Errorf("pattern read back as %q (present %v), want %q", got, ok, schema.Pattern)
	}
	if got := st.Get("min"); got != "1" {
		t.Errorf("min read back as %q", got)
	}
	var enum []string
	if err := json.Unmarshal([]byte(st.Get("enum")), &enum); err != nil || !slices.Equal(enum, schema.Enum) {
		t.Errorf("enum read back as %v (%v), want %v", enum, err, schema.Enum)
	}
	if st.Get("ignorecase") != "true" {
		t.Error("ignorecase tag missing")
	}
	// The common case keeps the readable raw form.
	if plain := inputFieldTag(fieldDef{Tag: "x", Constraint: constraintTags(&InputSchema{BaseSchema: BaseSchema{Pattern: `^\d+$`}})}); !strings.HasPrefix(plain, "`") {
		t.Errorf("a tag with no backquote should stay a raw literal, got %s", plain)
	}
}

// variable: takes one name or a list, and every consumer sees the same ordered names.
func TestVariables(t *testing.T) {
	for _, tc := range []struct {
		v    any
		want []string
	}{
		{nil, nil},
		{"", nil},
		{"TOKEN", []string{"TOKEN"}},
		{[]any{"GH_TOKEN", "GITHUB_TOKEN"}, []string{"GH_TOKEN", "GITHUB_TOKEN"}},
		{[]string{"A", "B"}, []string{"A", "B"}},
	} {
		if got := variables(&InputSchema{Variable: tc.v}); !slices.Equal(got, tc.want) {
			t.Errorf("variables(%#v) = %q, want %q", tc.v, got, tc.want)
		}
	}
	if got := envVarOf(&InputSchema{Variable: []any{"A", "B"}}); got != "A,B" {
		t.Errorf("envVarOf = %q, want the comma-joined tag form", got)
	}
	e := EnvInput{Name: "token", Schema: &InputSchema{Variable: []any{"GH_TOKEN", "GITHUB_TOKEN"}}}
	if got := envVarLabel(e, ""); got != "GH_TOKEN, GITHUB_TOKEN" {
		t.Errorf("help label = %q", got)
	}
}

// An optional-value flag reads --x[=<type>] in help and shows what bare means; default_text
// replaces only the shown default.
func TestFlagRow_implicitValueAndDefaultText(t *testing.T) {
	row := flagRow(FlagInput{Name: "color", Identifiers: []string{"-c", "--color"},
		Schema: &InputSchema{BaseSchema: BaseSchema{Type: "string"}, Default: "auto", ImplicitValue: "always"}})
	if !slices.Equal(row.Identifiers, []string{"-c", "--color[=string]"}) || row.Type != "" || row.Implicit != "always" || row.Default != "auto" {
		t.Errorf("row = %+v", row)
	}
	row = flagRow(FlagInput{Name: "workers", Identifiers: []string{"--workers"},
		Schema: &InputSchema{BaseSchema: BaseSchema{Type: "int"}, Default: 4, DefaultText: "the number of CPUs"}})
	if row.Default != "the number of CPUs" || row.Implicit != "" {
		t.Errorf("row = %+v", row)
	}
}

// `description` on an object schema or its properties becomes the Go doc comment on the
// generated type and field. On an input's own schema it would do nothing — the input has
// `summary:` — so it is still rejected there.
func TestSchemaDescriptionsBecomeDocComments(t *testing.T) {
	emitted := composeModuleStaged(t, map[string]string{
		"cmd/root/.rotini.spec.yaml": `version: 0.0.0
command:
  name: root
  summary: r
  schemas:
    DB:
      description: The database the command connects to.
      type: object
      properties:
        host: {type: string, description: Hostname or IP of the primary.}
  output:
    type: object
    description: What root reports.
    properties:
      ok: {type: boolean, description: Whether it worked.}
`,
		"cmd/root/.rotini.conf.yaml": "version: 0.0.0\ngenerate:\n  packages:\n    - type: cmd\n      file: internal/cmd/root/zz_root.go\n      package: root\n",
	})
	gen := emitted["internal/cmd/root/zz_root.go"]
	for _, want := range []string{
		"// The database the command connects to.\ntype DB struct",
		"// Hostname or IP of the primary.\n\tHost string",
		"// Whether it worked.\n\tOk ",
	} {
		if !strings.Contains(gen, want) {
			t.Errorf("generated code missing %q:\n%s", want, gen)
		}
	}

	err, _ := validateInModule(t, blockSpecHead+
		"  flags:\n    - name: a\n      identifiers: [--a]\n      summary: s\n      schema: { type: string, description: nope }\n",
		goldenConf, "")
	if err == nil || !strings.Contains(err.Error(), `unknown key "description"`) {
		t.Errorf("description on an input schema: err = %v, want it rejected", err)
	}
}
