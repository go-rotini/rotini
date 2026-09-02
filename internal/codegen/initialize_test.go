package codegen

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestInitialize_endToEnd is the guard the `rotini init` breakage slipped past: every
// other test stops at "the files were written and validate", which a scaffold that
// cannot COMPILE still passes. This one runs the real initialize into a fresh module
// and then builds the result, so a bad seed conf, a stale template, a missing require,
// or a wrong runtime import path fails here rather than in a new user's terminal.
//
// It shells out to `go build`, so it is the slowest test in the package — but it covers
// the one path every single user walks first.
func TestInitialize_endToEnd(t *testing.T) {
	if testing.Short() {
		t.Skip("compiles a scaffolded module; skipped under -short")
	}

	// The scaffold imports github.com/go-rotini/rotini, which is THIS repo — two levels
	// up from internal/codegen. Resolve it before chdir'ing into the temp module.
	repoRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}

	dir := t.TempDir()
	writeTestFile(t, dir, "go.mod", "module example.com/demo\n\ngo 1.26\n\nrequire github.com/go-rotini/rotini v0.0.0\n\nreplace github.com/go-rotini/rotini => "+filepath.ToSlash(repoRoot)+"\n")
	t.Chdir(dir)

	if err := NewProcessor("0.0.0").Initialize("demo", "", false); err != nil {
		t.Fatalf("Initialize: %v", err)
	}

	// The scaffold's shape: the seed files, the entrypoint, the editable stub, and the
	// generated framework file. A missing one means a codegen step silently no-op'd.
	for _, want := range []string{
		"cmd/demo/.rotini.spec.yaml",
		"cmd/demo/.rotini.conf.yaml",
		"cmd/demo/main.go",
		"internal/cmd/demo/demo.go",
		"internal/cmd/demo/zz_rotini.go",
	} {
		if _, err := os.Stat(filepath.Join(dir, filepath.FromSlash(want))); err != nil {
			t.Errorf("scaffold missing %s: %v", want, err)
		}
	}

	// The runtime is IMPORTED, never emitted — no copy of it may appear in the module.
	for _, gone := range []string{"internal/cmd/demo/rotini", "internal/demo"} {
		if _, err := os.Stat(filepath.Join(dir, filepath.FromSlash(gone))); err == nil {
			t.Errorf("%s exists — the runtime must be imported, not emitted", gone)
		}
	}

	// `go mod tidy` resolves the runtime's own requirements (recon/fs/…) from the module
	// cache; the build is the real assertion.
	for _, args := range [][]string{{"mod", "tidy"}, {"build", "./..."}} {
		cmd := exec.Command("go", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("go %s in the scaffolded module: %v\n%s", strings.Join(args, " "), err, out)
		}
	}
}
