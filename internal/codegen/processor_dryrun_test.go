package codegen

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestGenerateDryRun pins the dry run end to end in-process: an up-to-date tree plans nothing,
// a spec change plans exactly the real changes, and nothing is written either way.
func TestGenerateDryRun(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, dir, "go.mod", "module example.com/demo\n\ngo 1.26\n")
	writeTestFile(t, dir, ".rotini.spec.yaml", "version: 0.0.0\ncommand:\n  name: demo\n")
	writeTestFile(t, dir, ".rotini.conf.yaml", goldenConf)
	t.Chdir(dir)
	p := NewProcessor("0.0.0")
	if err := p.Generate(".rotini.spec.yaml", ".rotini.conf.yaml", false, nil, nil); err != nil {
		t.Fatal(err)
	}

	planned, err := p.GenerateDryRun(".rotini.spec.yaml", ".rotini.conf.yaml", nil)
	if err != nil || len(planned.Changes) != 0 || !strings.HasPrefix(planned.Result, "[") {
		t.Fatalf("up to date: %+v, %v; want no changes and a timing line", planned, err)
	}

	writeTestFile(t, dir, ".rotini.spec.yaml", "version: 0.0.0\ncommand:\n  name: demo\n  commands:\n    - name: ship\n")
	cmdFile := filepath.Join(dir, "internal", "cmd", "demo", "zz_demo.go")
	before, err := os.ReadFile(cmdFile)
	if err != nil {
		t.Fatal(err)
	}
	planned, err = p.GenerateDryRun(".rotini.spec.yaml", ".rotini.conf.yaml", nil)
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(planned.Changes, "\n")
	for _, want := range []string{"update internal/cmd/demo/zz_demo.go", "create internal/cmd/demo/demo_ship.go"} {
		if !strings.Contains(joined, want) {
			t.Errorf("changes =\n%s\nwant a line with %q", joined, want)
		}
	}
	if after, _ := os.ReadFile(cmdFile); string(after) != string(before) {
		t.Error("a dry run rewrote the generated file")
	}
	if _, err := os.Stat(filepath.Join(dir, "internal", "cmd", "demo", "demo_ship.go")); !os.IsNotExist(err) {
		t.Error("a dry run created a stub")
	}

	writeTestFile(t, dir, ".rotini.spec.yaml", "version: 0.0.0\ncommand: {name: demo, flags: [{name: x, schema: {type: nosuchtype}}]}\n")
	if _, err := p.GenerateDryRun(".rotini.spec.yaml", ".rotini.conf.yaml", nil); err == nil {
		t.Error("an invalid spec planned without an error")
	}
}

// TestInitializeDryRun pins that init's dry run lists what init writes and writes none of it.
func TestInitializeDryRun(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, dir, "go.mod", "module example.com/demo\n\ngo 1.26\n")
	t.Chdir(dir)
	out, err := NewProcessor("0.0.0").InitializeDryRun("demo", InitOptions{})
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(out.Changes, "\n")
	for _, want := range []string{"create cmd/demo/.rotini.spec.yaml", "create cmd/demo/.rotini.conf.yaml", "create cmd/demo/main.go", "create internal/cmd/demo/zz_rotini.go", "create internal/cmd/demo/demo.go"} {
		if !strings.Contains(joined, want) {
			t.Errorf("changes =\n%s\nwant a line with %q", joined, want)
		}
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 1 {
		t.Errorf("a dry run wrote files: %v", entries)
	}
	if _, err := NewProcessor("0.0.0").Initialize("demo", InitOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, err := NewProcessor("0.0.0").InitializeDryRun("demo", InitOptions{}); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Errorf("dry run over an existing seed = %v, want the same refusal init gives", err)
	}
}

