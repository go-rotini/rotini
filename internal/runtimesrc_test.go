package internal

import (
	"go/parser"
	"go/token"
	"testing"
)

func TestEmitRuntime_rewritesPackageClause(t *testing.T) {
	const pkg = "acme"
	files, err := emitRuntime(pkg)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) == 0 {
		t.Fatal("emitRuntime returned no files")
	}
	// Sentinel runtime files must be present (and no test files).
	for _, must := range []string{"program.go", "context.go", "parser.go", "binder.go"} {
		if _, ok := files[must]; !ok {
			t.Errorf("emitted set missing %s", must)
		}
	}
	fset := token.NewFileSet()
	for name, b := range files {
		if got := len(name); got >= 8 && name[got-8:] == "_test.go" {
			t.Errorf("test file leaked into emitted runtime: %s", name)
		}
		f, err := parser.ParseFile(fset, name, b, parser.PackageClauseOnly)
		if err != nil {
			t.Fatalf("emitted %s does not parse: %v", name, err)
		}
		if f.Name.Name != pkg {
			t.Errorf("emitted %s has package %q, want %q", name, f.Name.Name, pkg)
		}
	}
}

func TestEmitRuntime_identityForRotini(t *testing.T) {
	// pkgName "rotini" must be a faithful no-op against the source bytes.
	src, err := runtimeSourceFiles()
	if err != nil {
		t.Fatal(err)
	}
	emitted, err := emitRuntime("rotini")
	if err != nil {
		t.Fatal(err)
	}
	for name, b := range src {
		if string(emitted[name]) != string(b) {
			t.Errorf("emitRuntime(\"rotini\") changed %s; want byte-identical to source", name)
		}
	}
}
