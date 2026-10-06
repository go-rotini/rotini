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
	out, err := NewProcessor("0.0.0").InitializeDryRun("demo", "", false)
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
	if _, err := NewProcessor("0.0.0").Initialize("demo", "", false); err != nil {
		t.Fatal(err)
	}
	if _, err := NewProcessor("0.0.0").InitializeDryRun("demo", "", false); err == nil || !strings.Contains(err.Error(), "already exists") {
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
