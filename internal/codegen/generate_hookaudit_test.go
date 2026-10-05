package codegen

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The hook audit reports misspelled hook names, which the `var _ rotini.Handler` assertion
// cannot catch because the embedded No* still satisfies the interface. Missing hooks and
// drifted signatures are left to the compiler.

// hookAuditFixture generates a two-command project in a temp module and returns the cmd
// package directory and a function that re-runs generate and returns its notices.
func hookAuditFixture(t *testing.T) (cmdDir string, gen func(*testing.T) []error) {
	t.Helper()
	dir := t.TempDir()
	writeTestFile(t, dir, "go.mod", "module example.com/demo\n\ngo 1.26\n")
	writeTestFile(t, dir, ".rotini.spec.yaml", "version: 0.0.0\ncommand:\n  name: demo\n  commands:\n    - name: build\n")
	writeTestFile(t, dir, ".rotini.conf.yaml", goldenConf)
	t.Chdir(dir)

	gen = func(t *testing.T) []error {
		t.Helper()
		var notices []error
		if err := NewProcessor("0.0.0").Generate(".rotini.spec.yaml", ".rotini.conf.yaml", false,
			func(string, error) {}, func(n []error) { notices = append(notices, n...) }); err != nil {
			t.Fatalf("Generate: %v", err)
		}
		return notices
	}
	gen(t)
	return filepath.Join(dir, "internal", "cmd", "demo"), gen
}

// appendToStub appends declarations to a generated stub, leaving its No* embeds in place as an
// author implementing a hook would.
func appendToStub(t *testing.T, cmdDir, file, decls string) {
	t.Helper()
	path := filepath.Join(cmdDir, file)
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(body, []byte("\n"+decls)...), 0o600); err != nil {
		t.Fatal(err)
	}
}

// TestHookAudit_reportsAMisspelledHook pins the notice for a misspelled hook and its actionable
// details.
func TestHookAudit_reportsAMisspelledHook(t *testing.T) {
	cmdDir, gen := hookAuditFixture(t)
	appendToStub(t, cmdDir, "demo_build.go", `
func (*demoBuildHandler) Prerun(ctx context.Context, rtx *rotini.Context) {}
`)
	notices := gen(t)
	if len(notices) != 1 {
		t.Fatalf("notices = %v, want exactly one", notices)
	}
	got := notices[0].Error()
	for _, want := range []string{
		"internal/cmd/demo/demo_build.go:", // where
		`method "Prerun"`,                  // what they wrote
		"demoBuildHandler",                 // on which type
		"will never run",                   // the consequence
		"rotini.NoPreRun",                  // what runs instead
		`did you mean "PreRun"?`,           // the fix
	} {
		if !strings.Contains(got, want) {
			t.Errorf("notice is missing %q:\n%s", want, got)
		}
	}
}

// TestHookAudit_isQuietOnCorrectCode pins silence for correctly spelled hooks and ordinary
// helper methods.
func TestHookAudit_isQuietOnCorrectCode(t *testing.T) {
	cmdDir, gen := hookAuditFixture(t)
	appendToStub(t, cmdDir, "demo_build.go", `
// The four hooks spelled correctly, with the embeds still in place — a method at depth 0
// wins over a promoted one, so this is the normal way to implement a hook.
func (*demoBuildHandler) CascadingPreRun(ctx context.Context, rtx *rotini.Context)  {}
func (*demoBuildHandler) PreRun(ctx context.Context, rtx *rotini.Context)           {}
func (*demoBuildHandler) PostRun(ctx context.Context, rtx *rotini.Context)          {}
func (*demoBuildHandler) CascadingPostRun(ctx context.Context, rtx *rotini.Context) {}

// Ordinary helpers on the same type. Prepare and Rerun are the near-miss bait: "Rerun" is
// within an edit distance of 2 of "Run", which is why Run is not an audited target.
func (*demoBuildHandler) Prepare() error      { return nil }
func (*demoBuildHandler) Rerun() error        { return nil }
func (*demoBuildHandler) Row() string         { return "" }
func (*demoBuildHandler) render() string      { return "" }
func (*demoBuildHandler) loadConfig() error   { return nil }
`)
	if notices := gen(t); len(notices) != 0 {
		t.Errorf("correct code reported %v, want silence", notices)
	}
}

// TestHookAudit_ignoresNonHandlerTypes pins that methods on non-handler types are not audited.
func TestHookAudit_ignoresNonHandlerTypes(t *testing.T) {
	cmdDir, gen := hookAuditFixture(t)
	writeTestFile(t, cmdDir, "helpers.go", `package demo

type migrator struct{}

func (migrator) Prerun() error  { return nil }
func (migrator) PostRunn() error { return nil }
`)
	if notices := gen(t); len(notices) != 0 {
		t.Errorf("a non-handler type reported %v, want silence", notices)
	}
}

