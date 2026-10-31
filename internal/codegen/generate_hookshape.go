package codegen

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// hookNames are the five lifecycle hooks a handler type declares.
var hookNames = []string{"CascadingPreRun", "PreRun", "Run", "PostRun", "CascadingPostRun"}

// hookShapes reports, for each type in dir that declares a lifecycle hook, whether its hooks
// return a result: an ErrorHandler, which the generated wiring wraps with
// rotini.FromErrorHandler. A type that declares no hook is absent, so a stub not written yet
// takes the conf's hook_returns. Unreadable files, files that don't parse, test files and
// files out of the build (a disabled stub) are skipped.
func hookShapes(dir string) map[string]bool {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	shapes := map[string]bool{}
	fset := token.NewFileSet()
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		src, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil || buildIgnored(src) {
			continue
		}
		f, err := parser.ParseFile(fset, name, src, parser.SkipObjectResolution)
		if err != nil {
			continue
		}
		for _, d := range f.Decls {
			fd, ok := d.(*ast.FuncDecl)
			if !ok || fd.Recv == nil || len(fd.Recv.List) != 1 || !slices.Contains(hookNames, fd.Name.Name) {
				continue
			}
			typ := receiverTypeName(fd.Recv.List[0].Type)
			if typ == "" {
				continue
			}
			returns := fd.Type.Results != nil && len(fd.Type.Results.List) > 0
			shapes[typ] = shapes[typ] || returns
		}
	}
	return shapes
}

// errorHooks reports whether handler type typ is an ErrorHandler: by its own methods when
// shapes knows it, else by the conf, which decides the stub generate is about to seed.
func errorHooks(shapes map[string]bool, typ string, confError bool) bool {
	if returns, known := shapes[typ]; known {
		return returns
	}
	return confError
}
