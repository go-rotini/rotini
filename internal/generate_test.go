package internal_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-rotini/rotini/internal"
)

// =============================================================================
// Helpers
// =============================================================================

// runFixtureSpec is the on-disk spec content for integration tests. It
// covers a root flag, a single sub-command with a flag and required
// positional, and one nested command path so the rendered output
// exercises both the simple and the ancestor-composition paths.
const runFixtureSpec = `$schema: https://raw.githubusercontent.com/go-rotini/rotini/refs/tags/1.2.3/schemas/spec.json
name: todo
inputs:
  flags:
    - name: output
      identifiers: ["-o", "--output"]
      schema: {type: string, default: "table"}
commands:
  - name: add
    inputs:
      flags:
        - name: priority
          identifiers: ["-p"]
          schema: {type: integer, default: 3}
      arguments:
        - name: title
          schema: {type: string, required: true}
  - name: foo
    inputs:
      flags:
        - name: fopt
          identifiers: ["--fopt"]
          schema: {type: string}
    commands:
      - name: bar
        inputs:
          flags:
            - name: bopt
              identifiers: ["--bopt"]
              schema: {type: integer}
`

// writeRunFixture lays a spec file plus an empty output dir into a
// temp tree and returns spec path + output dir.
func writeRunFixture(t *testing.T) (specPath, outDir string) {
	t.Helper()
	root := t.TempDir()
	specPath = filepath.Join(root, "rotini.spec.yaml")
	if err := os.WriteFile(specPath, []byte(runFixtureSpec), 0o600); err != nil {
		t.Fatalf("write spec fixture: %v", err)
	}
	outDir = filepath.Join(root, "gen")
	return specPath, outDir
}

// =============================================================================
// Happy path: emit the consolidated framework file
// =============================================================================

