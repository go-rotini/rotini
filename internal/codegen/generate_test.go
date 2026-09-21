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

// The `models` package target exists for exactly one reason: composition mode 2
// (an inline command whose `handler:` sources its code from another package)
// otherwise deadlocks on an import cycle. The generated cmd package imports the
// handler package to delegate to it, so the handler package cannot import cmd back
// to reach its own generated input types.
//
// These two tests are the before and after of that cycle.

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

// modelsHandler is a passthrough handler package that needs its own input type —
// the thing that closes the cycle. importPath is where it reads the type from.
func modelsHandler(importPath, qualifier string) string {
	return `package handlers

import (
	"context"
	"fmt"

	"github.com/go-rotini/rotini"

	"` + importPath + `"
)

type deployHandlers struct {
	rotini.DefaultCascadingPreRun
	rotini.DefaultPreRun
	rotini.DefaultPostRun
	rotini.DefaultCascadingPostRun
}

func Deploy() rotini.Handlers { return &deployHandlers{} }

func (*deployHandlers) Run(ctx context.Context, rtx *rotini.Context) {
	inputs, err := rotini.Collect[` + qualifier + `.CycDeployInputs](rtx)
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
	writeTestFile(t, dir, "go.mod", "module example.com/cyc\n\ngo 1.26\n\nrequire github.com/go-rotini/rotini v0.0.0\n\nreplace github.com/go-rotini/rotini => "+filepath.ToSlash(repoRoot)+"\n")
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

// TestModels_withoutTargetTheHandlerCycles pins the DEFECT the models target fixes.
// Without a models package the handler's only source for its input type is the cmd
// package — which already imports the handler package. If this test ever stops
// failing, the cycle was solved some other way and the models target may be moot.
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

// TestModels_targetBreaksTheCycle is the fix: with the types in their own package,
// cmd and handlers both import models, and models imports neither.
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
	// models must depend on nothing local — that is what makes it importable from both sides.
	if strings.Contains(models, "example.com/cyc") {
		t.Errorf("the models package imports the module it serves:\n%s", models)
	}

	// The cmd file carries the aliases, not the definitions, so handler code inside
	// the cmd package still refers to its inputs unqualified.
	cmd := readEmitted(t, dir, "internal/cmd/cyc/zz_cyc.go")
	if !strings.Contains(cmd, "type CycDeployInputs = models.CycDeployInputs") {
		t.Errorf("cmd file does not re-export the models types:\n%s", cmd)
	}
	if strings.Contains(cmd, "type CycDeployInputs struct") {
		t.Error("cmd file still declares the input structs; they must live only in models")
	}
}

// Pointing models at the cmd file is a legal no-op: same file, same package, no split.
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

// TestModels_sharedDirectorySurvivesPruning guards a bug the split introduced: the
// pruner removes managed .go files in the cmd directory that no longer map to a
// command, and a models file living there is neither a stub nor the cmd file — so
// it was written and then immediately deleted.
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
	// Regenerating must not delete it either — pruning runs on every pass.
	if err := NewProcessor("0.0.0").Generate(".rotini.spec.yaml", ".rotini.conf.yaml", false, func(string, error) {}, func([]error) {}); err != nil {
		t.Fatal(err)
	}
	if _, err := readEmittedIfExists(dir, "internal/cmd/cyc/zz_models.go"); err != nil {
		t.Fatalf("the models file was pruned on the second pass: %v", err)
	}
	// Note this layout does NOT break the import cycle — models shares the cmd
	// PACKAGE, so a handler package still cannot import the types without cycling.
	// Splitting the directory is what fixes that (TestModels_targetBreaksTheCycle);
	// this test is only about the file surviving the pruner.
}

// ── golden output + idempotence ─────────────────────────────.

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
`

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

// TestPackageHeader covers the conf's `header:` — the license or copyright block many
// repositories mandate on every .go file, and the //go:build constraint some need.
//
// Without it, rotini is unusable at any shop whose CI rejects a file without a license
// header, which is a category of adoption blocker rather than a missing nicety. The header
// has to reach EVERY file a target produces — the generated file, the editable stub, the
// entrypoint and the models file — or the check fails on whichever one it missed.
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
		// rotini's own generated-code line stays, below the header, on the files that
		// carry one.
		if strings.Contains(path, "zz_") && !strings.Contains(body, "Code generated by rotini") {
			t.Errorf("%s lost rotini's generated-code marker", path)
		}
	}
}

// TestPackageHeader_buildConstraint: a //go:build line has to end up where the go tool reads
// it — before the package clause, with a blank line after — which is exactly what a header
// written verbatim above everything else produces.
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

// TestPackageHeader_malformedFailsLoudly: the header is verbatim, so a header that is not
// valid Go must fail at generate time rather than producing a file the go tool will not read.
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
	writeTestFile(t, dir, "go.mod", "module example.com/demo\n\ngo 1.26\n")
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

// TestPrune_sparesHandWrittenFiles pins the fix for a data-loss bug found by writing a real
// CLI against rotini: pruning used to remove EVERY unprotected .go file in the cmd package.
//
// The obvious place for a registry key shared by three handlers is a file beside them:
//
//	// internal/cmd/taskr/keys.go
//	var StoreKey = rotini.NewKey[*store.Store]("store")
//
// The next `go generate` deleted it, silently, and the build failed with `undefined: StoreKey`
// in five files. `keep:` was the documented remedy, and its own description says it is
// "intended to stay empty in steady state".
//
// Pruning now removes only files rotini WROTE — identified by the marker every generated stub
// carries — and reports each one.
func TestPrune_sparesHandWrittenFiles(t *testing.T) {
	const spec = `version: 0.0.0
command:
  name: demo
  commands:
    - name: build
    - name: ship
`
	dir := t.TempDir()
	writeTestFile(t, dir, "go.mod", "module example.com/demo\n\ngo 1.26\n")
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
		// The exact case that lost work: a key beside the handlers that use it.
		"keys.go": "package demo\n\nvar StoreKey = \"store\"\n",
		// A name that LOOKS like a stub for a command that does not exist. Only the
		// marker decides, so this survives too.
		"demo_helpers.go": "package demo\n\nfunc helper() string { return \"x\" }\n",
	}
	for name, body := range helpers {
		writeTestFile(t, cmdDir, name, body)
	}

	// Regenerating over an unchanged spec must touch none of them.
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

	// An ORPHANED STUB is still pruned — that is the feature — and is now reported.
	writeTestFile(t, dir, ".rotini.spec.yaml", `version: 0.0.0
command:
  name: demo
  commands:
    - name: build
`)
	notices := gen(t)
	if _, err := os.Stat(filepath.Join(cmdDir, "demo_ship.go")); !os.IsNotExist(err) {
		t.Error("the stub for a removed command was not pruned")
	}
	if len(notices) != 1 || !strings.Contains(notices[0].Error(), "demo_ship.go") {
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
