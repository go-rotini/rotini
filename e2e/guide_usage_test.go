//go:build !mutation

package e2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rogpeppe/go-internal/txtar"
)

// TestUsageRecipeMatchesTheGuide pins the guide's usage-line reporter to the file r3_usage
// builds and runs.
func TestUsageRecipeMatchesTheGuide(t *testing.T) {
	const title = "internal/cmd/todo/report_usage.go"
	page, err := os.ReadFile(filepath.Join("..", "docs", "content", "docs", "_index.md"))
	if err != nil {
		t.Fatalf("read the guide: %v", err)
	}
	want, ok := codeBlock(string(page), title)
	if !ok {
		t.Fatalf("the guide no longer shows a %q block", title)
	}
	archive, err := txtar.ParseFile(filepath.Join("testdata", "script", "r3_usage.txtar"))
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range archive.Files {
		if f.Name == title {
			if got := strings.TrimRight(string(f.Data), "\n"); got != want {
				t.Errorf("r3_usage's %s is not the guide's block.\n--- script\n%s\n--- guide\n%s", title, got, want)
			}
			return
		}
	}
	t.Errorf("r3_usage carries no %s", title)
}
