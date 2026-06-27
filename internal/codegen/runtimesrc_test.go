package codegen

import (
	"bytes"
	"go/parser"
	"go/token"
	"testing"
)

// TestMergeRuntime_singlePackage proves the runtime merges into ONE valid Go file
// under the requested package — not a directory of per-file copies.
func TestMergeRuntime_singlePackage(t *testing.T) {
	const pkg = "acme"
	merged, err := mergeRuntime(pkg)
	if err != nil {
		t.Fatal(err)
	}
	if len(merged) == 0 {
		t.Fatal("mergeRuntime returned empty output")
	}

	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "runtime.go", merged, parser.ParseComments)
	if err != nil {
		t.Fatalf("merged runtime does not parse as a single file: %v", err)
	}
	if f.Name.Name != pkg {
		t.Errorf("merged package = %q, want %q", f.Name.Name, pkg)
	}

	// Core + service identifiers from across the formerly-separate files must all
	// survive in the one merged file.
	for _, want := range []string{"NewProgram", "func NewParser", "func NewBinder", "func NewSuggestor", "func NewVersioner", "type Context"} {
		if !bytes.Contains(merged, []byte(want)) {
			t.Errorf("merged runtime missing %q", want)
		}
	}
}

// TestMergeRuntime_noTestContent proves the runtime's test files do not leak into the
// merged output (runtimeSourceFiles excludes them).
func TestMergeRuntime_noTestContent(t *testing.T) {
	merged, err := mergeRuntime("rotini")
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(merged, []byte("func Test")) {
		t.Error("a _test.go function leaked into the merged runtime")
	}
}
