package testmod

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestReplaces(t *testing.T) {
	root := t.TempDir()
	gomod := `module github.com/go-rotini/rotini

go 1.27

require github.com/go-rotini/toml v1.2.0

replace github.com/go-rotini/toml => ../toml // the local tree

replace (
	github.com/go-rotini/yaml v1.2.2 => /src/yaml
	github.com/go-rotini/fs => github.com/fork/fs v1.0.0
)
`
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte(gomod), 0o600); err != nil {
		t.Fatal(err)
	}
	toml := filepath.ToSlash(filepath.Join(root, "..", "toml"))
	want := []Replace{
		{"github.com/go-rotini/toml", toml},
		{"github.com/go-rotini/yaml v1.2.2", "/src/yaml"},
		{"github.com/go-rotini/fs", "github.com/fork/fs v1.0.0"},
	}
	if got := Replaces(root); !slices.Equal(got, want) {
		t.Errorf("Replaces = %q, want %q", got, want)
	}
	wantLines := "replace github.com/go-rotini/toml => " + toml + "\n" +
		"replace github.com/go-rotini/yaml v1.2.2 => /src/yaml\n" +
		"replace github.com/go-rotini/fs => github.com/fork/fs v1.0.0\n"
	if got := Directives(root); got != wantLines {
		t.Errorf("Directives =\n%s\nwant\n%s", got, wantLines)
	}
	wantFlags := []string{
		"-replace=github.com/go-rotini/toml=" + toml,
		"-replace=github.com/go-rotini/yaml@v1.2.2=/src/yaml",
		"-replace=github.com/go-rotini/fs=github.com/fork/fs@v1.0.0",
	}
	if got := EditFlags(root); !slices.Equal(got, wantFlags) {
		t.Errorf("EditFlags = %q, want %q", got, wantFlags)
	}
	if got := Replaces(t.TempDir()); got != nil {
		t.Errorf("with no go.mod: %q", got)
	}
}
