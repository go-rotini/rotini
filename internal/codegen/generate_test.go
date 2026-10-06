package codegen

import (
	"flag"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"testing"
)

// The `models` target breaks the import cycle of an inline command whose `handler:` lives in
// another package: cmd imports the handler package, so the handler cannot import cmd for its
// input types. The tests below cover the cycle with and without the target.

const modelsSpec = `version: 0.0.0
command:
  name: cyc
  commands:
    - name: deploy
      summary: deploy a thing
      handler:
        import: deployh example.com/cyc/handlers
        convention: Deploy
      arguments:
        - name: target
          schema: {type: string, required: true}
`

// modelsHandler returns a passthrough handler package that reads its input type from
// importPath.
func modelsHandler(importPath, qualifier string) string {
	return `package handlers

import (
	"context"
	"fmt"

	"github.com/go-rotini/rotini"

	"` + importPath + `"
)

type deployHandler struct {
	rotini.NoCascadingPreRun
	rotini.NoPreRun
	rotini.NoPostRun
	rotini.NoCascadingPostRun
}

func Deploy() rotini.Handler { return &deployHandler{} }

func (*deployHandler) Run(ctx context.Context, rtx *rotini.Context) {
	inputs, err := rtx.Inputs[` + qualifier + `.CycDeployInputs]()
	if err != nil {
		rtx.RecordError(err)
		return
	}
	fmt.Fprintln(rtx.Stdout, "deploying", inputs.CycDeploy.Arguments.Target)
}
`
}

// modelsModule scaffolds the cycle scenario and generates it, returning the module dir.
func modelsModule(t *testing.T, conf, handlerImport, qualifier string) string {
	t.Helper()
	repoRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	writeTestFile(t, dir, "go.mod", "module example.com/cyc\n\ngo 1.27\n\nrequire github.com/go-rotini/rotini v0.0.0\n\nreplace github.com/go-rotini/rotini => "+filepath.ToSlash(repoRoot)+"\n")
	writeTestFile(t, dir, ".rotini.spec.yaml", modelsSpec)
	writeTestFile(t, dir, ".rotini.conf.yaml", conf)
	writeTestFile(t, dir, "handlers/deploy.go", modelsHandler(handlerImport, qualifier))
	t.Chdir(dir)

	if err := NewProcessor("0.0.0").Generate(".rotini.spec.yaml", ".rotini.conf.yaml", false, func(string, error) {}, func([]error) {}); err != nil {
		t.Fatalf("Generate: %v", err)
	}
	return dir
}

