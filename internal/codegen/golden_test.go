package codegen

import (
	"flag"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// updateGolden regenerates the golden snapshots from the current emit output
// (mirrors the runtime's -update convention): `go test ./internal/codegen -run Golden -update`.
var updateGolden = flag.Bool("update", false, "update generate golden files")

// goldenSpec / goldenConf are a deliberately feature-spanning CLI: root flags +
// env, a sub-command with an argument + flag, so the emitted cmd file exercises
// the input-struct, Definition, BindMeta, rollup, and stub paths in one pass. This
// is the behavior-preserving safety net for the whole codegen refactor — the emitted
// bytes must not change across any structural move.
const goldenSpec = `version: 0.0.0
command:
  name: demo
  description: a demo cli
  flags:
    - name: verbose
      summary: verbose output
      identifiers: [--verbose, -v]
      schema: {type: bool}
  env:
    - name: home
      summary: home dir
      schema: {type: string, variable: DEMO_HOME}
  commands:
    - name: build
      summary: build the target
      arguments:
        - name: target
          summary: what to build
          schema: {type: string}
      flags:
        - name: out
          summary: output path
          identifiers: [--out, -o]
          schema: {type: string}
`

const goldenConf = `version: 0.0.0
generate:
  packages:
    - type: main
      file: cmd/demo/main.go
      package: main
    - type: cmd
      file: internal/cmd/demo/zz_demo.go
      package: demo
    - type: runtime
      file: internal/demo/rotini/zz_runtime.go
      package: rotini
`

// runtimeRel is the module-relative path of the emitted merged runtime — snapshotted
// only as a parse-check (it is the whole runtime, ~7k lines; byte-snapshotting it would
// make every unrelated runtime edit churn this golden).
const runtimeRel = "internal/demo/rotini/zz_runtime.go"

// emitInModule writes spec+conf into a fresh temp module, runs the generate pass with
// the working directory set there, and returns every emitted .go file keyed by its
// module-relative slash path.
func emitInModule(t *testing.T, spec, conf string) map[string]string {
	t.Helper()
	dir := t.TempDir()
	writeTestFile(t, dir, "go.mod", "module example.com/demo\n\ngo 1.26\n")
	writeTestFile(t, dir, ".rotini.spec.yaml", spec)
	writeTestFile(t, dir, ".rotini.conf.yaml", conf)
	t.Chdir(dir)

	var cbErr error
	if err := NewProcessor("0.0.0").Generate(".rotini.spec.yaml", ".rotini.conf.yaml", false, func(_ string, e error) {
		if e != nil {
			cbErr = e
		}
	}); err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if cbErr != nil {
		t.Fatalf("Generate reported: %v", cbErr)
	}
	return collectGoFiles(t, dir)
}

func collectGoFiles(t *testing.T, root string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(p, ".go") {
			return nil
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		out[filepath.ToSlash(rel)] = string(b)
		return nil
	})
	if err != nil {
		t.Fatalf("walk emitted tree: %v", err)
	}
	return out
}

func writeTestFile(t *testing.T, dir, name, content string) {
	t.Helper()
	p := filepath.Join(dir, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

// TestGenerateGolden pins the emitted output. The merged runtime file is parse-checked
// (correct package, parses) rather than byte-snapshotted; every other emitted .go file
// is byte-compared against testdata/golden. Run with -update to refresh.
func TestGenerateGolden(t *testing.T) {
	// Resolve the golden dir to absolute BEFORE emitInModule chdirs into the temp module.
	goldenDir, err := filepath.Abs(filepath.Join("testdata", "golden", "demo"))
	if err != nil {
		t.Fatal(err)
	}
	emitted := emitInModule(t, goldenSpec, goldenConf)

	// The runtime file must be valid Go in the declared package, but is not snapshotted.
	rt, ok := emitted[runtimeRel]
	if !ok {
		t.Fatalf("runtime file %q was not emitted; got %v", runtimeRel, sortedKeys(emitted))
	}
	if f, err := parser.ParseFile(token.NewFileSet(), runtimeRel, rt, parser.PackageClauseOnly); err != nil {
		t.Errorf("emitted runtime does not parse: %v", err)
	} else if f.Name.Name != "rotini" {
		t.Errorf("emitted runtime package = %q, want rotini", f.Name.Name)
	}
	delete(emitted, runtimeRel)

	if *updateGolden {
		writeGolden(t, goldenDir, emitted)
		t.Logf("updated %d golden files under %s", len(emitted), goldenDir)
		return
	}
	compareGolden(t, goldenDir, emitted)
}

// TestGenerateIdempotent asserts a second emit over the same module is byte-identical
// (no churn, no orphans) — the dogfood-regen invariant the whole refactor must preserve.
func TestGenerateIdempotent(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, dir, "go.mod", "module example.com/demo\n\ngo 1.26\n")
	writeTestFile(t, dir, ".rotini.spec.yaml", goldenSpec)
	writeTestFile(t, dir, ".rotini.conf.yaml", goldenConf)
	t.Chdir(dir)

	gen := func() map[string]string {
		if err := NewProcessor("0.0.0").Generate(".rotini.spec.yaml", ".rotini.conf.yaml", false, func(string, error) {}); err != nil {
			t.Fatalf("Generate: %v", err)
		}
		return collectGoFiles(t, dir)
	}
	first := gen()
	second := gen()

	if len(first) != len(second) {
		t.Fatalf("file count changed across passes: %d -> %d", len(first), len(second))
	}
	for name, b1 := range first {
		b2, ok := second[name]
		if !ok {
			t.Errorf("file %q vanished on the second pass", name)
			continue
		}
		if b1 != b2 {
			t.Errorf("file %q changed across passes (not idempotent)", name)
		}
	}
}

func sortedKeys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func writeGolden(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	if err := os.RemoveAll(dir); err != nil {
		t.Fatal(err)
	}
	for name, content := range files {
		writeTestFile(t, dir, name, content)
	}
}

func compareGolden(t *testing.T, dir string, emitted map[string]string) {
	t.Helper()
	want := collectGoFiles(t, dir)
	for _, name := range sortedKeys(want) {
		got, ok := emitted[name]
		if !ok {
			t.Errorf("golden file %q was not emitted", name)
			continue
		}
		if got != want[name] {
			t.Errorf("emitted %q differs from golden (run -update if intentional)", name)
		}
	}
	for _, name := range sortedKeys(emitted) {
		if _, ok := want[name]; !ok {
			t.Errorf("emitted unexpected file %q not in golden (run -update if intentional)", name)
		}
	}
}
