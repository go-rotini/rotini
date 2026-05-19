package internal_test

import (
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/go-rotini/rotini/internal"
)

// =============================================================================
// Helpers
// =============================================================================

// pruneFixtureSpec mirrors runFixtureSpec but lives in this file so the
// two test suites can evolve independently. Three commands: "add",
// "foo", "foo bar" — handler files expected: root.go, add.go, foo.go,
// foo_bar.go.
const pruneFixtureSpec = `$schema: https://raw.githubusercontent.com/matthewgetz/rotini/refs/tags/1.2.3/schema-spec.json
name: todo
commands:
  - name: add
  - name: foo
    commands:
      - name: bar
`

// writePruneFixture lays out a spec file plus a handlers directory
// pre-populated with the supplied basenames. The returned tuple is
// (specPath, handlersDir).
func writePruneFixture(t *testing.T, presentFiles []string) (specPath, handlersDir string) {
	t.Helper()
	root := t.TempDir()
	specPath = filepath.Join(root, "rotini.spec.yaml")
	if err := os.WriteFile(specPath, []byte(pruneFixtureSpec), 0o600); err != nil {
		t.Fatalf("write spec: %v", err)
	}
	handlersDir = filepath.Join(root, "handlers")
	if err := os.MkdirAll(handlersDir, 0o750); err != nil {
		t.Fatalf("mkdir handlers: %v", err)
	}
	for _, f := range presentFiles {
		path := filepath.Join(handlersDir, f)
		if err := os.WriteFile(path, []byte("package handlers\n"), 0o600); err != nil {
			t.Fatalf("write %s: %v", f, err)
		}
	}
	return specPath, handlersDir
}

// basenames extracts the basename of every entry in paths and returns
// the result sorted. Tests assert on basenames since absolute paths
// vary per t.TempDir.
func basenames(paths []string) []string {
	out := make([]string, len(paths))
	for i, p := range paths {
		out[i] = filepath.Base(p)
	}
	sort.Strings(out)
	return out
}

