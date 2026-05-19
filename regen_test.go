package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/go-rotini/rotini/internal"
)

// TestSelfRegenIsByteStable is the M4 self-hosting check: it runs
// internal.Run against rotini's own .rotini.spec.yaml and asserts
// that every committed *.gen.go file is byte-identical to what
// regeneration would produce now. Tracked drift means the renderer
// changed and the committed files need refreshing — either by
// running `go run . generate` locally, or via this test under
// -update.
//
// This is the "rotini regenerates itself" guarantee from
// .docs/ROTINI_PACKAGE_REQUIREMENTS.md §M4.5.
func TestSelfRegenIsByteStable(t *testing.T) {
	// Not parallel — uses t.Chdir (process-global cwd) and writes
	// to the repo's actual internal/ tree (then restores).
	repoRoot := repoRootDir(t)

	// Snapshot every committed .gen.go file before regen.
	tracked := snapshotGenFiles(t, repoRoot)

	t.Chdir(repoRoot)
	if _, err := internal.Run(internal.RunOptions{
		SpecPath: ".rotini.spec.yaml",
	}); err != nil {
		t.Fatalf("internal.Run: %v", err)
	}

	// Re-read and diff. Any drift fails the test loudly so CI catches
	// any "I changed a template but forgot to commit regen" situation.
	for path, want := range tracked {
		got, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read regenerated %s: %v", path, err)
		}
		if !bytes.Equal(got, want) {
			t.Errorf("%s: regenerated content differs from committed.\n"+
				"  Run `go run . generate` and commit the diff to fix.\n"+
				"  --- diff hint (first 200 bytes) ---\n"+
				"  want: %.200q\n"+
				"  got:  %.200q",
				path, want, got)
		}
	}
}

// repoRootDir returns the absolute path to the repo root. The test
// runs with cwd = repo root (the directory containing main.go and
// go.mod), so the path is simply the result of os.Getwd().
func repoRootDir(t *testing.T) string {
	t.Helper()
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	return cwd
}

// snapshotGenFiles reads every *.gen.go file under
// internal/rotini and internal/handlers and returns
// {path → bytes}. The map drives the post-regen comparison.
func snapshotGenFiles(t *testing.T, root string) map[string][]byte {
	t.Helper()
	out := make(map[string][]byte)
	for _, sub := range []string{"internal/rotini", "internal/handlers"} {
		dir := filepath.Join(root, sub)
		entries, err := os.ReadDir(dir)
		if err != nil {
			t.Fatalf("read %s: %v", dir, err)
		}
		for _, e := range entries {
			name := e.Name()
			if e.IsDir() || !isGenGoFile(name) {
				continue
			}
			path := filepath.Join(dir, name)
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read %s: %v", path, err)
			}
			out[path] = data
		}
	}
	if len(out) == 0 {
		t.Fatal("no .gen.go files found; the test fixture is broken")
	}
	return out
}

// isGenGoFile reports whether name matches *.gen.go (the convention
// every codegen template writes to).
func isGenGoFile(name string) bool {
	const suffix = ".gen.go"
	return len(name) > len(suffix) && name[len(name)-len(suffix):] == suffix
}
