//go:build !mutation

package rotini

import (
	"bufio"
	"os"
	"os/exec"
	"slices"
	"strings"
	"testing"
)

// forbiddenDeps reads testdata/forbidden_deps.txt, one import path per line.
func forbiddenDeps(t *testing.T, path string) []string {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var out []string
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		if line := strings.TrimSpace(sc.Text()); line != "" && !strings.HasPrefix(line, "#") {
			out = append(out, line)
		}
	}
	if err := sc.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

// TestRuntimeDependencies checks that rotini's runtime package links none of the forbidden
// packages on any of the platforms rotini supports.
func TestRuntimeDependencies(t *testing.T) {
	if testing.Short() {
		t.Skip("runs go list; skipped under -short")
	}
	forbidden := forbiddenDeps(t, "testdata/forbidden_deps.txt")
	for _, goos := range []string{"linux", "darwin", "windows"} {
		t.Run(goos, func(t *testing.T) {
			cmd := exec.Command("go", "list", "-deps", ".")
			cmd.Env = append(os.Environ(), "GOOS="+goos)
			out, err := cmd.Output()
			if err != nil {
				t.Fatalf("go list -deps: %v", err)
			}
			deps := strings.Fields(string(out))
			for _, pkg := range forbidden {
				if slices.Contains(deps, pkg) {
					t.Errorf("the runtime links %s", pkg)
				}
			}
		})
	}
}