// equalStringsSlice reports whether two string slices are
// element-wise equal.
func equalStringsSlice(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// =============================================================================
// HandlerFilename — convention
// =============================================================================

func TestHandlerFilename(t *testing.T) {
	t.Parallel()
	cases := []struct {
		path string
		want string
	}{
		{"", "root.go"},
		{"add", "add.go"},
		{"foo-bar", "foo_bar.go"},
		{"foo-bar-baz", "foo_bar_baz.go"},
	}
	for _, c := range cases {
		if got := internal.HandlerFilename(c.path); got != c.want {
			t.Errorf("HandlerFilename(%q): got %q, want %q", c.path, got, c.want)
		}
	}
}

// =============================================================================
// Prune — happy paths
// =============================================================================

func TestPrune_deletesStaleFile(t *testing.T) {
	t.Parallel()
	// Pre-populate handlers/ with every expected file plus one stale
	// file ("removed_cmd.go") that no longer corresponds to a command
	// in the spec.
	specPath, handlersDir := writePruneFixture(t, []string{
		"root.go",
		"add.go",
		"foo.go",
		"foo_bar.go",
		"removed_cmd.go",
	})
	res, err := internal.Prune(internal.PruneOptions{
		SpecPath:    specPath,
		HandlersDir: handlersDir,
	})
	if err != nil {
		t.Fatalf("Prune: %v", err)
	}
	if got := basenames(res.FilesDeleted); !equalStringsSlice(got, []string{"removed_cmd.go"}) {
		t.Errorf("FilesDeleted: got %v, want [removed_cmd.go]", got)
	}
	if got := basenames(res.FilesKept); !equalStringsSlice(got, []string{"add.go", "foo.go", "foo_bar.go", "root.go"}) {
		t.Errorf("FilesKept: got %v, want [add.go foo.go foo_bar.go root.go]", got)
	}
	// Verify the deletion actually happened.
	if _, err := os.Stat(filepath.Join(handlersDir, "removed_cmd.go")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("removed_cmd.go: stat err = %v, want os.ErrNotExist", err)
	}
}

func TestPrune_keepRetainsSafelistedFile(t *testing.T) {
	t.Parallel()
	specPath, handlersDir := writePruneFixture(t, []string{
		"root.go",
		"add.go",
		"foo.go",
		"foo_bar.go",
		"my_helper.go", // stale BUT safelisted
	})
	res, err := internal.Prune(internal.PruneOptions{
		SpecPath:    specPath,
		HandlersDir: handlersDir,
		Keep:        []string{"my_helper.go"},
	})
	if err != nil {
		t.Fatalf("Prune: %v", err)
	}
	if len(res.FilesDeleted) != 0 {
		t.Errorf("FilesDeleted: got %v, want []", basenames(res.FilesDeleted))
	}
	// my_helper.go must still exist.
	if _, err := os.Stat(filepath.Join(handlersDir, "my_helper.go")); err != nil {
		t.Errorf("my_helper.go: %v", err)
	}
}

func TestPrune_dryRunDoesNotDelete(t *testing.T) {
	t.Parallel()
	specPath, handlersDir := writePruneFixture(t, []string{
		"root.go",
		"add.go",
		"foo.go",
		"foo_bar.go",
		"stale.go",
	})
	res, err := internal.Prune(internal.PruneOptions{
		SpecPath:    specPath,
		HandlersDir: handlersDir,
		DryRun:      true,
	})
	if err != nil {
		t.Fatalf("Prune: %v", err)
	}
	if got := basenames(res.FilesDeleted); !equalStringsSlice(got, []string{"stale.go"}) {
		t.Errorf("FilesDeleted: got %v, want [stale.go]", got)
	}
	// stale.go must still exist on disk.
	if _, err := os.Stat(filepath.Join(handlersDir, "stale.go")); err != nil {
		t.Errorf("dry run deleted stale.go: %v", err)
	}
}

// =============================================================================
// Prune — non-handler files left alone
// =============================================================================

func TestPrune_skipsNonGoFiles(t *testing.T) {
	t.Parallel()
	specPath, handlersDir := writePruneFixture(t, []string{
		"root.go",
		"add.go",
		"foo.go",
		"foo_bar.go",
		"README.md",  // non-Go
		".gitignore", // hidden, non-Go
		"data.yaml",  // non-Go
	})
	res, err := internal.Prune(internal.PruneOptions{
		SpecPath:    specPath,
		HandlersDir: handlersDir,
	})
	if err != nil {
		t.Fatalf("Prune: %v", err)
	}
	if len(res.FilesDeleted) != 0 {
		t.Errorf("FilesDeleted: got %v, want []", basenames(res.FilesDeleted))
	}
	// All non-Go files must still exist.
	for _, f := range []string{"README.md", ".gitignore", "data.yaml"} {
		if _, err := os.Stat(filepath.Join(handlersDir, f)); err != nil {
			t.Errorf("non-Go file %s removed: %v", f, err)
		}
	}
}

func TestPrune_skipsGoFilesOutsideConvention(t *testing.T) {
	t.Parallel()
	// "foo-bar.go" uses a hyphen — not a generated-handler convention.
	// "_internal.go" starts with underscore — Go ignores it at build
	// time, so prune shouldn't claim it either.
	specPath, handlersDir := writePruneFixture(t, []string{
		"root.go",
		"add.go",
		"foo.go",
		"foo_bar.go",
		"foo-bar.go",  // hyphenated; not generated convention
		"123digit.go", // starts with digit; invalid Go identifier
	})
	res, err := internal.Prune(internal.PruneOptions{
		SpecPath:    specPath,
		HandlersDir: handlersDir,
	})
	if err != nil {
		t.Fatalf("Prune: %v", err)
	}
	if len(res.FilesDeleted) != 0 {
		t.Errorf("FilesDeleted: got %v, want [] (no conventional handler files were stale)", basenames(res.FilesDeleted))
	}
	for _, f := range []string{"foo-bar.go", "123digit.go"} {
		if _, err := os.Stat(filepath.Join(handlersDir, f)); err != nil {
			t.Errorf("convention-skipping file %s removed: %v", f, err)
		}
	}
}

// =============================================================================
// Prune — directory edge cases
// =============================================================================

func TestPrune_missingHandlersDirIsNoop(t *testing.T) {
	t.Parallel()
	specPath, _ := writePruneFixture(t, nil)
	// HandlersDir points at a sibling that doesn't exist; expected
	// behavior is "nothing to prune."
	handlersDir := filepath.Join(filepath.Dir(specPath), "does-not-exist")

	res, err := internal.Prune(internal.PruneOptions{
		SpecPath:    specPath,
		HandlersDir: handlersDir,
	})
	if err != nil {
		t.Fatalf("Prune: %v", err)
	}
	if len(res.FilesDeleted) != 0 || len(res.FilesKept) != 0 {
		t.Errorf("got non-empty result for missing dir: deleted=%v kept=%v",
			basenames(res.FilesDeleted), basenames(res.FilesKept))
	}
}

func TestPrune_emptyHandlersDir(t *testing.T) {
	t.Parallel()
	specPath, handlersDir := writePruneFixture(t, nil)
	res, err := internal.Prune(internal.PruneOptions{
		SpecPath:    specPath,
		HandlersDir: handlersDir,
	})
	if err != nil {
		t.Fatalf("Prune: %v", err)
	}
	if len(res.FilesDeleted) != 0 || len(res.FilesKept) != 0 {
		t.Errorf("empty dir produced non-empty result: %+v", res)
	}
}

func TestPrune_skipsSubdirectories(t *testing.T) {
	t.Parallel()
	specPath, handlersDir := writePruneFixture(t, []string{
		"root.go",
		"add.go",
		"foo.go",
		"foo_bar.go",
	})
	// Add a subdirectory that itself contains a "stale.go" file.
	// Prune is single-level, so the subdir is left alone.
	sub := filepath.Join(handlersDir, "internal_subpkg")
	if err := os.MkdirAll(sub, 0o750); err != nil {
		t.Fatalf("mkdir sub: %v", err)
	}
	if err := os.WriteFile(filepath.Join(sub, "stale.go"), []byte("package x\n"), 0o600); err != nil {
		t.Fatalf("write sub stale: %v", err)
	}
	res, err := internal.Prune(internal.PruneOptions{
		SpecPath:    specPath,
		HandlersDir: handlersDir,
	})
	if err != nil {
		t.Fatalf("Prune: %v", err)
	}
	if len(res.FilesDeleted) != 0 {
		t.Errorf("FilesDeleted: got %v, want []", basenames(res.FilesDeleted))
	}
	if _, err := os.Stat(filepath.Join(sub, "stale.go")); err != nil {
		t.Errorf("sub/stale.go: %v", err)
	}
}

// =============================================================================
// Idempotence
// =============================================================================

func TestPrune_isIdempotent(t *testing.T) {
	t.Parallel()
	specPath, handlersDir := writePruneFixture(t, []string{
		"root.go",
		"add.go",
		"foo.go",
		"foo_bar.go",
		"stale.go",
	})
	first, err := internal.Prune(internal.PruneOptions{
		SpecPath: specPath, HandlersDir: handlersDir,
	})
	if err != nil {
		t.Fatalf("first Prune: %v", err)
	}
	second, err := internal.Prune(internal.PruneOptions{
		SpecPath: specPath, HandlersDir: handlersDir,
	})
	if err != nil {
		t.Fatalf("second Prune: %v", err)
	}
	if got := basenames(first.FilesDeleted); !equalStringsSlice(got, []string{"stale.go"}) {
		t.Errorf("first run FilesDeleted: got %v, want [stale.go]", got)
	}
	if len(second.FilesDeleted) != 0 {
		t.Errorf("second run FilesDeleted: got %v, want []", basenames(second.FilesDeleted))
	}
	// FilesKept is identical across runs.
	if !equalStringsSlice(basenames(first.FilesKept), basenames(second.FilesKept)) {
		t.Errorf("FilesKept differs across runs: first=%v second=%v",
			basenames(first.FilesKept), basenames(second.FilesKept))
	}
}

// =============================================================================
// Error paths
// =============================================================================

func TestPrune_missingSpecPath(t *testing.T) {
	t.Parallel()
	_, err := internal.Prune(internal.PruneOptions{})
	if !errors.Is(err, internal.ErrMissingSpecPath) {
		t.Errorf("got %v, want errors.Is(ErrMissingSpecPath)=true", err)
	}
}

func TestPrune_missingHandlersDir(t *testing.T) {
	t.Parallel()
	specPath, _ := writePruneFixture(t, nil)
	_, err := internal.Prune(internal.PruneOptions{
		SpecPath: specPath,
	})
	if !errors.Is(err, internal.ErrMissingHandlersDir) {
		t.Errorf("got %v, want errors.Is(ErrMissingHandlersDir)=true", err)
	}
}

func TestPrune_invalidSpecPropagatesValidationError(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	specPath := filepath.Join(root, "spec.yaml")
	// Missing required $schema field; fails schema validation.
	if err := os.WriteFile(specPath, []byte("name: x\n"), 0o600); err != nil {
		t.Fatalf("write spec: %v", err)
	}
	handlersDir := filepath.Join(root, "handlers")
	if err := os.MkdirAll(handlersDir, 0o750); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	_, err := internal.Prune(internal.PruneOptions{
		SpecPath:    specPath,
		HandlersDir: handlersDir,
	})
	if !errors.Is(err, internal.ErrInvalidSpec) {
		t.Errorf("got %v, want errors.Is(ErrInvalidSpec)=true", err)
	}
}

// =============================================================================
// Composite scenario — exercises every branch in one run
// =============================================================================

func TestPrune_scenarioStaleActiveSafelistedNonGo(t *testing.T) {
	t.Parallel()
	specPath, handlersDir := writePruneFixture(t, []string{
		// Active (correspond to commands in the spec):
		"root.go",
		"add.go",
		"foo.go",
		"foo_bar.go",
		// Stale (no command in spec):
		"old_cmd.go",
		"another_stale.go",
		// Safelisted (stale by spec, but in Keep):
		"my_helper.go",
		// Non-Go (always left alone):
		"README.md",
	})
	res, err := internal.Prune(internal.PruneOptions{
		SpecPath:    specPath,
		HandlersDir: handlersDir,
		Keep:        []string{"my_helper.go"},
	})
	if err != nil {
		t.Fatalf("Prune: %v", err)
	}
	wantDeleted := []string{"another_stale.go", "old_cmd.go"}
	wantKept := []string{"add.go", "foo.go", "foo_bar.go", "my_helper.go", "root.go"}
	if got := basenames(res.FilesDeleted); !equalStringsSlice(got, wantDeleted) {
		t.Errorf("FilesDeleted: got %v, want %v", got, wantDeleted)
	}
	if got := basenames(res.FilesKept); !equalStringsSlice(got, wantKept) {
		t.Errorf("FilesKept: got %v, want %v", got, wantKept)
	}
	for _, f := range wantDeleted {
		if _, err := os.Stat(filepath.Join(handlersDir, f)); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("%s: not actually deleted (err=%v)", f, err)
		}
	}
	for _, f := range wantKept {
		if _, err := os.Stat(filepath.Join(handlersDir, f)); err != nil {
			t.Errorf("%s: should be kept (err=%v)", f, err)
		}
	}
	// README.md left untouched.
	if _, err := os.Stat(filepath.Join(handlersDir, "README.md")); err != nil {
		t.Errorf("README.md was deleted: %v", err)
	}
}

