package internal_test

import (
	"go/format"
	"go/parser"
	"go/token"
	"strings"
	"testing"

	"github.com/go-rotini/rotini/internal"
	"github.com/go-rotini/rotini/rtk"
)

// =============================================================================
// Fixture spec used by most rendering tests
// =============================================================================

// fixtureSpec returns a small but representative spec covering:
//   - root flag (string with default)
//   - root flag (bool)
//   - one top-level command with a flag + required positional
//   - one nested command path (foo → foo-bar) to exercise the
//     ancestor-composition logic in inputs.gen.tmpl
func fixtureSpec() rtk.ProgramSpec {
	return rtk.ProgramSpec{
		Name: "todo",
		Flags: []rtk.FlagSpec{
			{Name: "output", Identifiers: []string{"-o", "--output"}, Type: "string", Default: "table"},
			{Name: "verbose", Identifiers: []string{"-v", "--verbose"}, Type: "bool"},
		},
		Commands: []rtk.CommandSpec{
			{
				Path: "add", Name: "add",
				Flags: []rtk.FlagSpec{
					{Name: "priority", Identifiers: []string{"-p"}, Type: "int"},
				},
				Arguments: []rtk.ArgumentSpec{
					{Name: "title", Type: "string", Required: true},
				},
			},
			{
				Path: "foo", Name: "foo",
				Flags: []rtk.FlagSpec{
					{Name: "fopt", Identifiers: []string{"--fopt"}, Type: "string"},
				},
				Commands: []rtk.CommandSpec{
					{
						Path: "foo-bar", Name: "bar",
						Flags: []rtk.FlagSpec{
							{Name: "bopt", Identifiers: []string{"--bopt"}, Type: "int"},
						},
					},
				},
			},
		},
	}
}

// =============================================================================
// Driver: render + gofmt + go-parse
// =============================================================================

// renderOK renders the named template and asserts the output is valid
// Go (passes go/parser). It returns the rendered bytes so callers can
// do additional structural assertions.
func renderOK(t *testing.T, name string, in internal.RenderInput) []byte {
	t.Helper()
	out, err := internal.Render(name, in)
	if err != nil {
		t.Fatalf("Render(%s): %v\n--- output so far ---\n%s", name, err, out)
	}
	// gofmt-equivalent invariance check: re-formatting should yield
	// the exact same bytes (Render runs gofmt internally; this catches
	// any drift introduced by raw template padding).
	reformatted, err := format.Source(out)
	if err != nil {
		t.Fatalf("re-format %s: %v", name, err)
	}
	if string(reformatted) != string(out) {
		t.Errorf("%s output not gofmt-idempotent", name)
	}
	// Parse as Go source to ensure the output is structurally valid.
	if _, err := parser.ParseFile(token.NewFileSet(), name, out, parser.AllErrors); err != nil {
		t.Fatalf("parse %s as Go: %v\n--- output ---\n%s", name, err, out)
	}
	return out
}

// containsAll fails the test when any wanted substring is missing from
// got. Useful for asserting "the template emitted X, Y, and Z" without
// pinning down exact whitespace.
func containsAll(t *testing.T, name string, got []byte, wants ...string) {
	t.Helper()
	for _, w := range wants {
		if !strings.Contains(string(got), w) {
			t.Errorf("%s: missing %q\n--- got ---\n%s", name, w, got)
		}
	}
}

// =============================================================================
// spec.gen.tmpl
// =============================================================================

func TestRender_specGen(t *testing.T) {
	t.Parallel()
	in := internal.NewRenderInput("rotini", fixtureSpec())
	out := renderOK(t, "spec.gen.tmpl", in)

	containsAll(t, "spec.gen.tmpl", out,
		`package rotini`,
		`var Spec = rtk.ProgramSpec{`,
		`Name: "todo"`,
		`Name: "output"`,
		`Identifiers: []string{"-o", "--output"}`,
		`Default: "table"`,
		`Path: "add"`,
		`Required: true`,
		`Path: "foo"`,
		`Path: "foo-bar"`,
	)
}

func TestRender_specGen_emptySpec(t *testing.T) {
	t.Parallel()
	in := internal.NewRenderInput("rotini", rtk.ProgramSpec{Name: "minimal"})
	out := renderOK(t, "spec.gen.tmpl", in)
	containsAll(t, "spec.gen.tmpl", out,
		`package rotini`,
		`Name: "minimal"`,
	)
}

// =============================================================================
// render.gen.tmpl
// =============================================================================

func TestRender_renderGen(t *testing.T) {
	t.Parallel()
	in := internal.NewRenderInput("rotini", fixtureSpec())
	out := renderOK(t, "render.gen.tmpl", in)

	containsAll(t, "render.gen.tmpl", out,
		`package rotini`,
		`func RenderError(w io.Writer, err error)`,
		`func ExitCode(err error) int`,
		`"todo"`, // program name appears in the prefix initializer
		`rtk.UnknownCommandError`,
		`rtk.UnknownFlagError`,
		`rtk.MissingRequiredError`,
		`rtk.CoercionError`,
		`rtk.ValidationError`,
		`rtk.EarlyExit`,
	)
}