func goBuild(t *testing.T, dir string) (string, error) {
	t.Helper()
	if out, err := exec.Command("go", "mod", "tidy").CombinedOutput(); err != nil {
		t.Fatalf("go mod tidy: %v\n%s", err, out)
	}
	cmd := exec.Command("go", "build", "./...")
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// TestModels_withoutTargetTheHandlerCycles pins the import cycle the models target exists to
// break. If the build ever succeeds, the cycle was solved another way.
func TestModels_withoutTargetTheHandlerCycles(t *testing.T) {
	skipUnlessCompiling(t)
	dir := modelsModule(t, `version: 0.0.0
generate:
  packages:
    - type: cmd
      file: internal/cmd/cyc/zz_cyc.go
      package: cyc
`, "example.com/cyc/internal/cmd/cyc", "cyc")

	out, err := goBuild(t, dir)
	if err == nil {
		t.Fatal("the handler-passthrough layout compiled without a models target — the cycle is gone; revisit whether `models` is still needed")
	}
	if !strings.Contains(out, "import cycle not allowed") {
		t.Errorf("build failed for some other reason:\n%s", out)
	}
}

// TestModels_targetBreaksTheCycle pins that with a models package, cmd and handlers both
// import models, models imports neither, and the module builds.
func TestModels_targetBreaksTheCycle(t *testing.T) {
	skipUnlessCompiling(t)
	dir := modelsModule(t, `version: 0.0.0
generate:
  packages:
    - type: cmd
      file: internal/cmd/cyc/zz_cyc.go
      package: cyc
    - type: models
      file: internal/models/zz_models.go
      package: models
`, "example.com/cyc/internal/models", "models")

	if out, err := goBuild(t, dir); err != nil {
		t.Fatalf("the models layout does not compile:\n%s", out)
	}

	models := readEmitted(t, dir, "internal/models/zz_models.go")
	for _, want := range []string{"package models", "type CycDeployInputs struct", "type CycDeployArguments struct"} {
		if !strings.Contains(models, want) {
			t.Errorf("models file missing %q", want)
		}
	}
	// models must import nothing from the module so both sides can import it.
	if strings.Contains(models, "example.com/cyc") {
		t.Errorf("the models package imports the module it serves:\n%s", models)
	}

	// The cmd file carries aliases, so code in the cmd package still uses unqualified names.
	cmd := readEmitted(t, dir, "internal/cmd/cyc/zz_cyc.go")
	if !strings.Contains(cmd, "type CycDeployInputs = models.CycDeployInputs") {
		t.Errorf("cmd file does not re-export the models types:\n%s", cmd)
	}
	if strings.Contains(cmd, "type CycDeployInputs struct") {
		t.Error("cmd file still declares the input structs; they must live only in models")
	}
}

// TestModels_targetOnTheCmdFileIsANoOp pins that a models target naming the cmd file
// causes no split.
func TestModels_targetOnTheCmdFileIsANoOp(t *testing.T) {
	dir := modelsModule(t, `version: 0.0.0
generate:
  packages:
    - type: cmd
      file: internal/cmd/cyc/zz_cyc.go
      package: cyc
    - type: models
      file: internal/cmd/cyc/zz_cyc.go
      package: cyc
`, "example.com/cyc/internal/cmd/cyc", "cyc")

	cmd := readEmitted(t, dir, "internal/cmd/cyc/zz_cyc.go")
	if !strings.Contains(cmd, "type CycDeployInputs struct") {
		t.Error("a models target on the cmd file should leave the structs in place")
	}
	if strings.Contains(cmd, "= models.") {
		t.Error("a no-op split emitted aliases to itself")
	}
	if _, err := readEmittedIfExists(dir, "internal/models/zz_models.go"); err == nil {
		t.Error("a no-op split wrote a separate models file")
	}
}

// TestModels_sharedDirectorySurvivesPruning pins that a models file placed in the cmd
// directory is not pruned, on the first pass or on regeneration.
func TestModels_sharedDirectorySurvivesPruning(t *testing.T) {
	dir := modelsModule(t, `version: 0.0.0
generate:
  packages:
    - type: cmd
      file: internal/cmd/cyc/zz_cyc.go
      package: cyc
    - type: models
      file: internal/cmd/cyc/zz_models.go
      package: cyc
`, "example.com/cyc/internal/cmd/cyc", "cyc")

	if _, err := readEmittedIfExists(dir, "internal/cmd/cyc/zz_models.go"); err != nil {
		t.Fatalf("the models file was pruned from the cmd directory: %v", err)
	}
	if err := NewProcessor("0.0.0").Generate(".rotini.spec.yaml", ".rotini.conf.yaml", false, func(string, error) {}, func([]error) {}); err != nil {
		t.Fatal(err)
	}
	if _, err := readEmittedIfExists(dir, "internal/cmd/cyc/zz_models.go"); err != nil {
		t.Fatalf("the models file was pruned on the second pass: %v", err)
	}
}

// updateGolden regenerates the golden snapshots: `go test ./internal/codegen -run Golden -update`.
var updateGolden = flag.Bool("update", false, "update generate golden files")

// goldenSpec and goldenConf describe a CLI that exercises the input-struct, Definition,
// InputSettings, rollup, and stub paths in one pass.
const goldenSpec = `version: 0.0.0
command:
  name: demo
  description: a demo cli
  env_prefix: DEMO
  flags:
    - name: verbose
      summary: verbose output
      identifiers: [--verbose, -v]
      schema: {type: bool}
  env:
    - name: home
      summary: home dir
      schema: {type: string, variable: DEMO_HOME}
    # Two derived names, one with an underscore and one camelCase: the golden pins the env
    # tag the input reader reads.
    - name: base_url
      summary: an env input whose name carries an underscore
      schema: {type: string}
    - name: apiKey
      summary: a camelCase env input
      schema: {type: string}
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
        # A flag whose env fallback is derived from a recon key with an underscore inside a
        # segment — the same defect on the flag channel.
        - name: sort-by
          summary: ordering
          identifiers: [--sort-by]
          schema: {type: string, key: build.sort_by}
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
`

// emitInModule writes spec+conf into a fresh temp module, runs the generate pass with
// the working directory set there, and returns every emitted .go file keyed by its
// module-relative slash path.
func emitInModule(t *testing.T, spec, conf string) map[string]string {
	t.Helper()
	dir := t.TempDir()
	writeTestFile(t, dir, "go.mod", "module example.com/demo\n\ngo 1.27\n")
	writeTestFile(t, dir, ".rotini.spec.yaml", spec)
	writeTestFile(t, dir, ".rotini.conf.yaml", conf)
	t.Chdir(dir)

	var cbErr error
	if err := NewProcessor("0.0.0").Generate(".rotini.spec.yaml", ".rotini.conf.yaml", false, func(_ string, e error) {
		if e != nil {
			cbErr = e
		}
	}, func([]error) {}); err != nil {
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

// TestGenerateGolden pins the emitted output: every emitted .go file is byte-compared against
// testdata/golden. Run with -update to refresh.
func TestGenerateGolden(t *testing.T) {
	// Resolve the golden dir to absolute BEFORE emitInModule chdirs into the temp module.
	goldenDir, err := filepath.Abs(filepath.Join("testdata", "golden", "demo"))
	if err != nil {
		t.Fatal(err)
	}
	emitted := emitInModule(t, goldenSpec, goldenConf)

	if *updateGolden {
		writeGolden(t, goldenDir, emitted)
		t.Logf("updated %d golden files under %s", len(emitted), goldenDir)
		return
	}
	compareGolden(t, goldenDir, emitted)
}

// TestGenerateIdempotent pins that a second generate over the same module is byte-identical,
// with no new or removed files.
func TestGenerateIdempotent(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, dir, "go.mod", "module example.com/demo\n\ngo 1.27\n")
	writeTestFile(t, dir, ".rotini.spec.yaml", goldenSpec)
	writeTestFile(t, dir, ".rotini.conf.yaml", goldenConf)
	t.Chdir(dir)

	gen := func() map[string]string {
		if err := NewProcessor("0.0.0").Generate(".rotini.spec.yaml", ".rotini.conf.yaml", false, func(string, error) {}, func([]error) {}); err != nil {
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

// TestPackageHeader pins that a package's `header:` (e.g. a license block) starts every file
// its target produces: the generated file, stubs, the entrypoint, and the models file.
func TestPackageHeader(t *testing.T) {
	const spec = `version: 0.0.0
command:
  name: demo
  commands:
    - name: build
`
	const conf = `version: 0.0.0
generate:
  packages:
    - type: main
      file: cmd/demo/main.go
      header: |
        // Copyright 2026 Acme, Inc.
        // SPDX-License-Identifier: Apache-2.0
    - type: cmd
      file: internal/cmd/demo/zz_demo.go
      package: demo
      header: |
        // Copyright 2026 Acme, Inc.
        // SPDX-License-Identifier: Apache-2.0
    - type: models
      file: internal/models/zz_models.go
      package: models
      header: |
        // Copyright 2026 Acme, Inc.
        // SPDX-License-Identifier: Apache-2.0
`
	emitted := emitInModule(t, spec, conf)

	want := []string{
		"cmd/demo/main.go",             // the entrypoint, create-once
		"internal/cmd/demo/zz_demo.go", // the generated file, rewritten every pass
		"internal/cmd/demo/demo.go",    // an editable stub
		"internal/cmd/demo/demo_build.go",
		"internal/models/zz_models.go", // the split models file
	}
	for _, path := range want {
		body, ok := emitted[path]
		if !ok {
			t.Errorf("%s was not emitted; got %v", path, slices.Sorted(maps.Keys(emitted)))
			continue
		}
		if !strings.HasPrefix(body, "// Copyright 2026 Acme, Inc.\n// SPDX-License-Identifier: Apache-2.0\n") {
			t.Errorf("%s does not start with the declared header:\n%s", path, firstLines(body, 4))
		}
		// The generated-code marker remains below the header.
		if strings.Contains(path, "zz_") && !strings.Contains(body, "Code generated by rotini") {
			t.Errorf("%s lost rotini's generated-code marker", path)
		}
	}
}

// TestPackageHeader_buildConstraint pins that a //go:build header is emitted first, before
// the package clause.
func TestPackageHeader_buildConstraint(t *testing.T) {
	const spec = "version: 0.0.0\ncommand:\n  name: demo\n"
	const conf = `version: 0.0.0
generate:
  packages:
    - type: cmd
      file: internal/cmd/demo/zz_demo.go
      package: demo
      header: "//go:build !windows"
`
	emitted := emitInModule(t, spec, conf)
	body := emitted["internal/cmd/demo/zz_demo.go"]
	if !strings.HasPrefix(body, "//go:build !windows\n") {
		t.Fatalf("build constraint is not first:\n%s", firstLines(body, 4))
	}
	if i, j := strings.Index(body, "//go:build"), strings.Index(body, "package "); i > j {
		t.Error("the build constraint must precede the package clause")
	}
}

// TestPackageHeader_malformedFailsLoudly pins that a header that is not valid Go fails
// generate with a gofmt error.
func TestPackageHeader_malformedFailsLoudly(t *testing.T) {
	const spec = "version: 0.0.0\ncommand:\n  name: demo\n"
	const conf = `version: 0.0.0
generate:
  packages:
    - type: cmd
      file: internal/cmd/demo/zz_demo.go
      package: demo
      header: "this is not a comment"
`
	dir := t.TempDir()
	writeTestFile(t, dir, "go.mod", "module example.com/demo\n\ngo 1.27\n")
	writeTestFile(t, dir, ".rotini.spec.yaml", spec)
	writeTestFile(t, dir, ".rotini.conf.yaml", conf)
	t.Chdir(dir)

	err := NewProcessor("0.0.0").Generate(".rotini.spec.yaml", ".rotini.conf.yaml", false, func(string, error) {}, func([]error) {})
	if err == nil {
		t.Fatal("a header that is not valid Go generated successfully, want a gofmt failure")
	}
	if !strings.Contains(err.Error(), "gofmt") {
		t.Errorf("error %q does not point at the formatting failure", err)
	}
}

// firstLines returns at most n lines of s, for a readable failure message.
func firstLines(s string, n int) string {
	lines := strings.SplitN(s, "\n", n+1)
	if len(lines) > n {
		lines = lines[:n]
	}
	return strings.Join(lines, "\n")
}

// TestPrune_sparesHandWrittenFiles pins that pruning removes only orphaned files carrying
// the generated-stub marker, reports each removal, and leaves hand-written files alone.
func TestPrune_sparesHandWrittenFiles(t *testing.T) {
	const spec = `version: 0.0.0
command:
  name: demo
  commands:
    - name: build
    - name: ship
`
	dir := t.TempDir()
	writeTestFile(t, dir, "go.mod", "module example.com/demo\n\ngo 1.27\n")
	writeTestFile(t, dir, ".rotini.spec.yaml", spec)
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
	gen(t)

	cmdDir := filepath.Join(dir, "internal", "cmd", "demo")
	helpers := map[string]string{
		"deps.go": "package demo\n\nvar Store = \"store\"\n",
		// Named like a stub for a nonexistent command; only the marker decides.
		"demo_helpers.go": "package demo\n\nfunc helper() string { return \"x\" }\n",
	}
	for name, body := range helpers {
		writeTestFile(t, cmdDir, name, body)
	}

	// An unchanged spec touches none of them.
	if notices := gen(t); len(notices) != 0 {
		t.Errorf("an unchanged spec reported %v, want nothing pruned", notices)
	}
	for name, want := range helpers {
		got, err := os.ReadFile(filepath.Join(cmdDir, name))
		if err != nil {
			t.Errorf("%s was deleted by generate: %v", name, err)
			continue
		}
		if string(got) != want {
			t.Errorf("%s was rewritten by generate", name)
		}
	}

	// An orphaned stub is pruned and reported.
	writeTestFile(t, dir, ".rotini.spec.yaml", `version: 0.0.0
command:
  name: demo
  commands:
    - name: build
`)
	// It takes two generates: the first disables it, the second deletes it.
	notices := gen(t)
	if body, err := os.ReadFile(filepath.Join(cmdDir, "demo_ship.go")); err != nil || !strings.HasPrefix(string(body), "//go:build ignore\n") {
		t.Errorf("the stub for a removed command was not disabled: %v\n%s", err, body)
	}
	if len(notices) != 1 || !strings.Contains(notices[0].Error(), "demo_ship.go (disabled; removed by the next generate)") {
		t.Errorf("notices = %v, want one naming demo_ship.go as disabled", notices)
	}
	notices = gen(t)
	if _, err := os.Stat(filepath.Join(cmdDir, "demo_ship.go")); !os.IsNotExist(err) {
		t.Error("the stub for a removed command was not pruned")
	}
	if len(notices) != 1 || !strings.Contains(notices[0].Error(), "pruned demo_ship.go;") {
		t.Errorf("notices = %v, want one naming demo_ship.go — deleting a file is not silent", notices)
	}
	for name := range helpers {
		if _, err := os.Stat(filepath.Join(cmdDir, name)); err != nil {
			t.Errorf("%s was deleted while pruning an unrelated stub: %v", name, err)
		}
	}

	// A stub whose marker the author deleted is theirs now, and survives.
	writeTestFile(t, cmdDir, "demo_build.go", "package demo\n\n// mine now\n")
	writeTestFile(t, dir, ".rotini.spec.yaml", "version: 0.0.0\ncommand:\n  name: demo\n")
	gen(t)
	if _, err := os.Stat(filepath.Join(cmdDir, "demo_build.go")); err != nil {
		t.Error("a file whose generated marker was removed should be treated as hand-written")
	}
}

// TestStubLooksGenerated_recognizesTheLegacyMarker pins that stubs asserting the pre-v1.2.0
// interface name, Handlers, are still recognized as generated.
func TestStubLooksGenerated_recognizesTheLegacyMarker(t *testing.T) {
	dir := t.TempDir()
	cases := map[string]bool{
		"current.go": true,
		"legacy.go":  true,
		"mine.go":    false,
	}
	writeTestFile(t, dir, "current.go", "package demo\n\nvar _ rotini.Handler = (*demoBuildHandler)(nil)\n")
	writeTestFile(t, dir, "legacy.go", "package demo\n\nvar _ rotini.Handlers = (*demoBuildHandlers)(nil)\n")
	writeTestFile(t, dir, "mine.go", "package demo\n\nfunc helper() {}\n")
	for name, want := range cases {
		got, err := stubLooksGenerated(filepath.Join(dir, name))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if got != want {
			t.Errorf("stubLooksGenerated(%s) = %v, want %v", name, got, want)
		}
	}
}
