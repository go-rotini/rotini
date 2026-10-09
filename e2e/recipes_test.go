//go:build !mutation

package e2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rogpeppe/go-internal/txtar"
)

// recipeBlocks pins each code block on the recipes page to the file an r12 script builds or
// compares, so the page shows code and output that are known to work.
var recipeBlocks = []struct{ title, script, file string }{
	{"internal/cmd/lazydemo/store.go", "r12_recipe_lazy_dep.txtar", "store.go.txt"},
	{"internal/cmd/lazydemo/lazydemo_get.go", "r12_recipe_lazy_dep.txtar", "get.go.txt"},
	{"internal/cmd/mandemo/mandemo_man.go", "r12_recipe_man_all.txtar", "man.go.txt"},
	{"$ taskr __complete list --status ''", "r12_recipe_complete_debug.txtar", "status.txt"},
	{"$ taskr __complete add ''", "r12_recipe_complete_debug.txtar", "title.txt"},
	{"$ taskr __complete list --out ''", "r12_recipe_complete_debug.txtar", "out.txt"},
	{"internal/cmd/tracedemo/tracedemo.go", "r12_recipe_otel.txtar", "root.go.txt"},
	{"internal/cmd/tracedemo/tracedemo_work.go", "r12_recipe_otel.txtar", "work.go.txt"},
}

func TestRecipeBlocksMatchScripts(t *testing.T) {
	page, err := os.ReadFile(filepath.Join("..", "docs", "content", "recipes", "_index.md"))
	if err != nil {
		t.Fatalf("read the recipes page: %v", err)
	}
	for _, b := range recipeBlocks {
		if n := strings.Count(string(page), `{{< code title="`+b.title+`"`); n != 1 {
			t.Errorf("the recipes page has %d blocks titled %q, want exactly 1", n, b.title)
			continue
		}
		want, _ := codeBlock(string(page), b.title)
		archive, err := txtar.ParseFile(filepath.Join("testdata", "script", b.script))
		if err != nil {
			t.Fatal(err)
		}
		found := false
		for _, f := range archive.Files {
			if f.Name != b.file {
				continue
			}
			found = true
			if got := strings.TrimRight(string(f.Data), "\n"); got != want {
				t.Errorf("%s's %s is not the page's %q block.\n--- script\n%s\n--- page\n%s", b.script, b.file, b.title, got, want)
			}
		}
		if !found {
			t.Errorf("%s carries no %s", b.script, b.file)
		}
	}
}
