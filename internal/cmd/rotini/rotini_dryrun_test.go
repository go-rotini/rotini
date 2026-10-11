package rotini

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-rotini/rotini"
	"github.com/go-rotini/rotini/internal/codegen"
)

// dryRunCLI is a test CLI whose generate and init are doubles recording which ran.
func dryRunCLI(t *testing.T, changes []string) (*rotini.Program, *string) {
	t.Helper()
	prog, _, _ := newTestCLI(t)
	var which string
	prog.WithDependency(generateDep, codegen.GenerateFn(func(_, _ string, _ bool, onGenerate func(string, error), _ func([]error)) error {
		which = "generate"
		onGenerate("[12:00:00] 1ms", nil)
		return nil
	}))
	prog.WithDependency(generateDryRunDep, codegen.GenerateDryRunFn(func(string, string, func([]error)) (codegen.Planned, error) {
		which = "dry-run"
		return codegen.Planned{Result: "[12:00:00] 1ms", Changes: changes}, nil
	}))
	prog.WithDependency(initializeDep, codegen.InitializeFn(func(name string, _ codegen.InitOptions) (codegen.Initialized, error) {
		which = "init"
		return codegen.Initialized{Spec: "cmd/" + name + "/.rotini.spec.yaml", Conf: "cmd/" + name + "/.rotini.conf.yaml"}, nil
	}))
	prog.WithDependency(initializeDryRunDep, codegen.InitializeDryRunFn(func(name string, _ codegen.InitOptions) (codegen.Initialized, error) {
		which = "init dry-run"
		return codegen.Initialized{Spec: "cmd/" + name + "/.rotini.spec.yaml", Conf: "cmd/" + name + "/.rotini.conf.yaml", Changes: changes}, nil
	}))
	return prog, &which
}

func TestCLI_dryRun(t *testing.T) {
	conf := filepath.Join(t.TempDir(), "ci.conf.yaml")
	if err := os.WriteFile(conf, []byte("version: 0.0.0\ngenerate:\n  dry_run_env: ROTINI_TEST_DRY\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name    string
		env     string
		argv    []string
		changes []string
		ran     string
		code    int
	}{
		{"--dry-run, up to date", "", []string{"generate", "s.yaml", "--dry-run"}, nil, "dry-run", 0},
		{"--dry-run, changes", "", []string{"generate", "s.yaml", "-n"}, []string{"1. create a.go (1 bytes, mode 0644)"}, "dry-run", 2},
		{"env trigger", "yes", []string{"generate", "s.yaml", "--config", conf}, []string{"1. create a.go"}, "dry-run", 2},
		{"env trigger off", "false", []string{"generate", "s.yaml", "--config", conf}, nil, "generate", 0},
		{"--no-dry-run beats the env", "1", []string{"generate", "s.yaml", "--config", conf, "--no-dry-run"}, nil, "generate", 0},
		{"--dry-run without the env", "", []string{"generate", "s.yaml", "--config", conf, "--dry-run"}, nil, "dry-run", 0},
		{"env with --watch is a conflict", "on", []string{"generate", "s.yaml", "--config", conf, "--watch"}, nil, "", 1},
		{"--dry-run with --watch is a conflict", "", []string{"generate", "s.yaml", "--dry-run", "--watch"}, nil, "", 1},
		{"--no-dry-run lets the env-triggered run watch", "on", []string{"generate", "s.yaml", "--config", conf, "--no-dry-run", "--watch"}, nil, "generate", 0},
		{"init --dry-run", "", []string{"init", "demo", "--dry-run"}, []string{"1. create cmd/demo/.rotini.spec.yaml"}, "init dry-run", 2},
		{"init --no-dry-run", "", []string{"init", "demo", "--no-dry-run"}, nil, "init", 0},
		{"init --dry-run --no-dry-run: the last wins", "", []string{"init", "demo", "--dry-run", "--no-dry-run"}, []string{"1. create a.go"}, "init", 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("ROTINI_TEST_DRY", tt.env)
			p, ran := dryRunCLI(t, tt.changes)
			code, _ := p.Run(tt.argv)
			if code != tt.code || *ran != tt.ran {
				t.Errorf("exit %d, ran %q; want exit %d, ran %q", code, *ran, tt.code, tt.ran)
			}
		})
	}
}

func TestCLI_dryRunReport(t *testing.T) {
	prog, out, errb := newTestCLI(t)
	prog.WithDependency(generateDryRunDep, codegen.GenerateDryRunFn(func(string, string, func([]error)) (codegen.Planned, error) {
		return codegen.Planned{Changes: []string{"1. update zz.go", "2. delete old.go"}}, nil
	}))
	if code, _ := prog.Run([]string{"generate", "s.yaml", "--dry-run"}); code != 2 {
		t.Fatalf("exit = %d, want 2", code)
	}
	if want := "1. update zz.go\n2. delete old.go\ndry run: 2 changes not written\n"; errb.String() != want {
		t.Errorf("stderr = %q, want %q", errb.String(), want)
	}
	if strings.Contains(out.String(), "update") {
		t.Errorf("changes leaked to stdout: %q", out.String())
	}

	prog, _, errb = newTestCLI(t)
	prog.WithDependency(generateDryRunDep, codegen.GenerateDryRunFn(func(string, string, func([]error)) (codegen.Planned, error) {
		return codegen.Planned{Changes: []string{"1. create a.go"}}, nil
	}))
	if code, _ := prog.Run([]string{"generate", "s.yaml", "--dry-run"}); code != 2 {
		t.Fatalf("exit = %d, want 2", code)
	}
	if !strings.HasSuffix(errb.String(), "dry run: 1 change not written\n") {
		t.Errorf("stderr = %q, want the singular count", errb.String())
	}
}

func TestEnvTrue(t *testing.T) {
	for v, want := range map[string]bool{"1": true, "true": true, " YES ": true, "On": true, "": false, "0": false, "false": false, "off": false, "maybe": false} {
		if got := envTrue(v); got != want {
			t.Errorf("envTrue(%q) = %v, want %v", v, got, want)
		}
	}
}
