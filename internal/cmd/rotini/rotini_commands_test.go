package rotini

import (
	"context"
	"errors"
	"runtime"
	"runtime/debug"
	"strings"
	"testing"
	"time"

	"github.com/go-rotini/rotini"
	"github.com/go-rotini/rotini/internal/codegen"
)

// `rotini version --format json` writes the declared output: the version as text prints it,
// the Go version, and the build info's module path and version.
func TestCLI_versionJSON(t *testing.T) {
	restore := readBuildInfo
	t.Cleanup(func() { readBuildInfo = restore })
	readBuildInfo = func() (*debug.BuildInfo, bool) {
		return &debug.BuildInfo{Main: debug.Module{Path: "github.com/go-rotini/rotini", Version: "v9.9.9"}}, true
	}

	p, out, _ := newTestCLI(t)
	if code, err := p.Run([]string{"version", "--format", "json"}); code != 0 || err != nil {
		t.Fatalf("version --format json = (%d, %v)", code, err)
	}
	got, err := rotini.DecodeOutput[RotiniVersionOutput](p, out.Bytes(), "json")
	if err != nil {
		t.Fatalf("decode %q: %v", out.String(), err)
	}
	want := RotiniVersionOutput{Version: "v" + testVersion, GoVersion: runtime.Version(), ModulePath: "github.com/go-rotini/rotini", ModuleVersion: "v9.9.9"}
	if got != want {
		t.Errorf("version --format json = %+v, want %+v", got, want)
	}

	// Without build info, the version and Go version are still there.
	readBuildInfo = func() (*debug.BuildInfo, bool) { return nil, false }
	p, out, _ = newTestCLI(t)
	if code, _ := p.Run([]string{"version", "--format", "json"}); code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	if got, err := rotini.DecodeOutput[RotiniVersionOutput](p, out.Bytes(), "json"); err != nil || got.ModulePath != "" || got.GoVersion == "" {
		t.Errorf("without build info = %+v, %v", got, err)
	}
}

// `rotini tree` prints what the tree step returns, and reports each of its problems.
func TestCLI_tree(t *testing.T) {
	p, out, _ := newTestCLI(t)
	var gotPath string
	p.WithDependency(treeDep, codegen.TreeFn(func(specPath string) (string, error) {
		gotPath = specPath
		return "app\n  add, a\n", nil
	}))
	if code, err := p.Run([]string{"tree", "s.yaml"}); code != 0 || err != nil {
		t.Fatalf("tree = (%d, %v)", code, err)
	}
	if gotPath != "s.yaml" || out.String() != "app\n  add, a\n" {
		t.Errorf("tree read %q and printed %q", gotPath, out.String())
	}

	p, _, errb := newTestCLI(t)
	p.WithDependency(treeDep, codegen.TreeFn(func(string) (string, error) {
		return "", errors.Join(errBadSpec, errAdvisory)
	}))
	if code, _ := p.Run([]string{"tree"}); code != 1 {
		t.Fatalf("exit = %d, want 1", code)
	}
	if got := errb.String(); !strings.Contains(got, "Error: spec is broken") || !strings.Contains(got, "Error: an advisory warning") {
		t.Errorf("stderr = %q, want one Error line per problem", got)
	}
}

// `rotini import cobra` passes its inputs through, prints the notes and summary on stderr, and
// reports the spec and conf like init.
func TestCLI_importCobra(t *testing.T) {
	p, out, errb := newTestCLI(t)
	var got codegen.ImportOptions
	p.WithDependency(importDep, codegen.ImportFn(func(_ context.Context, opts codegen.ImportOptions) (codegen.Imported, error) {
		got = opts
		return codegen.Imported{
			Notes:   []string{"[lossy] acme: a note"},
			Summary: "imported 1 commands, 0 flags: 1 lossy, 0 unsupported, 0 info",
			Spec:    "cmd/acme/.rotini.spec.yaml", Conf: "cmd/acme/.rotini.conf.yaml", Result: "[12:00:00] 1ms",
		}, nil
	}))
	argv := []string{"import", "cobra", "./cmd", "--root", "newRoot()", "--name", "acme", "--dir", "acme-rotini",
		"--strict", "--format", "json", "--tags", "dev", "--timeout", "30s", "--importer-version", "../import", "--force"}
	if code, _ := p.Run(argv); code != 0 {
		t.Fatalf("exit = %d, stderr %q", code, errb.String())
	}
	want := codegen.ImportOptions{Package: "./cmd", Root: "newRoot()", Name: "acme", Dir: "acme-rotini", Strict: true,
		Format: "json", Tags: "dev", Timeout: 30 * time.Second, ImporterVersion: "../import", Force: true}
	if got != want {
		t.Errorf("options = %+v, want %+v", got, want)
	}
	if !strings.HasPrefix(errb.String(), "[lossy] acme: a note\nimported 1 commands") {
		t.Errorf("stderr = %q, want the notes then the summary", errb.String())
	}
	if want := "spec: cmd/acme/.rotini.spec.yaml\nconf: cmd/acme/.rotini.conf.yaml\n[12:00:00] 1ms\n"; out.String() != want {
		t.Errorf("stdout = %q, want %q", out.String(), want)
	}
}

// A dry run lists what the import would write and exits 2; a failed import still prints its
// notes before its errors.
func TestCLI_importCobraDryRunAndFailure(t *testing.T) {
	p, _, errb := newTestCLI(t)
	p.WithDependency(importDep, codegen.ImportFn(func(context.Context, codegen.ImportOptions) (codegen.Imported, error) {
		t.Error("a dry run ran the real import")
		return codegen.Imported{}, nil
	}))
	p.WithDependency(importDryRunDep, codegen.ImportFn(func(context.Context, codegen.ImportOptions) (codegen.Imported, error) {
		return codegen.Imported{Spec: "s", Conf: "c", Changes: []string{"1. create cmd/acme/.rotini.spec.yaml"}}, nil
	}))
	if code, _ := p.Run([]string{"import", "cobra", "./cmd", "-n"}); code != 2 {
		t.Fatalf("dry run exit = %d, want 2", code)
	}
	if !strings.Contains(errb.String(), "dry run: 1 change not written") {
		t.Errorf("stderr = %q", errb.String())
	}

	p, _, errb = newTestCLI(t)
	p.WithDependency(importDep, codegen.ImportFn(func(context.Context, codegen.ImportOptions) (codegen.Imported, error) {
		return codegen.Imported{Notes: []string{"[info] acme: a note"}}, errBadSpec
	}))
	if code, _ := p.Run([]string{"import", "cobra", "./cmd"}); code != 1 {
		t.Fatalf("failure exit = %d, want 1", code)
	}
	if got := errb.String(); !strings.HasPrefix(got, "[info] acme: a note\n") || !strings.Contains(got, "Error: spec is broken") {
		t.Errorf("stderr = %q, want the note, then the error", got)
	}
}

// The --importer-version summary names the release the import runs by default.
func TestImporterVersionInHelp(t *testing.T) {
	page, err := Help("import", "cobra")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(page, "(default "+codegen.ImporterVersion+")") {
		t.Errorf("rotini help import cobra doesn't name the default importer version %s", codegen.ImporterVersion)
	}
}
