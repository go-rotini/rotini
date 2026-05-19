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
const runFixtureSpec = `$schema: https://raw.githubusercontent.com/matthewgetz/rotini/refs/tags/1.2.3/schema-spec.json
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
// Happy path: emit foundation templates
// =============================================================================

func TestRun_emitsFoundationFiles(t *testing.T) {
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

	wantBasenames := []string{
		"executors.gen.go",
		"handlers.gen.go",
		"inputs.gen.go",
		"lifecycle.gen.go",
		"program.gen.go",
		"render.gen.go",
		"spec.gen.go",
	}
	gotBasenames := make([]string, len(res.FilesWritten))
	for i, p := range res.FilesWritten {
		gotBasenames[i] = filepath.Base(p)
	}
	if strings.Join(gotBasenames, ",") != strings.Join(wantBasenames, ",") {
		t.Errorf("FilesWritten basenames: got %v, want %v", gotBasenames, wantBasenames)
	}

	// Verify each file is non-empty, declares the right package,
	// and references rtk.
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

	specGo, err := os.ReadFile(filepath.Join(outDir, "spec.gen.go"))
	if err != nil {
		t.Fatalf("read spec.gen.go: %v", err)
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
			t.Errorf("spec.gen.go missing %q", w)
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
	specGo, err := os.ReadFile(filepath.Join(outDir, "spec.gen.go"))
	if err != nil {
		t.Fatalf("read spec.gen.go: %v", err)
	}
	if !strings.Contains(string(specGo), "package mycli") {
		t.Errorf("spec.gen.go: missing `package mycli` (got first line: %q)", firstLine(string(specGo)))
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
	specGo, _ := os.ReadFile(filepath.Join(outDir, "spec.gen.go"))
	if !strings.Contains(string(specGo), "package explicit") {
		t.Errorf("spec.gen.go: missing explicit package override")
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
	conf := `$schema: https://raw.githubusercontent.com/matthewgetz/rotini/refs/tags/1.2.3/schema-conf.json
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
	if _, err := os.Stat("fwgen/spec.gen.go"); err != nil {
		t.Errorf("fwgen/spec.gen.go: %v", err)
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
// Helpers
// =============================================================================

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}
