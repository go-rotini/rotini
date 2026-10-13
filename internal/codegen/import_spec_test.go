package codegen

import (
	"os"
	"path/filepath"
	"testing"
)

// TestHookLocation pins that a hook's file is written into the spec without a machine's own
// directories: relative to the module root, with "../" for one outside it.
func TestHookLocation(t *testing.T) {
	t.Parallel()
	root := filepath.Join(string(filepath.Separator)+"work", "app")
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip(err)
	}
	for file, want := range map[string]string{
		filepath.Join(root, "cmd", "root.go"):              "cmd/root.go",
		filepath.Join(root, "..", "lib", "cmd", "root.go"): "../lib/cmd/root.go",
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