// =============================================================================
// Smoke: convention check
// =============================================================================

// TestPrune_paths verifies the expected-set calculation directly. Not
// exposed as a function — we infer behavior by observing what gets
// kept vs deleted against a known-population fixture.
func TestPrune_recognizesNestedCommandPaths(t *testing.T) {
	t.Parallel()
	// Spec with three-level nesting: foo -> bar -> baz.
	deepSpec := `$schema: https://raw.githubusercontent.com/matthewgetz/rotini/refs/tags/1.2.3/schema-spec.json
name: x
commands:
  - name: foo
    commands:
      - name: bar
        commands:
          - name: baz
`
	root := t.TempDir()
	specPath := filepath.Join(root, "spec.yaml")
	if err := os.WriteFile(specPath, []byte(deepSpec), 0o600); err != nil {
		t.Fatalf("write spec: %v", err)
	}
	handlersDir := filepath.Join(root, "handlers")
	if err := os.MkdirAll(handlersDir, 0o750); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	for _, f := range []string{"root.go", "foo.go", "foo_bar.go", "foo_bar_baz.go", "foo_bar_baz_quux.go"} {
		if err := os.WriteFile(filepath.Join(handlersDir, f), []byte("package x\n"), 0o600); err != nil {
			t.Fatalf("write %s: %v", f, err)
		}
	}
	res, err := internal.Prune(internal.PruneOptions{
		SpecPath:    specPath,
		HandlersDir: handlersDir,
	})
	if err != nil {
		t.Fatalf("Prune: %v", err)
	}
	if got := basenames(res.FilesDeleted); !equalStringsSlice(got, []string{"foo_bar_baz_quux.go"}) {
		t.Errorf("FilesDeleted: got %v, want [foo_bar_baz_quux.go]", got)
	}
	// Sanity: every legitimately-deep path was retained.
	keptNames := basenames(res.FilesKept)
	for _, want := range []string{"root.go", "foo.go", "foo_bar.go", "foo_bar_baz.go"} {
		found := false
		for _, n := range keptNames {
			if n == want {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("expected %q in FilesKept, got %v", want, keptNames)
		}
	}
	// And FilesKept has no surprises (4 expected handlers, plus none
	// from the deleted list).
	if len(keptNames) != 4 {
		t.Errorf("FilesKept count: got %d (%v), want 4", len(keptNames), keptNames)
	}
	// Defensive: deletion produces an absolute path under handlersDir.
	for _, p := range res.FilesDeleted {
		if !strings.HasPrefix(p, handlersDir) {
			t.Errorf("deleted path %q escapes handlersDir %q", p, handlersDir)
		}
	}
}