// TestDryRunEnv pins reading generate.dry_run_env, and that an unreadable conf names none.
func TestDryRunEnv(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, dir, ".rotini.spec.yaml", "version: 0.0.0\ncommand:\n  name: demo\n")
	writeTestFile(t, dir, ".rotini.conf.yaml", "version: 0.0.0\ngenerate:\n  dry_run_env: CI\n")
	spec := filepath.Join(dir, ".rotini.spec.yaml")
	if got := DryRunEnv(spec, ""); got != "CI" {
		t.Errorf("DryRunEnv = %q, want CI", got)
	}
	if got := DryRunEnv(spec, filepath.Join(dir, "missing.yaml")); got != "" {
		t.Errorf("DryRunEnv with a missing conf = %q, want none", got)
	}
}

// TestGenerate_renamedCmdPackage pins that a conf naming the cmd package anew stops generate
// and its dry run before anything is written, naming each file that declares another package,
// and that generate runs again once those files match. Test files may use the _test package;
// build-ignored and generated files don't count.
func TestGenerate_renamedCmdPackage(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, dir, "go.mod", "module example.com/demo\n\ngo 1.26\n")
	writeTestFile(t, dir, ".rotini.spec.yaml", "version: 0.0.0\ncommand:\n  name: demo\n  commands:\n    - name: ship\n")
	writeTestFile(t, dir, ".rotini.conf.yaml", goldenConf)
	t.Chdir(dir)
	p := NewProcessor("0.0.0")
	if err := p.Generate(".rotini.spec.yaml", ".rotini.conf.yaml", false, nil, nil); err != nil {
		t.Fatal(err)
	}
	pkgDir := filepath.Join(dir, "internal", "cmd", "demo")
	writeTestFile(t, pkgDir, "helper.go", "package demo\n")
	writeTestFile(t, pkgDir, "demo_test.go", "package democli_test\n")
	writeTestFile(t, pkgDir, "ignored.go", "//go:build ignore\n\npackage demo\n")
	writeTestFile(t, pkgDir, "zz_other.go", "// Code generated by hand. DO NOT EDIT.\n\npackage demo\n")
	writeTestFile(t, dir, ".rotini.conf.yaml", strings.Replace(goldenConf, "package: demo", "package: democli", 1))
	before, err := os.ReadFile(filepath.Join(pkgDir, "zz_demo.go"))
	if err != nil {
		t.Fatal(err)
	}

	want := `the conf names the cmd package "democli", but these files in internal/cmd/demo declare another package: demo.go (package demo), demo_ship.go (package demo), helper.go (package demo); rotini never rewrites them`
	if _, err := p.GenerateDryRun(".rotini.spec.yaml", ".rotini.conf.yaml", nil); err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("dry run: err = %v, want it to contain %q", err, want)
	}
	if err := p.Generate(".rotini.spec.yaml", ".rotini.conf.yaml", false, nil, nil); err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("generate: err = %v, want it to contain %q", err, want)
	}
	if after, _ := os.ReadFile(filepath.Join(pkgDir, "zz_demo.go")); string(after) != string(before) {
		t.Error("the refused generate rewrote the generated file")
	}
	if src, _ := os.ReadFile(filepath.Join(pkgDir, "demo.go")); !strings.HasPrefix(string(src), "package demo\n") && !strings.Contains(string(src), "\npackage demo\n") {
		t.Errorf("the refused generate edited a handler:\n%s", src)
	}

	for _, name := range []string{"demo.go", "demo_ship.go", "helper.go"} {
		src, err := os.ReadFile(filepath.Join(pkgDir, name))
		if err != nil {
			t.Fatal(err)
		}
		writeTestFile(t, pkgDir, name, strings.Replace(string(src), "package demo\n", "package democli\n", 1))
	}
	writeTestFile(t, pkgDir, "zz_other.go", "// Code generated by hand. DO NOT EDIT.\n\npackage democli\n")
	if err := p.Generate(".rotini.spec.yaml", ".rotini.conf.yaml", false, nil, nil); err != nil {
		t.Fatalf("generate after the handlers moved: %v", err)
	}
	if src, _ := os.ReadFile(filepath.Join(pkgDir, "zz_demo.go")); !strings.Contains(string(src), "\npackage democli\n") {
		t.Error("the generated file doesn't declare the new package")
	}
}
