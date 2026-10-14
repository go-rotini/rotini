package codegen

import (
	"path/filepath"
	"testing"
)

// TestHookLocation pins that a hook's file is written into the spec without a machine's own
// directories: relative to the module root, as module@version/path in the module cache, under
// "~/" elsewhere in the home directory, and relative ("../") outside it.
func TestHookLocation(t *testing.T) {
	base := t.TempDir()
	home := filepath.Join(base, "home", "me")
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("GOMODCACHE", filepath.Join(home, "go", "pkg", "mod"))
	root := filepath.Join(home, "src", "app")
	for file, want := range map[string]string{
		filepath.Join(root, "cmd", "root.go"): "cmd/root.go",
		filepath.Join(home, "go", "pkg", "mod", "github.com", "!acme", "lib@v1.2.0", "cmd", "root.go"): "github.com/Acme/lib@v1.2.0/cmd/root.go",
		filepath.Join(home, "src", "lib", "cmd", "root.go"):                                            "~/src/lib/cmd/root.go",
		filepath.Join(base, "work", "lib", "root.go"):                                                  "../../../../work/lib/root.go",
		"cmd/root.go": "cmd/root.go",
		"":            "",
	} {
		if got := hookLocation(root, file); got != want {
			t.Errorf("hookLocation(%q) = %q, want %q", file, got, want)
		}
	}
	if got := hookLocation("", filepath.Join(home, "lib", "cmd.go")); got != "~/lib/cmd.go" {
		t.Errorf("with no module root: %q, want ~/lib/cmd.go", got)
	}
}
