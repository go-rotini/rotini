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

// writeGoMod plants a minimal go.mod into dir so [internal.Initialize]
// can resolve the module path without an explicit override.
func writeGoMod(t *testing.T, dir, module string) {
	t.Helper()
	body := "module " + module + "\n\ngo 1.22\n"
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte(body), 0o600); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}
}

// readFile is os.ReadFile with t.Fatal on error and a string return.
func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(b)
}

// =============================================================================
// Happy path
// =============================================================================

func TestInitialize_writesThreeFiles(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeGoMod(t, dir, "example.com/me/myapp")

	res, err := internal.Initialize(internal.InitOptions{
		Name:          "todo",
		Dir:           dir,
		RotiniVersion: "1.2.3",
	})
	if err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	if len(res.FilesWritten) != 3 {
		t.Fatalf("FilesWritten: got %d, want 3 (%v)", len(res.FilesWritten), res.FilesWritten)
	}
	for _, p := range res.FilesWritten {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("expected file %s: %v", p, err)
		}
	}
}

func TestInitialize_specContent(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeGoMod(t, dir, "example.com/me/myapp")

	_, err := internal.Initialize(internal.InitOptions{
		Name:          "todo",
		Dir:           dir,
		RotiniVersion: "1.2.3",
	})
	if err != nil {
		t.Fatalf("Initialize: %v", err)
	}

	got := readFile(t, filepath.Join(dir, ".rotini.spec.yaml"))
	wants := []string{
		`$schema: https://raw.githubusercontent.com/matthewgetz/rotini/refs/tags/1.2.3/schema-spec.json`,
		`name: todo`,
		`commands: []`,
	}
	for _, w := range wants {
		if !strings.Contains(got, w) {
			t.Errorf("spec missing %q\n--- got ---\n%s", w, got)
		}
	}

	// Loadable + validates against the embedded schema.
	s, err := internal.LoadSpec(filepath.Join(dir, ".rotini.spec.yaml"))
	if err != nil {
		t.Fatalf("LoadSpec on generated file: %v", err)
	}
	if err := internal.Validate(s); err != nil {
		t.Fatalf("Validate on generated file: %v", err)
	}
}

func TestInitialize_confContent(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeGoMod(t, dir, "example.com/me/myapp")

	_, err := internal.Initialize(internal.InitOptions{
		Name:          "todo",
		Dir:           dir,
		RotiniVersion: "1.2.3",
	})
	if err != nil {
		t.Fatalf("Initialize: %v", err)
	}

	got := readFile(t, filepath.Join(dir, ".rotini.conf.yaml"))
	wants := []string{
		`$schema: https://raw.githubusercontent.com/matthewgetz/rotini/refs/tags/1.2.3/schema-conf.json`,
		`internal/cli/cmd`,
		`internal/cli/rotini`,
		`prune:`,
		`enabled: true`,
	}
	for _, w := range wants {
		if !strings.Contains(got, w) {
			t.Errorf("conf missing %q\n--- got ---\n%s", w, got)
		}
	}

	// Loadable + validates against the embedded conf schema.
	c, err := internal.LoadConf(filepath.Join(dir, ".rotini.conf.yaml"))
	if err != nil {
		t.Fatalf("LoadConf on generated file: %v", err)
	}
	if err := internal.ValidateConf(c); err != nil {
		t.Fatalf("ValidateConf on generated file: %v", err)
	}
}

func TestInitialize_mainContent(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeGoMod(t, dir, "example.com/me/myapp")

	_, err := internal.Initialize(internal.InitOptions{
		Name:          "todo",
		Dir:           dir,
		RotiniVersion: "1.2.3",
	})
	if err != nil {
		t.Fatalf("Initialize: %v", err)
	}

	got := readFile(t, filepath.Join(dir, "main.go"))
	wants := []string{
		`//go:generate go tool github.com/go-rotini/rotini generate`,
		`package main`,
		`"example.com/me/myapp/internal/cli/cmd"`,
		`"example.com/me/myapp/internal/cli/rotini"`,
		`os.Exit(rotini.ExitCode(cmd.Program.Execute()))`,
	}
	for _, w := range wants {
		if !strings.Contains(got, w) {
			t.Errorf("main.go missing %q\n--- got ---\n%s", w, got)
		}
	}
}

// =============================================================================
// Force vs no-Force
// =============================================================================

