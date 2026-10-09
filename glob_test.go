package rotini

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestExpandGlobs(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	for _, name := range []string{"b.txt", "a.txt", "file[1].txt", "c.log"} {
		if err := os.WriteFile(filepath.Join(dir, name), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	def := Definition{
		Name: "app", Handler: "App",
		Arguments: []ArgDef{
			{Name: "files", Type: "[]inputfile", Variadic: true, Glob: true},
			{Name: "dest", Type: "string"},
		},
	}
	chain := NewContextFor(def, nil).CommandChain()
	tests := []struct {
		name string
		goos string
		argv []string
		want []string
	}{
		{"a pattern expands, sorted", "windows", []string{"*.txt", "out"}, []string{"a.txt", "b.txt", "file[1].txt", "out"}},
		{"an existing name is kept", "windows", []string{"file[1].txt", "out"}, []string{"file[1].txt", "out"}},
		{"no match passes through", "windows", []string{"*.md", "out"}, []string{"*.md", "out"}},
		{"dash is kept", "windows", []string{"-", "*.log", "out"}, []string{"-", "c.log", "out"}},
		{"the fixed argument is not expanded", "windows", []string{"a.txt", "*.log"}, []string{"a.txt", "*.log"}},
		{"other systems leave patterns alone", "linux", []string{"*.txt", "out"}, []string{"*.txt", "out"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			store, err := parseArgvTokens(chain, tt.argv, argvAcq{dir: dir, goos: tt.goos})
			if err != nil {
				t.Fatal(err)
			}
			if got := store.scopes[0].args; !slices.Equal(got, tt.want) {
				t.Errorf("args = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestExpandGlob_relativeToTheRunDirectory(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "src", "main.go"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if got, want := expandGlob(dir, filepath.Join("src", "*.go")), []string{filepath.Join("src", "main.go")}; !slices.Equal(got, want) {
		t.Errorf("expandGlob = %q, want %q", got, want)
	}
	abs := filepath.Join(dir, "src", "*.go")
	if got, want := expandGlob(dir, abs), []string{filepath.Join(dir, "src", "main.go")}; !slices.Equal(got, want) {
		t.Errorf("an absolute pattern = %q, want %q", got, want)
	}
}