func TestRender_renderGen_unnamedProgramFallback(t *testing.T) {
	t.Parallel()
	in := internal.NewRenderInput("rotini", rtk.ProgramSpec{}) // no Name
	out := renderOK(t, "render.gen.tmpl", in)
	// Empty name still renders; the fallback "rotini" sits inside the
	// generated function body.
	containsAll(t, "render.gen.tmpl", out, `prefix := ""`, `"rotini"`)
}

// =============================================================================
// inputs.gen.tmpl
// =============================================================================

func TestRender_inputsGen(t *testing.T) {
	t.Parallel()
	in := internal.NewRenderInput("rotini", fixtureSpec())
	out := renderOK(t, "inputs.gen.tmpl", in)

	containsAll(t, "inputs.gen.tmpl", out,
		`package rotini`,
		`type RootFlags struct`,
		`Output  string`, // gofmt aligns with Verbose bool below
		`Verbose bool`,
		`type RootInputs struct`,
		`func (i *RootInputs) RotiniCommandPath() string { return "" }`,
		`type AddFlags struct`,
		`Priority int`,
		`type AddArguments struct`,
		`Title string`,
		`type AddInputs struct`,
		`func (i *AddInputs) RotiniCommandPath() string { return "add" }`,
		`rtk.AssignFlag(scope, "output"`,
		`rtk.AssignFlag(scope, "priority"`,
		`rtk.AssignStringArg(r.ParsedArgs, 0`,
		`type FooBarFlags struct`,
		`Bopt int`,
		`type FooBarInputs struct`,
		"FooFlags "+" FooFlags", // field name + type; literal split to dodge dupword linter
		`func (i *FooBarInputs) RotiniCommandPath() string { return "foo-bar" }`,
	)
}

func TestRender_inputsGen_populateFromArgv_callsAncestors(t *testing.T) {
	t.Parallel()
	in := internal.NewRenderInput("rotini", fixtureSpec())
	out := renderOK(t, "inputs.gen.tmpl", in)
	// FooBarInputs.PopulateFromArgv should pull from root scope (""),
	// the "foo" intermediate scope, and the "bar" leaf scope.
	got := string(out)
	if !strings.Contains(got, `if scope := r.FlagsByScope[""]; scope != nil {`) {
		t.Errorf("FooBarInputs missing root-scope read")
	}
	if !strings.Contains(got, `if scope := r.FlagsByScope["foo"]; scope != nil {`) {
		t.Errorf("FooBarInputs missing foo-scope read")
	}
	if !strings.Contains(got, `if scope := r.FlagsByScope["bar"]; scope != nil {`) {
		t.Errorf("FooBarInputs missing bar-scope read")
	}
}

func TestRender_inputsGen_variadicArg(t *testing.T) {
	t.Parallel()
	spec := rtk.ProgramSpec{
		Name: "cat",
		Commands: []rtk.CommandSpec{
			{
				Path: "cat", Name: "cat",
				Arguments: []rtk.ArgumentSpec{
					{Name: "paths", Type: "string", Variadic: true},
				},
			},
		},
	}
	in := internal.NewRenderInput("rotini", spec)
	out := renderOK(t, "inputs.gen.tmpl", in)
	containsAll(t, "inputs.gen.tmpl", out,
		`Paths []string`,
		`rtk.AssignVariadicStringArg(r.ParsedArgs, 0`,
	)
}

func TestRender_inputsGen_coercedArg(t *testing.T) {
	t.Parallel()
	spec := rtk.ProgramSpec{
		Name: "x",
		Commands: []rtk.CommandSpec{
			{
				Path: "count", Name: "count",
				Arguments: []rtk.ArgumentSpec{
					{Name: "n", Type: "int", Required: true},
				},
			},
		},
	}
	in := internal.NewRenderInput("rotini", spec)
	out := renderOK(t, "inputs.gen.tmpl", in)
	containsAll(t, "inputs.gen.tmpl", out,
		`N int`,
		`rtk.AssignCoercedArg(r.ParsedArgs, 0, "int", &i.Arguments.N, rtk.CoerceBuiltinValue)`,
	)
}

// =============================================================================
// handlers.gen.tmpl
// =============================================================================

