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

// leanRuntimeDeps turns on the failure in TestRuntimeDependencies. While it is false the test
// only logs the forbidden packages it finds: rotini's go.mod still requires go-rotini module
// releases that link them. Set it, and leanRuntimeDeps in e2e/e2e_test.go, to true in the change
// that requires releases free of them.
const leanRuntimeDeps = false

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
				if !slices.Contains(deps, pkg) {
					continue
				}
				if leanRuntimeDeps {
					t.Errorf("the runtime links %s", pkg)
				} else {
					t.Logf("the runtime links %s (not yet enforced)", pkg)
				}
			}
		})
	}
}