// TestRun_emitsRotiniGenFile asserts the framework-package output is
// exactly one file (rotini.gen.go) per rotiniold convention and
// standard Go codegen practice. Anything else means the renderer
// regressed back to the broken per-template-per-file split.
func TestRun_emitsRotiniGenFile(t *testing.T) {
	t.Parallel()
	specPath, outDir := writeRunFixture(t)

	res, err := internal.Run(internal.RunOptions{
		SpecPath:  specPath,
		OutputDir: outDir,
		Package:   "rotini",
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	wantBasenames := []string{"rotini.gen.go"}
	gotBasenames := make([]string, len(res.FilesWritten))
	for i, p := range res.FilesWritten {
		gotBasenames[i] = filepath.Base(p)
	}
	if strings.Join(gotBasenames, ",") != strings.Join(wantBasenames, ",") {
		t.Errorf("FilesWritten basenames: got %v, want %v", gotBasenames, wantBasenames)
	}

	// The single file must declare the right package and import rtk.
	for _, p := range res.FilesWritten {
		data, err := os.ReadFile(p)
		if err != nil {
			t.Fatalf("read %s: %v", p, err)
		}
		got := string(data)
		if !strings.Contains(got, "package rotini") {
			t.Errorf("%s: missing `package rotini`", filepath.Base(p))
		}
		if !strings.Contains(got, "github.com/go-rotini/rotini/rtk") {
			t.Errorf("%s: missing rtk import", filepath.Base(p))
		}
	}
}

func TestRun_specContentReflectsInput(t *testing.T) {
	t.Parallel()
	specPath, outDir := writeRunFixture(t)

	_, err := internal.Run(internal.RunOptions{
		SpecPath:  specPath,
		OutputDir: outDir,
		Package:   "rotini",
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	specGo, err := os.ReadFile(filepath.Join(outDir, "rotini.gen.go"))
	if err != nil {
		t.Fatalf("read rotini.gen.go: %v", err)
	}
	got := string(specGo)
	wants := []string{
		`var Spec = rtk.ProgramSpec{`,
		`Name: "todo"`,
		`Name: "output"`,
		`Default: "table"`,
		`Name: "add"`,
		`Required: true`,
		`Path: "foo-bar"`,
	}
	for _, w := range wants {
		if !strings.Contains(got, w) {
			t.Errorf("rotini.gen.go missing %q", w)
		}
	}
}

// =============================================================================
// Atomicity: a second run overwrites prior content cleanly
// =============================================================================

func TestRun_isIdempotent(t *testing.T) {
	t.Parallel()
	specPath, outDir := writeRunFixture(t)

	first, err := internal.Run(internal.RunOptions{
		SpecPath: specPath, OutputDir: outDir, Package: "rotini",
	})
	if err != nil {
		t.Fatalf("first Run: %v", err)
	}
	second, err := internal.Run(internal.RunOptions{
		SpecPath: specPath, OutputDir: outDir, Package: "rotini",
	})
	if err != nil {
		t.Fatalf("second Run: %v", err)
	}
	if len(first.FilesWritten) != len(second.FilesWritten) {
		t.Errorf("file counts differ across runs: first=%d, second=%d", len(first.FilesWritten), len(second.FilesWritten))
	}
	// Byte-for-byte equality across runs: deterministic codegen.
	for i, p := range first.FilesWritten {
		a, _ := os.ReadFile(p)
		b, _ := os.ReadFile(second.FilesWritten[i])
		if string(a) != string(b) {
			t.Errorf("%s changed across runs", filepath.Base(p))
		}
	}
}

// =============================================================================
// OutputDir / Package derivation
// =============================================================================

func TestRun_packageDerivedFromOutputDirBasename(t *testing.T) {
	t.Parallel()
	specPath, _ := writeRunFixture(t)
	outDir := filepath.Join(filepath.Dir(specPath), "mycli")

	_, err := internal.Run(internal.RunOptions{
		SpecPath:  specPath,
		OutputDir: outDir,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	specGo, err := os.ReadFile(filepath.Join(outDir, "rotini.gen.go"))
	if err != nil {
		t.Fatalf("read rotini.gen.go: %v", err)
	}
	if !strings.Contains(string(specGo), "package mycli") {
		t.Errorf("rotini.gen.go: missing `package mycli` (got first line: %q)", firstLine(string(specGo)))
	}
}

func TestRun_explicitPackageOverridesBasename(t *testing.T) {
	t.Parallel()
	specPath, _ := writeRunFixture(t)
	outDir := filepath.Join(filepath.Dir(specPath), "anything")

	_, err := internal.Run(internal.RunOptions{
		SpecPath:  specPath,
		OutputDir: outDir,
		Package:   "explicit",
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	specGo, _ := os.ReadFile(filepath.Join(outDir, "rotini.gen.go"))
	if !strings.Contains(string(specGo), "package explicit") {
		t.Errorf("rotini.gen.go: missing explicit package override")
	}
}

// =============================================================================
// Conf-driven OutputDir
// =============================================================================

func TestRun_outputDirFromConf(t *testing.T) {
	// Not parallel — uses t.Chdir which mutates the process-wide cwd.
	specPath, _ := writeRunFixture(t)

	// Write a conf alongside the spec pointing at "fwgen".
	confPath := filepath.Join(filepath.Dir(specPath), ".rotini.conf.yaml")
	conf := `$schema: https://raw.githubusercontent.com/go-rotini/rotini/refs/tags/1.2.3/schemas/conf.json
generate:
  framework:
    package: fwgen
`
	if err := os.WriteFile(confPath, []byte(conf), 0o600); err != nil {
		t.Fatalf("write conf: %v", err)
	}

	// Run with no OutputDir/Package — should pick fwgen from conf.
	// t.Chdir scopes the working-directory change to the test lifetime.
	t.Chdir(filepath.Dir(specPath))

	_, err := internal.Run(internal.RunOptions{
		SpecPath: filepath.Base(specPath),
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if _, err := os.Stat("fwgen/rotini.gen.go"); err != nil {
		t.Errorf("fwgen/rotini.gen.go: %v", err)
	}
}

// =============================================================================
// Error paths
// =============================================================================

func TestRun_missingSpecPathReturnsSentinel(t *testing.T) {
	t.Parallel()
	_, err := internal.Run(internal.RunOptions{})
	if !errors.Is(err, internal.ErrMissingSpecPath) {
		t.Errorf("got %v, want errors.Is(ErrMissingSpecPath)=true", err)
	}
}

func TestRun_invalidSpecSurfacesValidationError(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	specPath := filepath.Join(root, "spec.yaml")
	// Missing required $schema field.
	if err := os.WriteFile(specPath, []byte("name: x\n"), 0o600); err != nil {
		t.Fatalf("write spec: %v", err)
	}
	_, err := internal.Run(internal.RunOptions{
		SpecPath:  specPath,
		OutputDir: filepath.Join(root, "out"),
	})
	if err == nil {
		t.Fatal("expected validation error, got nil")
	}
	if !errors.Is(err, internal.ErrInvalidSpec) {
		t.Errorf("err is not ErrInvalidSpec: %v", err)
	}
}

func TestRun_missingSpecFileReturnsError(t *testing.T) {
	t.Parallel()
	_, err := internal.Run(internal.RunOptions{
		SpecPath:  filepath.Join(t.TempDir(), "nope.yaml"),
		OutputDir: t.TempDir(),
	})
	if err == nil {
		t.Fatal("expected error for missing spec file, got nil")
	}
}

// =============================================================================
// handlers.go.tmpl + handler.go.tmpl emission
// =============================================================================

func TestRun_emitsBridgeAndSkelsWhenModulePathResolvable(t *testing.T) {
	t.Parallel()
	specPath, outDir := writeRunFixture(t)
	cmdDir := filepath.Join(filepath.Dir(specPath), "cmd")

	res, err := internal.Run(internal.RunOptions{
		SpecPath:   specPath,
		OutputDir:  outDir,
		Package:    "rotini",
		CmdDir:     cmdDir,
		CmdPackage: "cmd",
		ModulePath: "example.com/me/todo",
		ModuleRoot: filepath.Dir(specPath),
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	// Bridge + per-command skels should be in cmdDir.
	bridgePath := filepath.Join(cmdDir, "handlers.gen.go")
	if _, err := os.Stat(bridgePath); err != nil {
		t.Fatalf("bridge not written: %v", err)
	}
	bridge, err := os.ReadFile(bridgePath)
	if err != nil {
		t.Fatalf("read bridge: %v", err)
	}
	wantBridge := []string{
		`package cmd`,
		`rotini "example.com/me/todo/`, // framework import path
		`type handlers struct`,
		`type handlers struct`,
		`func (h *handlers) Root() rotini.RootHandler { return h.root }`,
		`func (h *handlers) Add() rotini.AddHandler`,
		`func (h *handlers) FooBar() rotini.FooBarHandler`,
		`var Program = rotini.NewProgram(&handlers{`,
		`&RootHandlerImpl{}`,
		`&AddHandlerImpl{}`,
		`&FooBarHandlerImpl{}`,
	}
	for _, w := range wantBridge {
		if !strings.Contains(string(bridge), w) {
			t.Errorf("bridge missing %q\n--- got ---\n%s", w, bridge)
		}
	}

	// Per-command stubs (one per path + root).
	wantStubs := []string{"todo.go", "todo_add.go", "todo_foo.go", "todo_foo_bar.go"}
	for _, s := range wantStubs {
		p := filepath.Join(cmdDir, s)
		if _, err := os.Stat(p); err != nil {
			t.Errorf("stub %s missing: %v", s, err)
			continue
		}
		content, err := os.ReadFile(p)
		if err != nil {
			t.Fatalf("read %s: %v", s, err)
		}
		got := string(content)
		if !strings.Contains(got, "package cmd") {
			t.Errorf("%s missing package cmd", s)
		}
		if !strings.Contains(got, `rotini "example.com/me/todo/`) {
			t.Errorf("%s missing rotini import", s)
		}
	}

	// Per-command type names match the convention.
	rootStub, _ := os.ReadFile(filepath.Join(cmdDir, "todo.go"))
	if !strings.Contains(string(rootStub), `type RootHandlerImpl struct{}`) {
		t.Errorf("todo.go missing RootHandlerImpl type")
	}
	fooBarStub, _ := os.ReadFile(filepath.Join(cmdDir, "todo_foo_bar.go"))
	if !strings.Contains(string(fooBarStub), `type FooBarHandlerImpl struct{}`) {
		t.Errorf("foo_bar.go missing FooBarHandlerImpl type")
	}
	if !strings.Contains(string(fooBarStub), `*rotini.FooBarInputs`) {
		t.Errorf("foo_bar.go missing FooBarInputs reference")
	}

	// FilesWritten lists exactly: 1 framework file (rotini.gen.go) +
	// 1 bridge file (handlers.gen.go) + 4 stubs (root + 3 cmds).
	if len(res.FilesWritten) != 6 {
		t.Errorf("FilesWritten count: got %d, want 6 (rotini.gen.go + handlers.gen.go + 4 stubs); files=%v",
			len(res.FilesWritten), res.FilesWritten)
	}
}

func TestRun_stubsAreWriteOnlyIfMissing(t *testing.T) {
	t.Parallel()
	specPath, outDir := writeRunFixture(t)
	cmdDir := filepath.Join(filepath.Dir(specPath), "cmd")

	// First run: writes everything fresh.
	if _, err := internal.Run(internal.RunOptions{
		SpecPath: specPath, OutputDir: outDir, Package: "rotini",
		CmdDir: cmdDir, CmdPackage: "cmd",
		ModulePath: "example.com/me/todo",
		ModuleRoot: filepath.Dir(specPath),
	}); err != nil {
		t.Fatalf("first Run: %v", err)
	}
	// Hand-edit add.go: replace the TODO with real implementation.
	addPath := filepath.Join(cmdDir, "todo_add.go")
	custom := "package cmd\n\n// user-authored content\n"
	if err := os.WriteFile(addPath, []byte(custom), 0o600); err != nil {
		t.Fatalf("rewrite add.go: %v", err)
	}

	// Second run: stubs should NOT be overwritten (write-only-if-missing).
	res2, err := internal.Run(internal.RunOptions{
		SpecPath: specPath, OutputDir: outDir, Package: "rotini",
		CmdDir: cmdDir, CmdPackage: "cmd",
		ModulePath: "example.com/me/todo",
		ModuleRoot: filepath.Dir(specPath),
	})
	if err != nil {
		t.Fatalf("second Run: %v", err)
	}
	got, err := os.ReadFile(addPath)
	if err != nil {
		t.Fatalf("read add.go: %v", err)
	}
	if string(got) != custom {
		t.Errorf("add.go was overwritten on second run despite write-only-if-missing rule:\n--- got ---\n%s\n--- want ---\n%s",
			got, custom)
	}
	// FilesSkipped should list every stub that already existed.
	wantSkipped := []string{"todo_add.go", "todo_foo.go", "todo_foo_bar.go", "todo.go"}
	for _, w := range wantSkipped {
		found := false
		for _, p := range res2.FilesSkipped {
			if filepath.Base(p) == w {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("FilesSkipped missing %q (got %v)", w, basenamesSlice(res2.FilesSkipped))
		}
	}
}

func TestRun_bridgeAlwaysOverwrites(t *testing.T) {
	t.Parallel()
	specPath, outDir := writeRunFixture(t)
	cmdDir := filepath.Join(filepath.Dir(specPath), "cmd")
	bridgePath := filepath.Join(cmdDir, "handlers.gen.go")

	// First run: writes a fresh bridge.
	if _, err := internal.Run(internal.RunOptions{
		SpecPath: specPath, OutputDir: outDir, Package: "rotini",
		CmdDir: cmdDir, CmdPackage: "cmd",
		ModulePath: "example.com/me/todo",
		ModuleRoot: filepath.Dir(specPath),
	}); err != nil {
		t.Fatalf("first Run: %v", err)
	}
	first, _ := os.ReadFile(bridgePath)

	// Hand-edit the bridge to verify it gets clobbered on next run.
	if err := os.WriteFile(bridgePath, []byte("// hand-edited!\n"), 0o600); err != nil {
		t.Fatalf("rewrite bridge: %v", err)
	}

	// Second run: bridge must be regenerated.
	if _, err := internal.Run(internal.RunOptions{
		SpecPath: specPath, OutputDir: outDir, Package: "rotini",
		CmdDir: cmdDir, CmdPackage: "cmd",
		ModulePath: "example.com/me/todo",
		ModuleRoot: filepath.Dir(specPath),
	}); err != nil {
		t.Fatalf("second Run: %v", err)
	}
	second, _ := os.ReadFile(bridgePath)

	if string(second) == "// hand-edited!\n" {
		t.Error("bridge retained hand-edit; expected regeneration")
	}
	if string(second) != string(first) {
		t.Error("bridge content differs across runs (non-deterministic)")
	}
}

func TestRun_skipBridgeOptionSuppressesEmission(t *testing.T) {
	t.Parallel()
	specPath, outDir := writeRunFixture(t)
	cmdDir := filepath.Join(filepath.Dir(specPath), "cmd")

	_, err := internal.Run(internal.RunOptions{
		SpecPath: specPath, OutputDir: outDir, Package: "rotini",
		CmdDir: cmdDir, CmdPackage: "cmd",
		ModulePath: "example.com/me/todo",
		ModuleRoot: filepath.Dir(specPath),
		SkipBridge: true,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if _, err := os.Stat(filepath.Join(cmdDir, "handlers.gen.go")); !os.IsNotExist(err) {
		t.Errorf("handlers.gen.go: want not-exist, got err=%v", err)
	}
}

func TestRun_missingModulePathSkipsBridge(t *testing.T) {
	t.Parallel()
	specPath, outDir := writeRunFixture(t)
	cmdDir := filepath.Join(filepath.Dir(specPath), "cmd")

	res, err := internal.Run(internal.RunOptions{
		SpecPath: specPath, OutputDir: outDir, Package: "rotini",
		CmdDir: cmdDir, CmdPackage: "cmd",
		// ModulePath: "" — go.mod walk fails inside a tempdir
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if _, err := os.Stat(filepath.Join(cmdDir, "handlers.gen.go")); !os.IsNotExist(err) {
		t.Errorf("handlers.gen.go: want not-exist, got err=%v", err)
	}
	foundSkip := false
	for _, p := range res.FilesSkipped {
		if strings.Contains(p, "handlers.gen.go") {
			foundSkip = true
			break
		}
	}
	if !foundSkip {
		t.Errorf("FilesSkipped: did not mention bridge (%v)", res.FilesSkipped)
	}
}

// basenamesSlice is a local helper extracting basenames from a slice
// of full paths for error-message rendering. The prune_test.go file
// has a `basenames` helper but that test file is part of the same
// package — duplicating here avoids a tangle of cross-test imports.
func basenamesSlice(paths []string) []string {
	out := make([]string, len(paths))
	for i, p := range paths {
		out[i] = filepath.Base(p)
	}
	return out
}

// =============================================================================
// Helpers
// =============================================================================

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}
