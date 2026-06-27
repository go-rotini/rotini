package codegen

import (
	"fmt"
	"io/fs"
	"regexp"
	"strings"

	rotini "github.com/go-rotini/rotini/internal/runtime"
)

// runtimePackageClause matches the runtime files' `package rotini` clause (a whole
// line), the only edit emission makes to each file.
var runtimePackageClause = regexp.MustCompile(`(?m)^package rotini$`)

// emitRuntime returns the runtime source ready to write into a target package:
// every non-test runtime file with its `package rotini` clause rewritten to
// pkgName (a no-op when pkgName is "rotini"). The files reference each other
// unqualified (same package) and carry their own third-party imports, so the
// rewritten set is a self-contained Go package the generated framework imports.
func emitRuntime(pkgName string) (map[string][]byte, error) {
	src, err := runtimeSourceFiles()
	if err != nil {
		return nil, err
	}
	clause := []byte("package " + pkgName)
	out := make(map[string][]byte, len(src))
	for name, b := range src {
		rewritten, n := replacePackageClause(b, clause)
		if n != 1 {
			return nil, fmt.Errorf("emit runtime %s: expected exactly one `package rotini` clause, found %d", name, n)
		}
		out[name] = rewritten
	}
	return out, nil
}

// replacePackageClause rewrites the sole `package rotini` line to clause and
// reports how many it replaced (a guard against a malformed source file).
func replacePackageClause(src, clause []byte) ([]byte, int) {
	n := len(runtimePackageClause.FindAll(src, -1))
	if n == 0 {
		return src, 0
	}
	return runtimePackageClause.ReplaceAll(src, clause), n
}

// runtimeSourceFiles returns the emittable runtime source: every runtime .go file
// (from the internal/runtime package's own embedded [rotini.Source]), keyed by base
// filename, bytes verbatim — the package-clause rewrite happens at emit time. The
// runtime's test files and its embed.go (the self-embed support file) are excluded:
// neither is runtime behavior, so neither is shipped into user projects.
func runtimeSourceFiles() (map[string][]byte, error) {
	out := map[string][]byte{}
	entries, err := rotini.Source.ReadDir(".")
	if err != nil {
		return nil, err
	}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || strings.HasSuffix(name, "_test.go") || name == "embed.go" {
			continue
		}
		b, err := fs.ReadFile(rotini.Source, name)
		if err != nil {
			return nil, err
		}
		out[name] = b
	}
	return out, nil
}