// TestHookAudit_findsHooksInAnyFileOfThePackage pins that the audit reads every file in the
// package, not only the stub.
func TestHookAudit_findsHooksInAnyFileOfThePackage(t *testing.T) {
	cmdDir, gen := hookAuditFixture(t)
	writeTestFile(t, cmdDir, "lifecycle.go", `package demo

import (
	"context"

	"github.com/go-rotini/rotini"
)

func (*demoBuildHandler) PostRunn(ctx context.Context, rtx *rotini.Context) {}
`)
	notices := gen(t)
	if len(notices) != 1 || !strings.Contains(notices[0].Error(), "lifecycle.go") {
		t.Errorf("notices = %v, want one naming lifecycle.go", notices)
	}
}

// TestHookAudit_neverFailsTheGenerate pins that an unparseable handler file is skipped and
// generate still succeeds.
func TestHookAudit_neverFailsTheGenerate(t *testing.T) {
	cmdDir, gen := hookAuditFixture(t)
	writeTestFile(t, cmdDir, "broken.go", "package demo\n\nfunc (*demoBuildHandler) Prerun( {\n")
	if notices := gen(t); len(notices) != 0 {
		t.Errorf("an unparseable file reported %v, want it skipped", notices)
	}
	if _, err := os.Stat(filepath.Join(cmdDir, "zz_demo.go")); err != nil {
		t.Errorf("generate did not emit while a handler file was unparseable: %v", err)
	}
}

// TestHookAudit_reportsEveryOffenderInOrder pins that every offender is reported, sorted by
// file then line.
func TestHookAudit_reportsEveryOffenderInOrder(t *testing.T) {
	cmdDir, gen := hookAuditFixture(t)
	appendToStub(t, cmdDir, "demo_build.go", `
func (*demoBuildHandler) CascadingPreRunn(ctx context.Context, rtx *rotini.Context) {}
func (*demoBuildHandler) postRun(ctx context.Context, rtx *rotini.Context)          {}
`)
	writeTestFile(t, cmdDir, "a_more.go", `package demo

import (
	"context"

	"github.com/go-rotini/rotini"
)

func (*demoBuildHandler) PreRunn(ctx context.Context, rtx *rotini.Context) {}
`)
	notices := gen(t)
	if len(notices) != 3 {
		t.Fatalf("notices = %v, want three", notices)
	}
	// a_more.go sorts before demo_build.go; within the stub, by line.
	for i, want := range []string{"a_more.go", "demo_build.go", "demo_build.go"} {
		if !strings.Contains(notices[i].Error(), want) {
			t.Errorf("notice %d = %q, want it to name %s", i, notices[i], want)
		}
	}
	if !strings.Contains(notices[1].Error(), "CascadingPreRunn") || !strings.Contains(notices[2].Error(), "postRun") {
		t.Errorf("within one file the notices are out of line order: %v", notices[1:])
	}
}

// TestHookAudit_readsTheAssertionLoosely pins that a rewritten assertion or an aliased runtime
// import still identifies a handler type.
func TestHookAudit_readsTheAssertionLoosely(t *testing.T) {
	cmdDir, gen := hookAuditFixture(t)
	writeTestFile(t, cmdDir, "aliased.go", `package demo

import (
	"context"

	rt "github.com/go-rotini/rotini"
)

var _ rt.Handler = aliasedHandler{}

type aliasedHandler struct {
	rt.NoCascadingPreRun
	rt.NoPreRun
	rt.NoPostRun
	rt.NoCascadingPostRun
}

func (aliasedHandler) Run(ctx context.Context, rtx *rt.Context) {}

func (aliasedHandler) PostRunn(ctx context.Context, rtx *rt.Context) {}
`)
	notices := gen(t)
	if len(notices) != 1 || !strings.Contains(notices[0].Error(), "aliasedHandler") {
		t.Errorf("notices = %v, want one naming aliasedHandler — an aliased import must not disable the audit", notices)
	}
}

// handlerPkgSpec delegates the `check` command to a hand-written package through a
// `handler: {import, convention}` block.
const handlerPkgSpec = `version: 0.0.0
command:
  name: demo
  commands:
    - name: check
      handler:
        import: checkcmd example.com/demo/handlers/check
        convention: Check
`

