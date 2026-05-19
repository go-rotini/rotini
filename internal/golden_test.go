package internal_test

import (
	"flag"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/go-rotini/rotini/internal"
)

// updateGolden toggles regeneration mode: when set, the test rewrites
// every scenario's expected/ tree to match the current renderer output.
// Usage:
//
//	go test ./internal -run TestGolden -update
//
// Commit the resulting changes to internal/testdata/golden/ after
// reviewing the diff. Without -update, the test only reads expected/
// and compares.
var updateGolden = flag.Bool("update", false, "rewrite testdata/golden/*/expected/ from current renderer output")

// TestGolden walks every scenario under internal/testdata/golden, runs
// internal.Run against the scenario's spec.yaml (+ optional conf.yaml),
// and byte-compares the rendered output to the checked-in expected/
// tree.
//
// Each scenario directory layout:
//
//	internal/testdata/golden/<name>/
//	  spec.yaml        — input
//	  conf.yaml        — optional input
//	  expected/        — expected emitted file tree
//
// Scenarios produce identical-bytes output across runs (the renderer
// is deterministic), so the byte-compare is a real regression check —
// any whitespace, ordering, or content drift fails the test.
func TestGolden(t *testing.T) {
	// Not parallel — uses `-update` flag which mutates the workspace.
	scenarios := listGoldenScenarios(t)
	if len(scenarios) == 0 {
		t.Fatal("no golden scenarios found under internal/testdata/golden")
	}
	for _, scenario := range scenarios {
		t.Run(scenario, func(t *testing.T) {
			runGoldenScenario(t, scenario)
		})
	}
}

func runGoldenScenario(t *testing.T, scenario string) {
	t.Helper()
	root := filepath.Join("testdata", "golden", scenario)
	specPath := filepath.Join(root, "spec.yaml")
	if _, err := os.Stat(specPath); err != nil {
		t.Fatalf("scenario %q: spec.yaml missing: %v", scenario, err)
	}

	outDir := t.TempDir()
	confPath := filepath.Join(root, "conf.yaml")
	opts := internal.RunOptions{
		SpecPath:  specPath,
		OutputDir: outDir,
		Package:   "rotini",
	}
	if _, err := os.Stat(confPath); err == nil {
		opts.ConfPath = confPath
	}

	if _, err := internal.Run(opts); err != nil {
		t.Fatalf("Run: %v", err)
	}

	expectedDir := filepath.Join(root, "expected")
	if *updateGolden {
		if err := replaceTree(expectedDir, outDir); err != nil {
			t.Fatalf("update expected tree: %v", err)
		}
		t.Logf("updated %s", expectedDir)
		return
	}

	compareTrees(t, expectedDir, outDir)
}

// listGoldenScenarios returns the sorted names of every directory
// under internal/testdata/golden. Used by TestGolden to discover
// scenarios automatically — adding a new scenario only requires
// dropping a directory under that path.
func listGoldenScenarios(t *testing.T) []string {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join("testdata", "golden"))
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		t.Fatalf("read golden root: %v", err)
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	return names
}

// compareTrees fails the test when expectedDir and actualDir differ
// in their tree shape or any file's content. Errors aggregate so a
// single run surfaces every drift point rather than just the first.
func compareTrees(t *testing.T, expectedDir, actualDir string) {
	t.Helper()
	expected, err := collectFiles(expectedDir)
	if err != nil {
		t.Fatalf("read expected dir: %v\n  hint: run `go test ./internal -run TestGolden -update`", err)
	}
	actual, err := collectFiles(actualDir)
	if err != nil {
		t.Fatalf("read actual dir: %v", err)
	}

	// Surface missing files.
	for rel := range expected {
		if _, ok := actual[rel]; !ok {
			t.Errorf("file missing from output: %s", rel)
		}
	}
	// Surface unexpected files.
	for rel := range actual {
		if _, ok := expected[rel]; !ok {
			t.Errorf("unexpected file in output: %s", rel)
		}
	}
	// Compare contents for files in both sides.
	for rel, wantBytes := range expected {
		gotBytes, ok := actual[rel]
		if !ok {
			continue
		}
		if string(gotBytes) != string(wantBytes) {
			t.Errorf("%s: content differs\n--- want ---\n%s\n--- got ---\n%s",
				rel, string(wantBytes), string(gotBytes))
		}
	}
}

// collectFiles walks root and returns a map of relative-path → file
// bytes. Subdirectories are flattened into their slash-joined relative
// paths. Symlinks are not followed.
func collectFiles(root string) (map[string][]byte, error) {
	out := make(map[string][]byte)
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		out[filepath.ToSlash(rel)] = data
		return nil
	})
	return out, err
}

// replaceTree removes dst and recreates it as a deep copy of src. Used
// by -update mode to rewrite the expected/ tree atomically.
func replaceTree(dst, src string) error {
	if err := os.RemoveAll(dst); err != nil {
		return err
	}
	if err := os.MkdirAll(dst, 0o750); err != nil {
		return err
	}
	return filepath.WalkDir(src, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == src {
			return nil
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o750)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(target, data, 0o600)
	})
}

// TestGoldenScenarioNamesArePortable asserts that every scenario
// directory under testdata/golden uses an ASCII-only
// alphanumeric+hyphen basename. This keeps the directory portable
// across case-insensitive filesystems and avoids surprises with shell
// glob expansion in CI scripts.
func TestGoldenScenarioNamesArePortable(t *testing.T) {
	t.Parallel()
	for _, name := range listGoldenScenarios(t) {
		for _, r := range name {
			ok := (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-'
			if !ok {
				t.Errorf("scenario %q: rune %q is not [a-z0-9-]", name, r)
			}
		}
	}
	// Sanity-touch strings.HasPrefix so casual maintenance doesn't have
	// to chase a stale import. The function is otherwise unused in
	// this file once the init() sanity-check was lifted to a test.
	_ = strings.HasPrefix
}
