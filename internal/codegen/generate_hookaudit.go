package codegen

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Generate-time audit of the handler files rotini does not own.
//
// A handler embeds the Default* no-ops and overrides the hooks it wants. If an override's
// NAME is wrong, the embedded no-op keeps satisfying Handlers and the author's method becomes
// dead code: it compiles, `go vet` is clean, and staticcheck's unused is deliberately
// conservative about exported methods, so nothing reports it. The stub's
// `var _ rotini.Handlers` assertion catches a missing hook and a drifted signature, but it
// cannot catch this one — the embed still supplies a valid method, so the interface holds.
//
// This is the remaining gap, and it is reported where every other "you wrote something that
// will not do what you meant" problem in rotini is: at generate time, with a file:line, as a
// warning rather than an error, because an unused method is legal Go.

// auditedHooks are the hook names a near-miss is measured against.
//
// Run is deliberately ABSENT. There is no DefaultRun, so a misspelled Run leaves the type
// without one and the assertion fails to compile — the mistake is already loud. Only the four
// hooks with an embeddable no-op can be typo'd silently, and leaving Run out also keeps the
// audit away from a three-letter target, where an edit distance of 2 would match ordinary
// helper names.
var auditedHooks = []string{"CascadingPreRun", "PreRun", "PostRun", "CascadingPostRun"}

// auditHooks reports methods on the cmd package's handler types whose names are near-misses
// of a lifecycle hook. It is the last generate step: by then every stub this pass created
// exists and every orphan is gone, so the audit sees exactly the files the author will build.
//
// It is BEST-EFFORT and never fails the pass. These are the author's files, possibly mid-edit;
// a file that will not parse is skipped, because the compiler is about to say so in better
// detail than this audit could.
func (p *program) auditHooks() error {
	// No cmd directory is nothing to audit, not a problem to report: the same answer
	// stubLooksGenerated gives when deciding whether to delete a file that is not there.
	// Anything else is a real filesystem failure and is surfaced, because a step that
	// silently does nothing is the class of bug this one exists to catch.
	entries, err := os.ReadDir(p.layout.cmdDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("read %s to audit its handler hooks: %w", p.layout.cmdDir, err)
	}

	fset := token.NewFileSet()
	files := make(map[string]*ast.File)
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		path := filepath.Join(p.layout.cmdDir, name)
		f, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
		if err != nil {
			continue
		}
		files[path] = f
	}

	handlerTypes := handlerTypeNames(files)
	if len(handlerTypes) == 0 {
		return nil
	}

	p.hookWarnings = nearMissHooks(fset, files, handlerTypes, p.module.root)
	return nil
}

// handlerTypeNames collects the type names asserted to implement rotini.Handlers anywhere in
// the package — the `var _ rotini.Handlers = (*T)(nil)` line every stub is written with, which
// is also what identifies a generated stub for pruning (see stubMarker).
//
// Matching on the interface NAME rather than the qualified selector keeps this working when
// the runtime is imported under an alias. The value form is read loosely — (*T)(nil), T{} and
// &T{} all name T — so an author who rewrote the assertion by hand is still covered.
func handlerTypeNames(files map[string]*ast.File) map[string]bool {
	declared := map[string]bool{}
	for _, f := range files {
		for _, d := range f.Decls {
			gd, ok := d.(*ast.GenDecl)
			if !ok || gd.Tok != token.TYPE {
				continue
			}
			for _, s := range gd.Specs {
				if ts, ok := s.(*ast.TypeSpec); ok {
					declared[ts.Name.Name] = true
				}
			}
		}
	}

	asserted := map[string]bool{}
	for _, f := range files {
		for _, d := range f.Decls {
			gd, ok := d.(*ast.GenDecl)
			if !ok || gd.Tok != token.VAR {
				continue
			}
			for _, s := range gd.Specs {
				vs, ok := s.(*ast.ValueSpec)
				if !ok || !assertsHandlers(vs.Type) || len(vs.Values) == 0 {
					continue
				}
				ast.Inspect(vs.Values[0], func(n ast.Node) bool {
					if id, ok := n.(*ast.Ident); ok && declared[id.Name] {
						asserted[id.Name] = true
					}
					return true
				})
			}
		}
	}
	return asserted
}

// assertsHandlers reports whether a var's declared type is the Handlers interface, under any
// package qualifier (rotini.Handlers, rt.Handlers) or none (a dot import).
func assertsHandlers(t ast.Expr) bool {
	switch v := t.(type) {
	case *ast.SelectorExpr:
		return v.Sel != nil && v.Sel.Name == "Handlers"
	case *ast.Ident:
		return v.Name == "Handlers"
	}
	return false
}

// nearMissHooks returns one warning per method on a handler type whose name is within an edit
// distance of 2 of a hook name without being one. The threshold and the phrasing are
// closestName's and didYouMean's, shared with the spec lint rules that suggest a typo fix.
//
// Warnings are sorted by file then line so a pass is reproducible.
func nearMissHooks(fset *token.FileSet, files map[string]*ast.File, handlerTypes map[string]bool, moduleRoot string) []error {
	hook := map[string]bool{"Run": true}
	for _, h := range auditedHooks {
		hook[h] = true
	}

	type finding struct {
		file string
		line int
		msg  string
	}
	var found []finding

	for _, f := range files {
		for _, d := range f.Decls {
			fd, ok := d.(*ast.FuncDecl)
			if !ok || fd.Recv == nil || len(fd.Recv.List) == 0 || fd.Name == nil {
				continue
			}
			recv := receiverTypeName(fd.Recv.List[0].Type)
			if !handlerTypes[recv] {
				continue
			}
			name := fd.Name.Name
			if hook[name] {
				continue // the real thing
			}
			match := closestName(name, auditedHooks)
			if match == "" {
				continue
			}
			pos := fset.Position(fd.Name.Pos())
			rel := pos.Filename
			if r, err := filepath.Rel(moduleRoot, pos.Filename); err == nil {
				rel = filepath.ToSlash(r)
			}
			msg := fmt.Sprintf(
				"%s:%d: method %q on %s is not a lifecycle hook, so it will never run — rotini.Default%s is what supplies %s",
				rel, pos.Line, name, recv, match, match,
			)
			found = append(found, finding{file: rel, line: pos.Line, msg: didYouMean(msg, name, auditedHooks)})
		}
	}

	sort.Slice(found, func(i, j int) bool {
		if found[i].file != found[j].file {
			return found[i].file < found[j].file
		}
		return found[i].line < found[j].line
	})

	warnings := make([]error, 0, len(found))
	for _, f := range found {
		warnings = append(warnings, fmt.Errorf("%s", f.msg))
	}
	return warnings
}

// receiverTypeName is the bare type name of a method receiver: T, *T, or a generic T[…].
func receiverTypeName(t ast.Expr) string {
	switch v := t.(type) {
	case *ast.StarExpr:
		return receiverTypeName(v.X)
	case *ast.IndexExpr:
		return receiverTypeName(v.X)
	case *ast.IndexListExpr:
		return receiverTypeName(v.X)
	case *ast.Ident:
		return v.Name
	}
	return ""
}
