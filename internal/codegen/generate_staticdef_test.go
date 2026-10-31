package codegen

import (
	"go/format"
	"strings"
	"testing"
)

// TestMakeStatic_definition pins the static definition: pointers at package variables, the
// types and maps moved into fills keyed by their place in the tree, and everything else kept
// byte for byte.
func TestMakeStatic_definition(t *testing.T) {
	src := `var definition = rotini.Definition{
Name: "demo",
Inputs: rotiniTypeOf((*DemoInputs)(nil)),
Metadata: map[string]string{"a": "b"},
Commands: []rotini.CommandDef{
{Name: "build",
Flags: []rotini.FlagDef{
{Name: "n", Minimum: new(float64(-1.5)), Maximum: new(float64(1e+06)), MaxItems: new(8), Metadata: map[string]string{"k": "v"}},
},
Output: &rotini.OutputDef{Type: rotiniTypeOf((*DemoBuildOutput)(nil)), Schema: ` + "`{}`" + `},
Effects: &rotini.Effects{Kind: "write", Idempotent: new(true), OpenWorld: new(false)},
},
},
}
`
	pv := pointerVars{}
	got, err := makeStatic(src, pv, true)
	if err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"rotiniTypeOf(", "new(", "map["} {
		if strings.Contains(got.decl, bad) {
			t.Errorf("the literal keeps %q:\n%s", bad, got.decl)
		}
	}
	for _, want := range []string{
		"Name: \"demo\",\nCommands:",
		"{Name: \"n\", Minimum: &rotiniFloatN1p5, Maximum: &rotiniFloat1e06, MaxItems: &rotiniInt8, }",
		"Output: &rotini.OutputDef{ Schema: `{}`}",
		"Idempotent: &rotiniTrue, OpenWorld: &rotiniFalse}",
	} {
		if !strings.Contains(got.decl, want) {
			t.Errorf("the literal lacks %q:\n%s", want, got.decl)
		}
	}
	wantFills := []string{
		".Inputs = rotiniTypeOf((*DemoInputs)(nil))",
		`.Metadata = map[string]string{"a": "b"}`,
		`.Commands[0].Flags[0].Metadata = map[string]string{"k": "v"}`,
		".Commands[0].Output.Type = rotiniTypeOf((*DemoBuildOutput)(nil))",
	}
	if strings.Join(got.fills, "\n") != strings.Join(wantFills, "\n") {
		t.Errorf("fills:\n%s\nwant:\n%s", strings.Join(got.fills, "\n"), strings.Join(wantFills, "\n"))
	}
	vars := pv.decl()
	for _, want := range []string{"rotiniFalse = false", "rotiniTrue = true", "rotiniInt8 = 8", "rotiniFloatN1p5 = float64(-1.5)", "rotiniFloat1e06 = float64(1e+06)"} {
		if !strings.Contains(vars, want) {
			t.Errorf("the pointer variables lack %q:\n%s", want, vars)
		}
	}
	file := "package p\n" + got.decl + definitionFill(got.fills) + vars
	if _, err := format.Source([]byte(file)); err != nil {
		t.Errorf("the static definition is not valid Go: %v\n%s", err, file)
	}
}

// TestMakeStatic_keepsWithoutMove pins InputSettings' form: pointers move to variables, but a
// map stays in the exported literal, which handler code may read before NewProgram.
func TestMakeStatic_keepsWithoutMove(t *testing.T) {
	src := "// InputSettings is the descriptor.\nvar InputSettings = rotini.InputSettings{\nStdinSchemas: map[string]string{\"a\": `{}`},\nConfigFiles: []rotini.ConfigFile{{Name: \"user\", Keys: []rotini.ConfigKey{{Key: \"n\", Minimum: new(float64(0))}}}},\n}"
	pv := pointerVars{}
	got, err := makeStatic(src, pv, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.fills) != 0 {
		t.Errorf("fills without move: %v", got.fills)
	}
	want := strings.Replace(src, "new(float64(0))", "&rotiniFloat0", 1)
	if got.decl != want {
		t.Errorf("got:\n%s\nwant:\n%s", got.decl, want)
	}
}

// TestMakeStatic_positionalAndNoFills pins a moved list element becoming nil in its slot,
// and a literal with nothing dynamic coming back unchanged with no fill function.
func TestMakeStatic_positionalAndNoFills(t *testing.T) {
	got, err := makeStatic("var x = T{List: []map[string]string{nil, map[string]string{\"a\": \"b\"}}}\n", pointerVars{}, true)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got.decl, "List: []map[string]string{nil, nil}") || len(got.fills) != 1 || got.fills[0] != `.List[1] = map[string]string{"a": "b"}` {
		t.Errorf("got %q, fills %q", got.decl, got.fills)
	}
	plain := "var definition = rotini.Definition{\nName: \"demo\",\n}\n"
	got, err = makeStatic(plain, pointerVars{}, true)
	if err != nil {
		t.Fatal(err)
	}
	if got.decl != plain || definitionFill(got.fills) != "" || (pointerVars{}).decl() != "" {
		t.Errorf("a static literal changed: %q, fill %q", got.decl, definitionFill(got.fills))
	}
}