func TestInitialize_refusesToOverwriteByDefault(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeGoMod(t, dir, "example.com/me/myapp")
	// Pre-create one of the targets.
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte("// existing\n"), 0o600); err != nil {
		t.Fatalf("seed main.go: %v", err)
	}

	_, err := internal.Initialize(internal.InitOptions{
		Name:          "todo",
		Dir:           dir,
		RotiniVersion: "1.2.3",
	})
	if err == nil {
		t.Fatal("expected ErrAlreadyExists, got nil")
	}
	if !errors.Is(err, internal.ErrAlreadyExists) {
		t.Errorf("got %v, want errors.Is(ErrAlreadyExists)=true", err)
	}
	// The pre-existing file should be untouched.
	if got := readFile(t, filepath.Join(dir, "main.go")); got != "// existing\n" {
		t.Errorf("main.go got rewritten despite ErrAlreadyExists: %q", got)
	}
}

func TestInitialize_forceOverwrites(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeGoMod(t, dir, "example.com/me/myapp")
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte("// existing\n"), 0o600); err != nil {
		t.Fatalf("seed main.go: %v", err)
	}

	_, err := internal.Initialize(internal.InitOptions{
		Name:          "todo",
		Dir:           dir,
		RotiniVersion: "1.2.3",
		Force:         true,
	})
	if err != nil {
		t.Fatalf("Initialize with Force: %v", err)
	}
	got := readFile(t, filepath.Join(dir, "main.go"))
	if strings.Contains(got, "// existing") {
		t.Error("main.go retained pre-existing content despite Force=true")
	}
	if !strings.Contains(got, "package main") {
		t.Errorf("main.go missing expected content; got:\n%s", got)
	}
}

// =============================================================================
// Module path resolution
// =============================================================================

func TestInitialize_explicitModulePathWins(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeGoMod(t, dir, "wrong.example.com/me/myapp") // would be picked if override was missing

	_, err := internal.Initialize(internal.InitOptions{
		Name:          "todo",
		Dir:           dir,
		ModulePath:    "right.example.com/me/myapp",
		RotiniVersion: "1.2.3",
	})
	if err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	got := readFile(t, filepath.Join(dir, "main.go"))
	if !strings.Contains(got, `"right.example.com/me/myapp/internal/cli/cmd"`) {
		t.Errorf("main.go did not use explicit ModulePath; got:\n%s", got)
	}
}

func TestInitialize_walksAncestorForGoMod(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeGoMod(t, root, "ancestor.example.com/me/myapp")
	nested := filepath.Join(root, "a", "b", "c")
	if err := os.MkdirAll(nested, 0o750); err != nil {
		t.Fatalf("mkdir nested: %v", err)
	}

	_, err := internal.Initialize(internal.InitOptions{
		Name:          "todo",
		Dir:           nested,
		RotiniVersion: "1.2.3",
	})
	if err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	got := readFile(t, filepath.Join(nested, "main.go"))
	if !strings.Contains(got, `"ancestor.example.com/me/myapp/internal/cli/cmd"`) {
		t.Errorf("module path walk failed; got:\n%s", got)
	}
}

func TestInitialize_missingGoModReturnsErrModulePathUnknown(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	// Walking will eventually hit "/" without finding go.mod.
	_, err := internal.Initialize(internal.InitOptions{
		Name:          "todo",
		Dir:           dir,
		RotiniVersion: "1.2.3",
	})
	if err == nil {
		t.Fatal("expected ErrModulePathUnknown, got nil")
	}
	if !errors.Is(err, internal.ErrModulePathUnknown) {
		t.Errorf("got %v, want errors.Is(ErrModulePathUnknown)=true", err)
	}
}

// =============================================================================
// Name validation
// =============================================================================

func TestInitialize_emptyNameReturnsSentinel(t *testing.T) {
	t.Parallel()
	_, err := internal.Initialize(internal.InitOptions{})
	if !errors.Is(err, internal.ErrMissingInitName) {
		t.Errorf("got %v, want errors.Is(ErrMissingInitName)=true", err)
	}
}

func TestInitialize_invalidNameReturnsSentinel(t *testing.T) {
	t.Parallel()
	cases := []string{
		"has spaces",
		"123starts-with-digit",
		"has/slash",
		".dotfile",
	}
	for _, name := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			_, err := internal.Initialize(internal.InitOptions{Name: name})
			if !errors.Is(err, internal.ErrInvalidInitName) {
				t.Errorf("name %q: got %v, want errors.Is(ErrInvalidInitName)=true", name, err)
			}
		})
	}
}

// =============================================================================
// Version defaulting
// =============================================================================

func TestInitialize_unsetVersionUsesPlaceholder(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeGoMod(t, dir, "example.com/me/myapp")

	_, err := internal.Initialize(internal.InitOptions{
		Name: "todo",
		Dir:  dir,
		// RotiniVersion: ""  — defaulted
	})
	if err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	got := readFile(t, filepath.Join(dir, ".rotini.spec.yaml"))
	if !strings.Contains(got, `/refs/tags/0.0.0/schema-spec.json`) {
		t.Errorf("spec did not use 0.0.0 placeholder; got:\n%s", got)
	}
}
