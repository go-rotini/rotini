package rotini

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/go-rotini/rotini/internal/codegen"
)

func TestCLI_fmt(t *testing.T) {
	tests := []struct {
		name    string
		argv    []string
		changed []string
		err     error
		code    int
		stdout  string
		stderr  string
	}{
		{"nothing to do", []string{"fmt", "a.yaml"}, nil, nil, 0, "", ""},
		{"formats", []string{"fmt", "a.yaml", "b.yaml"}, []string{"b.yaml"}, nil, 0, "formatted b.yaml\n", ""},
		{"check, formatted", []string{"fmt", "--check", "a.yaml"}, nil, nil, 0, "", ""},
		{"check, one file", []string{"fmt", "--check", "a.yaml"}, []string{"a.yaml"}, nil, 2, "",
			"would reformat a.yaml\ncheck: 1 file not formatted\n"},
		{"check, two files", []string{"fmt", "--check", "a.yaml", "b.yaml"}, []string{"a.yaml", "b.yaml"}, nil, 2, "",
			"would reformat a.yaml\nwould reformat b.yaml\ncheck: 2 files not formatted\n"},
		{"an error beats a change", []string{"fmt", "--check", "a.yaml", "c.json"}, []string{"a.yaml"},
			errors.New("c.json: formatting JSON files isn't supported yet"), 1, "",
			"would reformat a.yaml\nError: c.json: formatting JSON files isn't supported yet\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			prog, out, errb := newTestCLI(t)
			var gotFiles []string
			var gotCheck bool
			prog.WithDependency(formatDep, codegen.FormatFn(func(files []string, kind string, check bool) ([]string, error) {
				gotFiles, gotCheck = files, check
				return tt.changed, tt.err
			}))
			code, _ := prog.Run(tt.argv)
			if code != tt.code {
				t.Errorf("exit = %d, want %d (stderr %q)", code, tt.code, errb.String())
			}
			if out.String() != tt.stdout || errb.String() != tt.stderr {
				t.Errorf("stdout %q, stderr %q; want %q, %q", out.String(), errb.String(), tt.stdout, tt.stderr)
			}
			if wantCheck := slices.Contains(tt.argv, "--check"); gotCheck != wantCheck {
				t.Errorf("check = %v", gotCheck)
			}
			if !slices.Equal(gotFiles, tt.argv[len(tt.argv)-len(gotFiles):]) {
				t.Errorf("files = %v", gotFiles)
			}
		})
	}
}

func TestCLI_fmtDiscovers(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{".rotini.spec.yaml", ".rotini.conf.yaml"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("version: 0.0.0\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	t.Chdir(dir)
	prog, out, _ := newTestCLI(t)
	var gotFiles []string
	var gotKind string
	prog.WithDependency(formatDep, codegen.FormatFn(func(files []string, kind string, _ bool) ([]string, error) {
		gotFiles, gotKind = files, kind
		return nil, nil
	}))
	if code, _ := prog.Run([]string{"fmt", "--kind", "spec"}); code != 0 {
		t.Fatalf("exit = %d", code)
	}
	if len(gotFiles) != 2 || filepath.Base(gotFiles[0]) != ".rotini.spec.yaml" || filepath.Base(gotFiles[1]) != ".rotini.conf.yaml" || gotKind != "spec" {
		t.Errorf("files %v, kind %q", gotFiles, gotKind)
	}
	if want := "spec: .rotini.spec.yaml\nconf: .rotini.conf.yaml\n"; out.String() != want {
		t.Errorf("stdout = %q, want %q", out.String(), want)
	}
}

func TestCLI_fmtRealFiles(t *testing.T) {
	dir := t.TempDir()
	spec := filepath.Join(dir, "app.yaml")
	if err := os.WriteFile(spec, []byte("command:\n    summary: s\n    name: app\nversion: 0.0.0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	prog, out, _ := newTestCLI(t)
	if code, _ := prog.Run([]string{"fmt", spec}); code != 0 {
		t.Fatalf("exit = %d", code)
	}
	if out.String() != "formatted "+spec+"\n" {
		t.Errorf("stdout = %q", out.String())
	}
	got, _ := os.ReadFile(spec)
	if want := "version: 0.0.0\ncommand:\n  name: app\n  summary: s\n"; string(got) != want {
		t.Errorf("file:\n%s", got)
	}
}
