package internal_test

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/go-rotini/rotini/internal"
)

// TestRenderedOutputCompiles is the M3 exit-check meta-test. For
// every scenario under testdata/golden, it:
//
//  1. Renders the framework + bridge + handler-skel files into a
//     temp directory laid out as a real Go module.
//  2. Writes a go.mod with a `replace` directive pointing at the
//     local rotini repo so the rendered imports resolve.
//  3. Runs `go build ./...` and asserts success.
//
// Catches: import errors, type mismatches, signature drift between
// templates and the rtk runtime, and any "the rendered Go code is
// syntactically valid but doesn't compile" gap that gofmt + go-parser
// (used by Render's internal validation) cannot detect.
//
// Spawns the go toolchain so it's relatively slow (~2-5s per
// scenario). Skipped under -short.
func TestRenderedOutputCompiles(t *testing.T) {
	if testing.Short() {
		t.Skip("compile-check spawns the go toolchain; skipped under -short")
	}
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go toolchain not in PATH")
	}

	repoRoot, err := filepath.Abs("..")
	if err != nil {
		t.Fatalf("resolve repo root: %v", err)
	}

	for _, scenario := range listGoldenScenarios(t) {
		t.Run(scenario, func(t *testing.T) {
			t.Parallel()
			compileCheckScenario(t, scenario, repoRoot)
		})
	}
}

func compileCheckScenario(t *testing.T, scenario, repoRoot string) {
	t.Helper()
	tmp := t.TempDir()
	moduleName := "example.com/compile-check/" + scenario

	specPath := filepath.Join("testdata", "golden", scenario, "spec.yaml")

	// Render into a module-layout-friendly path. Run resolves the
	// framework dir relative to either an absolute path or cwd; we
	// pass absolute paths so the renderer doesn't have to know about
	// the temp module root.
	frameworkDir := filepath.Join(tmp, "internal", "cli", "rotini")
	cmdDir := filepath.Join(tmp, "internal", "cli", "cmd")

	if _, err := internal.Run(internal.RunOptions{
		SpecPath:   specPath,
		OutputDir:  frameworkDir,
		Package:    "rotini",
		CmdDir:     cmdDir,
		CmdPackage: "cmd",
		ModulePath: moduleName,
		ModuleRoot: tmp,
	}); err != nil {
		t.Fatalf("Run: %v", err)
	}

	// Write a go.mod that replaces github.com/go-rotini/rotini with
	// the local repo. The rendered code imports the rtk subpackage;
	// MVS handles the transitive deps via the replaced module's own
	// go.mod.
	goMod := fmt.Sprintf(`module %s

go 1.22

require github.com/go-rotini/rotini v0.0.0

replace github.com/go-rotini/rotini => %s
`, moduleName, repoRoot)
	if err := os.WriteFile(filepath.Join(tmp, "go.mod"), []byte(goMod), 0o600); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}

	// Write main.go that blank-imports cmd. The cmd package
	// transitively pulls in the framework (rotini package) so a
	// successful `go build ./...` exercises every rendered file.
	mainGo := fmt.Sprintf(`package main

import _ %q

func main() {}
`, moduleName+"/internal/cli/cmd")
	if err := os.WriteFile(filepath.Join(tmp, "main.go"), []byte(mainGo), 0o600); err != nil {
		t.Fatalf("write main.go: %v", err)
	}

	// `go mod tidy` would pull in transitive deps but adds time we
	// can avoid by setting GOFLAGS=-mod=mod, which lets `go build`
	// resolve transitive requirements lazily from the replaced
	// module's own go.sum.
	cmd := exec.Command("go", "build", "-mod=mod", "-o", os.DevNull, "./...")
	cmd.Dir = tmp
	cmd.Env = append(os.Environ(), "GOFLAGS=") // ignore caller's GOFLAGS
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("go build failed:\n%s\n--- err ---\n%v", out, err)
	}
}