// handlerPkgFixture generates a project whose `check` command delegates to a hand-written
// package, and returns that package's directory plus a re-generate function.
func handlerPkgFixture(t *testing.T, handlerBody string) (pkgDir string, gen func(*testing.T) []error) {
	t.Helper()
	dir := t.TempDir()
	writeTestFile(t, dir, "go.mod", "module example.com/demo\n\ngo 1.26\n")
	writeTestFile(t, dir, ".rotini.spec.yaml", handlerPkgSpec)
	writeTestFile(t, dir, ".rotini.conf.yaml", goldenConf)
	writeTestFile(t, dir, "handlers/check/check.go", handlerBody)
	t.Chdir(dir)

	gen = func(t *testing.T) []error {
		t.Helper()
		var notices []error
		if err := NewProcessor("0.0.0").Generate(".rotini.spec.yaml", ".rotini.conf.yaml", false,
			func(string, error) {}, func(n []error) { notices = append(notices, n...) }); err != nil {
			t.Fatalf("Generate: %v", err)
		}
		return notices
	}
	return filepath.Join(dir, "handlers", "check"), gen
}

// handlerPkg returns a hand-written handler package source with extra appended. Like real ones,
// it has no `var _ rotini.Handler` assertion; the convention function's return type suffices.
func handlerPkg(extra string) string {
	return `package check

import (
	"context"

	"github.com/go-rotini/rotini"
)

// Check is the convention the spec's handler: block names.
func Check() rotini.Handler { return &handlers{} }

type handlers struct {
	rotini.NoHooks
}

func (*handlers) Run(ctx context.Context, rtx *rotini.Context) {}
` + extra
}

// TestHookAudit_reachesAHandlerPackage pins that the audit follows `handler:` into an in-module
// package.
func TestHookAudit_reachesAHandlerPackage(t *testing.T) {
	_, gen := handlerPkgFixture(t, handlerPkg(`
func (*handlers) CascadingPrerun(ctx context.Context, rtx *rotini.Context) {}
`))
	notices := gen(t)
	if len(notices) != 1 {
		t.Fatalf("notices = %v, want one naming the handler package", notices)
	}
	got := notices[0].Error()
	for _, want := range []string{"handlers/check/check.go:", `method "CascadingPrerun"`, `did you mean "CascadingPreRun"?`} {
		if !strings.Contains(got, want) {
			t.Errorf("notice is missing %q:\n%s", want, got)
		}
	}
}

// TestHookAudit_findsHandlerTypesWithoutAnAssertion pins that the handler-package fixture has
// no assertion, so the audit must find its type through a function returning rotini.Handler.
func TestHookAudit_findsHandlerTypesWithoutAnAssertion(t *testing.T) {
	dir, gen := handlerPkgFixture(t, handlerPkg(""))
	if notices := gen(t); len(notices) != 0 {
		t.Fatalf("a correct handler package reported %v", notices)
	}
	body, err := os.ReadFile(filepath.Join(dir, "check.go"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(body), "var _ rotini.Handler") {
		t.Fatal("fixture no longer demonstrates the case: it carries an assertion")
	}
}

// TestHookAudit_isQuietOnACorrectHandlerPackage pins silence for a correct handler package.
func TestHookAudit_isQuietOnACorrectHandlerPackage(t *testing.T) {
	_, gen := handlerPkgFixture(t, handlerPkg(`
// A correctly spelled hook, and an ordinary helper.
func (*handlers) CascadingPreRun(ctx context.Context, rtx *rotini.Context) {}

func (*handlers) prepare() error { return nil }
`))
	if notices := gen(t); len(notices) != 0 {
		t.Errorf("a correct handler package reported %v, want silence", notices)
	}
}

// TestHookAudit_skipsHandlerPackagesInOtherModules pins that handler packages outside this
// module are not audited.
func TestHookAudit_skipsHandlerPackagesInOtherModules(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, dir, "go.mod", "module example.com/demo\n\ngo 1.26\n")
	writeTestFile(t, dir, ".rotini.spec.yaml", strings.Replace(handlerPkgSpec,
		"example.com/demo/handlers/check", "example.com/elsewhere/handlers/check", 1))
	writeTestFile(t, dir, ".rotini.conf.yaml", goldenConf)
	t.Chdir(dir)

	var notices []error
	if err := NewProcessor("0.0.0").Generate(".rotini.spec.yaml", ".rotini.conf.yaml", false,
		func(string, error) {}, func(n []error) { notices = append(notices, n...) }); err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if len(notices) != 0 {
		t.Errorf("an out-of-module handler package produced %v, want it skipped", notices)
	}
}

// The inputs audit reports a handler acquiring another command's generated inputs type, which
// binds to the running command's frame and reads it through the wrong shape.