func TestRender_handlersGen(t *testing.T) {
	t.Parallel()
	in := internal.NewRenderInput("rotini", fixtureSpec())
	out := renderOK(t, "handlers.gen.tmpl", in)

	containsAll(t, "handlers.gen.tmpl", out,
		`package rotini`,
		`type RootCtx = rtk.Ctx`,
		`type RootHandler interface`,
		`Run(ctx RootCtx, inputs *RootInputs) error`,
		`type AddCtx = rtk.Ctx`,
		`type AddHandler interface`,
		`Run(ctx AddCtx, inputs *AddInputs) error`,
		`type FooBarCtx = rtk.Ctx`,
		`type FooBarHandler interface`,
		`Run(ctx FooBarCtx, inputs *FooBarInputs) error`,
		`type Handlers interface`,
		`Root() RootHandler`,
		`Add() AddHandler`,
		`Foo() FooHandler`,
		`FooBar() FooBarHandler`,
	)
}

// =============================================================================
// lifecycle.gen.tmpl
// =============================================================================

func TestRender_lifecycleGen(t *testing.T) {
	t.Parallel()
	in := internal.NewRenderInput("rotini", fixtureSpec())
	out := renderOK(t, "lifecycle.gen.tmpl", in)

	containsAll(t, "lifecycle.gen.tmpl", out,
		`package rotini`,
		`func SafeCall(fn func() error) (err error)`,
		`type Execution struct`,
		`Path string`,
		`Run func() error`, // godoc comments between fields prevent gofmt alignment
		`func RunExecution(ex Execution) error`,
		`rtk.EarlyExit`,
		`ErrEmptyExecution`,
	)
}

// =============================================================================
// executors.gen.tmpl
// =============================================================================

func TestRender_executorsGen(t *testing.T) {
	t.Parallel()
	in := internal.NewRenderInput("rotini", fixtureSpec())
	out := renderOK(t, "executors.gen.tmpl", in)

	containsAll(t, "executors.gen.tmpl", out,
		`package rotini`,
		`var executors = map[string]func(*Program) Execution{`,
		`"":        buildRootExecution`,
		`"add":     buildAddExecution`,
		`"foo":     buildFooExecution`,
		`"foo-bar": buildFooBarExecution`,
		`func buildRootExecution(p *Program) Execution`,
		`func buildAddExecution(p *Program) Execution`,
		`func buildFooBarExecution(p *Program) Execution`,
		`var inputs RootInputs`,
		`var inputs AddInputs`,
		`var inputs FooBarInputs`,
		`p.handlers.Root()`,
		`p.handlers.Add()`,
		`p.handlers.FooBar()`,
		`ErrParserUnbound`,
	)
}

// =============================================================================
// program.gen.tmpl
// =============================================================================

func TestRender_programGen(t *testing.T) {
	t.Parallel()
	in := internal.NewRenderInput("rotini", fixtureSpec())
	out := renderOK(t, "program.gen.tmpl", in)

	containsAll(t, "program.gen.tmpl", out,
		`package rotini`,
		`type Program struct`,
		`registry *rtk.Registry`,
		`handlers Handlers`,
		`func NewProgram(h Handlers) *Program`,
		`reg.Bind("io", rtk.NewIO())`,
		`reg.Bind("os", rtk.NewOS())`,
		`reg.Bind("signals", rtk.NewSignals())`,
		`reg.Bind("ticker", rtk.NewTicker())`,
		`func (p *Program) Registry() *rtk.Registry`,
		`func (p *Program) Execute() error`,
		`func (p *Program) ExecuteArgs(argv []string) error`,
		`rtk.NewParser(Spec, rtk.Inputs{`,
		`p.registry.Bind("parser", parser)`,
		`rtk.Tokenize(argv, Spec)`,
		`strings.Join(tok.CommandPath, "-")`,
		`build, ok := executors[path]`,
		`RunExecution(build(p))`,
		`ErrParserUnbound`,
	)
}

// =============================================================================
// Unknown-template error
// =============================================================================

func TestRender_unknownTemplate(t *testing.T) {
	t.Parallel()
	in := internal.NewRenderInput("rotini", fixtureSpec())
	_, err := internal.Render("does-not-exist.tmpl", in)
	if err == nil {
		t.Fatal("expected error for unknown template, got nil")
	}
}

// =============================================================================
// Smoke: deterministic ordering
// =============================================================================

// TestRender_deterministicOrdering verifies that rendering the same
// spec twice produces byte-identical output. Map iteration order in Go
// is randomized; the renderer must sort everywhere it iterates a map.
func TestRender_deterministicOrdering(t *testing.T) {
	t.Parallel()
	in := internal.NewRenderInput("rotini", fixtureSpec())
	for _, name := range []string{"spec.gen.tmpl", "render.gen.tmpl", "inputs.gen.tmpl", "handlers.gen.tmpl"} {
		a, err := internal.Render(name, in)
		if err != nil {
			t.Fatalf("first Render(%s): %v", name, err)
		}
		b, err := internal.Render(name, in)
		if err != nil {
			t.Fatalf("second Render(%s): %v", name, err)
		}
		if string(a) != string(b) {
			t.Errorf("%s: render is non-deterministic", name)
		}
	}
}
