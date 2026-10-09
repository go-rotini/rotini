//go:build !mutation

package shape

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// importers maps each package in pkg's dependency graph to the packages there that import it.
func importers(t *testing.T, pkg string) map[string][]string {
	t.Helper()
	out, err := exec.Command("go", "list", "-deps", "-f", `{{.ImportPath}}{{range .Imports}} {{.}}{{end}}`, pkg).Output()
	if err != nil {
		t.Fatalf("go list -deps %s: %v", pkg, err)
	}
	by := map[string][]string{}
	for line := range strings.Lines(string(out)) {
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		if _, ok := by[fields[0]]; !ok {
			by[fields[0]] = nil
		}
		for _, imp := range fields[1:] {
			by[imp] = append(by[imp], fields[0])
		}
	}
	return by
}

// TestCoreLinksNoTemplates checks that the rotini package links no template code of its own and
// does not import this package, so only programs that import shape link text/template. The
// go-rotini/fs module is the one importer allowed, until the root package's
// TestRuntimeDependencies forbids it too.
func TestCoreLinksNoTemplates(t *testing.T) {
	if testing.Short() {
		t.Skip("runs go list; skipped under -short")
	}
	deps := importers(t, "github.com/go-rotini/rotini")
	if _, ok := deps["github.com/go-rotini/rotini/shape"]; ok {
		t.Error("the rotini package depends on rotini/shape")
	}
	for _, imp := range deps["text/template"] {
		if !strings.HasPrefix(imp, "text/template") && !strings.HasPrefix(imp, "github.com/go-rotini/fs") {
			t.Errorf("%s imports text/template into the rotini package", imp)
		}
	}
}

func TestShapeLinksTemplates(t *testing.T) {
	if testing.Short() {
		t.Skip("runs go list; skipped under -short")
	}
	if _, ok := importers(t, ".")["text/template"]; !ok {
		t.Error("go list -deps ./shape does not list text/template")
	}
}

// TestLeanRuntimeFixtureSkipsShape keeps the lean-runtime e2e fixture, whose linked packages and
// linker output are checked for what text/template would bring in, free of this package.
func TestLeanRuntimeFixtureSkipsShape(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("..", "e2e", "testdata", "script", "r11_lean_runtime.txtar"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "rotini/shape") || strings.Contains(string(b), "text/template") {
		t.Error("r11_lean_runtime's fixture imports rotini/shape or text/template")
	}
	list, err := os.ReadFile(filepath.Join("..", "testdata", "forbidden_deps.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(strings.Fields(string(list)), "text/template") {
		t.Error("testdata/forbidden_deps.txt no longer forbids text/template")
	}
}