const twoCommandSpec = `version: 0.0.0
command:
  name: demo
  flags:
    - name: trace
      summary: cascading, so it appears on every frame
      identifiers: [--trace]
      cascading: true
      schema: { type: bool }
  commands:
    - name: build
      summary: has its own inputs type
`

func inputsAuditFixture(t *testing.T, buildBody string) func(*testing.T) []error {
	t.Helper()
	dir := t.TempDir()
	writeTestFile(t, dir, "go.mod", "module example.com/demo\n\ngo 1.26\n")
	writeTestFile(t, dir, ".rotini.spec.yaml", twoCommandSpec)
	writeTestFile(t, dir, ".rotini.conf.yaml", goldenConf)
	t.Chdir(dir)

	gen := func(t *testing.T) []error {
		t.Helper()
		var notices []error
		if err := NewProcessor("0.0.0").Generate(".rotini.spec.yaml", ".rotini.conf.yaml", false,
			func(string, error) {}, func(n []error) { notices = append(notices, n...) }); err != nil {
			t.Fatalf("Generate: %v", err)
		}
		return notices
	}
	gen(t) // seed the stubs
	if buildBody != "" {
		writeTestFile(t, dir, "internal/cmd/demo/demo_build.go", buildBody)
	}
	return gen
}

func buildStub(collect string) string {
	return `package demo

import (
	"context"

	"github.com/go-rotini/rotini"
)

var _ rotini.Handler = (*demoBuildHandler)(nil)

type demoBuildHandler struct {
	rotini.NoHooks
}

func (*demoBuildHandler) Run(ctx context.Context, rtx *rotini.Context) {
	` + collect + `
}
`
}

// TestInputsAudit_reportsAnotherCommandsType pins the notice for acquiring a parent's inputs
// type.
func TestInputsAudit_reportsAnotherCommandsType(t *testing.T) {
	gen := inputsAuditFixture(t, buildStub(`in, err := rtx.Inputs[DemoInputs]()
	_, _ = in, err`))
	notices := gen(t)
	if len(notices) != 1 {
		t.Fatalf("notices = %v, want one", notices)
	}
	got := notices[0].Error()
	for _, want := range []string{"demo_build.go:", "Inputs", "demoBuildHandler", "DemoInputs", "DemoBuildInputs"} {
		if !strings.Contains(got, want) {
			t.Errorf("notice is missing %q:\n%s", want, got)
		}
	}
}

// TestInputsAudit_coversEveryAcquirer pins that every inputs-acquiring method is audited, not
// only rtx.Inputs.
func TestInputsAudit_coversEveryAcquirer(t *testing.T) {
	for _, fn := range []string{"Inputs", "InputsWithReport", "DefaultInputs", "ArgvInputs", "EnvInputs", "FileInputs", "StdinInputs"} {
		t.Run(fn, func(t *testing.T) {
			call := "v, err := rtx." + fn + "[DemoInputs]()\n\t_, _ = v, err"
			if fn == "InputsWithReport" {
				call = "v, r, err := rtx.InputsWithReport[DemoInputs]()\n\t_, _, _ = v, r, err"
			}
			gen := inputsAuditFixture(t, buildStub(call))
			if notices := gen(t); len(notices) != 1 {
				t.Errorf("%s: notices = %v, want one", fn, notices)
			}
		})
	}
}

// TestInputsAudit_isQuietOnTheOwnType pins silence for a handler acquiring its own inputs type.
func TestInputsAudit_isQuietOnTheOwnType(t *testing.T) {
	gen := inputsAuditFixture(t, buildStub(`in, err := rtx.Inputs[DemoBuildInputs]()
	_, _ = in, err`))
	if notices := gen(t); len(notices) != 0 {
		t.Errorf("a handler collecting its own type reported %v", notices)
	}
}

// TestInputsAudit_ignoresAHandWrittenStruct pins silence for a hand-written inputs struct, as a
// shared `handler:` package declares.
func TestInputsAudit_ignoresAHandWrittenStruct(t *testing.T) {
	gen := inputsAuditFixture(t, `package demo

import (
	"context"

	"github.com/go-rotini/rotini"
)

var _ rotini.Handler = (*demoBuildHandler)(nil)

type demoBuildHandler struct {
	rotini.NoHooks
}

// Own is this handler's own view of the command line, as a shared handler declares.
type Own struct {
	Host struct {
		Flags     struct{}
		Arguments struct{}
	}
}

func (*demoBuildHandler) Run(ctx context.Context, rtx *rotini.Context) {
	in, err := rtx.Inputs[Own]()
	_, _ = in, err
}
`)
	if notices := gen(t); len(notices) != 0 {
		t.Errorf("a hand-written inputs struct reported %v, want it ignored", notices)
	}
}
