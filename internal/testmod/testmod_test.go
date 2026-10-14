package testmod

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"testing"
)

func TestReplaces(t *testing.T) {
	root := filepath.Join(t.TempDir(), "my repo")
	if err := os.Mkdir(root, 0o700); err != nil {
		t.Fatal(err)
	}
	// A local directory that is absolute on this system is kept as written; any other is relative
	// to the module, as the go command reads it. /src/yaml is not absolute on Windows.
	abs := "/src/yaml"
	if runtime.GOOS == "windows" {
		abs = "C:/src/yaml"
	}
	gomod := "module github.com/go-rotini/rotini\n\ngo 1.27\n\nrequire github.com/go-rotini/toml v1.2.0\n\n" +
		"replace github.com/go-rotini/toml => ../toml // the local tree\n\n" +
		"replace\tgithub.com/go-rotini/env => \"../my dir\"\n\n" +
		"replace (\n\tgithub.com/go-rotini/yaml v1.2.2 => " + abs + "\n\tgithub.com/go-rotini/fs => github.com/fork/fs v1.0.0\n)\n"
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte(gomod), 0o600); err != nil {
		t.Fatal(err)
	}
	toml := filepath.ToSlash(filepath.Join(root, "..", "toml"))
	myDir := filepath.ToSlash(filepath.Join(root, "..", "my dir"))
	want := []Replace{
		{Old: "github.com/go-rotini/toml", New: toml},
		{Old: "github.com/go-rotini/env", New: myDir},
		{Old: "github.com/go-rotini/yaml", OldVersion: "v1.2.2", New: abs},
		{Old: "github.com/go-rotini/fs", New: "github.com/fork/fs", NewVersion: "v1.0.0"},
	}
	if got := Replaces(root); !slices.Equal(got, want) {
		t.Errorf("Replaces = %q, want %q", got, want)
	}
	wantLines := "replace github.com/go-rotini/toml => " + Quote(toml) + "\n" +
		"replace github.com/go-rotini/env => " + Quote(myDir) + "\n" +
		"replace github.com/go-rotini/yaml v1.2.2 => " + abs + "\n" +
		"replace github.com/go-rotini/fs => github.com/fork/fs v1.0.0\n"
	if got := Directives(root); got != wantLines {
		t.Errorf("Directives =\n%s\nwant\n%s", got, wantLines)
	}

	// The lines and the flags each give a test module the same directives.
	mod := filepath.Join(t.TempDir(), "with lines")
	if err := os.MkdirAll(mod, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(mod, "go.mod"), []byte("module example.com/m\n\ngo 1.27\n\n"+Directives(root)), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := Replaces(mod); !slices.Equal(got, want) {
		t.Errorf("from Directives: %q, want %q", got, want)
	}
	if err := os.WriteFile(filepath.Join(mod, "go.mod"), []byte("module example.com/m\n\ngo 1.27\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("go", append([]string{"mod", "edit"}, EditFlags(root)...)...)
	cmd.Dir = mod
	cmd.Env = append(os.Environ(), "GOWORK=off", "GOFLAGS=")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("go mod edit %q: %v\n%s", EditFlags(root), err, out)
	}
	if got := Replaces(mod); !slices.Equal(got, want) {
		t.Errorf("from EditFlags: %q, want %q", got, want)
	}

	if got := Replaces(t.TempDir()); got != nil {
		t.Errorf("with no go.mod: %q", got)
	}
}

func TestQuote(t *testing.T) {
	for in, want := range map[string]string{
		"/src/yaml": "/src/yaml", "../my dir": `"../my dir"`, `C:\src`: `C:\src`, "a//b": `"a//b"`, "a,b": `"a,b"`, "": `""`,
	} {
		if got := Quote(in); got != want {
			t.Errorf("Quote(%q) = %s, want %s", in, got, want)
		}
	}
}
