package codegen

import (
	"os/exec"
	"path/filepath"
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

	if err := NewProcessor("0.0.0").Generate(".rotini.spec.yaml", ".rotini.conf.yaml", false, func(string, error) {}); err != nil {
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
	if err := NewProcessor("0.0.0").Generate(".rotini.spec.yaml", ".rotini.conf.yaml", false, func(string, error) {}); err != nil {
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
