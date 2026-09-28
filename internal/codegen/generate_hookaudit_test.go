package codegen

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The hook audit closes the one way a lifecycle hook can go wrong silently.
//
// Two of the three ways are already loud, and these tests pin that division of labour rather
// than re-testing the compiler: a hook that is MISSING and a hook whose SIGNATURE drifted both
// break the `var _ rotini.Handlers` assertion every stub carries. A hook whose NAME is
// misspelled does not — the embedded Default* still satisfies the interface — and that is what
// the audit is for.

// hookAuditFixture generates a two-command project in a temp module and returns a function that
// re-runs generate, returning its notices. The cmd package directory is returned so a test can
// edit the stub the way an author would.
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

// appendToStub adds declarations to a generated stub, the way an author implementing a hook
// does. The embeds are left in place: the stub's own comment says to implement a hook by
// "declaring a method with the same name", and never says to remove the no-op it replaces —
// which is exactly why a misspelled name is silent.
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

// TestHookAudit_reportsAMisspelledHook is the finding itself: the method compiles, satisfies
// Handlers, and never runs. The notice has to carry enough to act on without opening the file.
func TestHookAudit_reportsAMisspelledHook(t *testing.T) {
	cmdDir, gen := hookAuditFixture(t)
	appendToStub(t, cmdDir, "demo_build.go", `
func (*demoBuildHandlers) Prerun(ctx context.Context, rtx *rotini.Context) {}
`)
	notices := gen(t)
	if len(notices) != 1 {
		t.Fatalf("notices = %v, want exactly one", notices)
	}
	got := notices[0].Error()
	for _, want := range []string{
		"internal/cmd/demo/demo_build.go:", // where
		`method "Prerun"`,                  // what they wrote
		"demoBuildHandlers",                // on which type
		"will never run",                   // the consequence
		"rotini.DefaultPreRun",             // what runs instead
		`did you mean "PreRun"?`,           // the fix
	} {
		if !strings.Contains(got, want) {
			t.Errorf("notice is missing %q:\n%s", want, got)
		}
	}
}

// TestHookAudit_isQuietOnCorrectCode guards the audit against being noise. A warning that
// fires on working code gets ignored, and then the real one is ignored with it.
func TestHookAudit_isQuietOnCorrectCode(t *testing.T) {
	cmdDir, gen := hookAuditFixture(t)
	appendToStub(t, cmdDir, "demo_build.go", `
// The four hooks spelled correctly, with the embeds still in place — a method at depth 0
// wins over a promoted one, so this is the normal way to implement a hook.
func (*demoBuildHandlers) CascadingPreRun(ctx context.Context, rtx *rotini.Context)  {}
func (*demoBuildHandlers) PreRun(ctx context.Context, rtx *rotini.Context)           {}
func (*demoBuildHandlers) PostRun(ctx context.Context, rtx *rotini.Context)          {}
func (*demoBuildHandlers) CascadingPostRun(ctx context.Context, rtx *rotini.Context) {}

// Ordinary helpers on the same type. Prepare and Rerun are the near-miss bait: "Rerun" is
// within an edit distance of 2 of "Run", which is why Run is not an audited target.
func (*demoBuildHandlers) Prepare() error      { return nil }
func (*demoBuildHandlers) Rerun() error        { return nil }
func (*demoBuildHandlers) Row() string         { return "" }
func (*demoBuildHandlers) render() string      { return "" }
func (*demoBuildHandlers) loadConfig() error   { return nil }
`)
	if notices := gen(t); len(notices) != 0 {
		t.Errorf("correct code reported %v, want silence", notices)
	}
}

// TestHookAudit_ignoresNonHandlerTypes keeps the audit scoped to types that actually implement
// Handlers, found through the assertion. A helper type in the same package is not a handler,
// and a method on it is none of the audit's business.
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

// TestHookAudit_findsHooksInAnyFileOfThePackage: an author who puts the hook in its own file
// beside the stub has made the same mistake, so the audit reads the package, not one file.
func TestHookAudit_findsHooksInAnyFileOfThePackage(t *testing.T) {
	cmdDir, gen := hookAuditFixture(t)
	writeTestFile(t, cmdDir, "lifecycle.go", `package demo

import (
	"context"

	"github.com/go-rotini/rotini"
)

func (*demoBuildHandlers) PostRunn(ctx context.Context, rtx *rotini.Context) {}
`)
	notices := gen(t)
	if len(notices) != 1 || !strings.Contains(notices[0].Error(), "lifecycle.go") {
		t.Errorf("notices = %v, want one naming lifecycle.go", notices)
	}
}

