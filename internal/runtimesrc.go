package internal

import (
	"embed"
	"fmt"
	"io/fs"
	"regexp"
	"strings"
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

// runtimeFS embeds the rotini runtime SOURCE (the internal/runtime package). On
// `rotini generate`, these files are EMITTED into the user's generated package —
// with their `package rotini` clause rewritten to the target package — so a built
// CLI carries its own runtime and never imports go-rotini/rotini at run time.
//
//go:embed runtime/*.go
var runtimeFS embed.FS

// runtimeSourceFiles returns the emittable runtime source: every non-test .go
// file under internal/runtime, keyed by base filename, with bytes verbatim (the
// package-clause rewrite happens at emit time). Test files (_test.go) are
// excluded — the runtime's own tests are not shipped into user projects.
func runtimeSourceFiles() (map[string][]byte, error) {
	out := map[string][]byte{}
	entries, err := runtimeFS.ReadDir("runtime")
	if err != nil {
		return nil, err
	}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || strings.HasSuffix(name, "_test.go") {
			continue
		}
		b, err := fs.ReadFile(runtimeFS, "runtime/"+name)
		if err != nil {
			return nil, err
		}
		out[name] = b
	}
	return out, nil
}