// TestHookAudit_neverFailsTheGenerate: these are the author's files, and they are read at the
// one moment they are most likely to be mid-edit. A file that will not parse is skipped — the
// compiler is about to report it far better than the audit could — and generate still succeeds,
// because refusing to regenerate over a syntax error would be a worse bug than the one this
// step exists to catch.
func TestHookAudit_neverFailsTheGenerate(t *testing.T) {
	cmdDir, gen := hookAuditFixture(t)
	writeTestFile(t, cmdDir, "broken.go", "package demo\n\nfunc (*demoBuildHandlers) Prerun( {\n")
	if notices := gen(t); len(notices) != 0 {
		t.Errorf("an unparseable file reported %v, want it skipped", notices)
	}
	// And the pass still emitted: the generated file is present and current.
	if _, err := os.Stat(filepath.Join(cmdDir, "zz_demo.go")); err != nil {
		t.Errorf("generate did not emit while a handler file was unparseable: %v", err)
	}
}

// TestHookAudit_reportsEveryOffenderInOrder: warnings are sorted by file then line so two runs
// over the same tree read the same, which is what makes them diffable in CI.
func TestHookAudit_reportsEveryOffenderInOrder(t *testing.T) {
	cmdDir, gen := hookAuditFixture(t)
	appendToStub(t, cmdDir, "demo_build.go", `
func (*demoBuildHandlers) CascadingPreRunn(ctx context.Context, rtx *rotini.Context) {}
func (*demoBuildHandlers) postRun(ctx context.Context, rtx *rotini.Context)          {}
`)
	writeTestFile(t, cmdDir, "a_more.go", `package demo

import (
	"context"

	"github.com/go-rotini/rotini"
)

func (*demoBuildHandlers) PreRunn(ctx context.Context, rtx *rotini.Context) {}
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

// TestHookAudit_readsTheAssertionLoosely: an author may rewrite the assertion, and the runtime
// may be imported under an alias. Neither should switch the audit off silently.
func TestHookAudit_readsTheAssertionLoosely(t *testing.T) {
	cmdDir, gen := hookAuditFixture(t)
	writeTestFile(t, cmdDir, "aliased.go", `package demo

import (
	"context"

	rt "github.com/go-rotini/rotini"
)

var _ rt.Handlers = aliasedHandlers{}

type aliasedHandlers struct {
	rt.DefaultCascadingPreRun
	rt.DefaultPreRun
	rt.DefaultPostRun
	rt.DefaultCascadingPostRun
}

func (aliasedHandlers) Run(ctx context.Context, rtx *rt.Context) {}

func (aliasedHandlers) PostRunn(ctx context.Context, rtx *rt.Context) {}
`)
	notices := gen(t)
	if len(notices) != 1 || !strings.Contains(notices[0].Error(), "aliasedHandlers") {
		t.Errorf("notices = %v, want one naming aliasedHandlers — an aliased import must not disable the audit", notices)
	}
}

// A `handler: {import, convention}` package is the bring-your-own seam: one Handlers
// implementation, written by hand, shared by several CLIs. It is where a misspelled hook is
// least likely to be noticed — nobody regenerates it, and it has no stub to compare against —
// and for a while it was the one place the audit did not look.
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

// handlerPkg is the real shape: no `var _ rotini.Handlers` assertion, because the convention
// function's return type already proves the type implements it.
func handlerPkg(extra string) string {
	return `package check

import (
	"context"

	"github.com/go-rotini/rotini"
)

// Check is the convention the spec's handler: block names.
func Check() rotini.Handlers { return &handlers{} }

type handlers struct {
	rotini.DefaultHooks
}

func (*handlers) Run(ctx context.Context, rtx *rotini.Context) {}
` + extra
}

// TestHookAudit_reachesAHandlerPackage is the gap closed: the audit follows `handler:` into a
// package this module owns.
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

// TestHookAudit_findsHandlerTypesWithoutAnAssertion pins the mechanism that makes the above
// possible: a type is a handler if a function RETURNS it as rotini.Handlers, not only if an
// assertion names it. Without this the audit walks the package and finds nothing to check.
func TestHookAudit_findsHandlerTypesWithoutAnAssertion(t *testing.T) {
	dir, gen := handlerPkgFixture(t, handlerPkg(""))
	if notices := gen(t); len(notices) != 0 {
		t.Fatalf("a correct handler package reported %v", notices)
	}
	body, err := os.ReadFile(filepath.Join(dir, "check.go"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(body), "var _ rotini.Handlers") {
		t.Fatal("fixture no longer demonstrates the case: it carries an assertion")
	}
}

// TestHookAudit_isQuietOnACorrectHandlerPackage guards against the new reach becoming noise.
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

// TestHookAudit_skipsHandlerPackagesInOtherModules is the deliberate boundary: a dependency's
// source is not this tool's to lint, and resolving an import path outside this module would mean
// consulting the build list or the module cache.
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
