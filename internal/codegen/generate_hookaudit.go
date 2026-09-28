package codegen

import (
	"errors"
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

// auditHooks reports methods on a handler type whose names are near-misses of a lifecycle hook.
// It is the last generate step: by then every stub this pass created exists and every orphan is
// gone, so the audit sees exactly the files the author will build.
//
// It covers the cmd package AND every package a spec's `handler:` block points at that lives in
// this module — the bring-your-own seam, where a Handlers implementation is written by hand and
// shared by several CLIs, and therefore the place a misspelled hook is LEAST likely to be noticed.
// A handler package outside this module is skipped deliberately: that is a dependency's source,
// and linting someone else's package is not this tool's business.
//
// It is BEST-EFFORT and never fails the pass for a package it cannot read. These are the
// author's files, possibly mid-edit; a file that will not parse is skipped, because the compiler
// is about to say so in better detail than this audit could.
func (p *program) auditHooks() error {
	dirs, err := p.auditDirs()
	if err != nil {
		return err
	}

	fset := token.NewFileSet()
	files := make(map[string]*ast.File)
	for _, dir := range dirs {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue // a directory that is not there is nothing to audit
		}
		for _, e := range entries {
			name := e.Name()
			if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
				continue
			}
			f, err := parser.ParseFile(fset, filepath.Join(dir, name), nil, parser.SkipObjectResolution)
			if err != nil {
				continue
			}
			files[filepath.Join(dir, name)] = f
		}
	}
	if len(files) == 0 {
		return nil
	}

	handlerTypes := handlerTypeNames(files)
	if len(handlerTypes) == 0 {
		return nil
	}

	p.hookWarnings = nearMissHooks(fset, files, handlerTypes, p.module.root)

	expected, known := p.inputsTypeExpectations()
	p.hookWarnings = append(p.hookWarnings, wrongInputsTypes(fset, files, expected, known, p.module.root)...)
	return nil
}

// noteHandlerImport records a Go import path a spec's `handler:` block names, for auditHooks.
func (p *program) noteHandlerImport(importPath string) {
	importPath = strings.TrimSpace(importPath)
	if importPath == "" {
		return
	}
	if p.handlerImports == nil {
		p.handlerImports = map[string]bool{}
	}
	p.handlerImports[importPath] = true
}

// auditDirs is the cmd package plus each in-module handler package, deduped and ordered so a
// pass is reproducible.
//
// The module check is what makes resolving an import path to a directory possible at all: a path
// under this module's own path maps to a directory beneath its root by string surgery, with no
// build list and no module cache to consult. Anything else is another module's code.
func (p *program) auditDirs() ([]string, error) {
	seen := map[string]bool{}
	var dirs []string
	add := func(d string) {
		if d != "" && !seen[d] {
			seen[d] = true
			dirs = append(dirs, d)
		}
	}

	// The cmd directory must exist by now — the stubs were just written — so unlike a handler
	// package, a failure to read it is a real filesystem problem and is surfaced.
	if _, err := os.Stat(p.layout.cmdDir); err != nil {
		if !os.IsNotExist(err) {
			return nil, fmt.Errorf("read %s to audit its handler hooks: %w", p.layout.cmdDir, err)
		}
	} else {
		add(p.layout.cmdDir)
	}

	for imp := range p.handlerImports {
		if dir, ok := inModuleDir(imp, p.module); ok {
			add(dir)
		}
	}
	sort.Strings(dirs)
	return dirs, nil
}

// inModuleDir maps a Go import path to a directory inside m, reporting false when the path
// belongs to another module.
func inModuleDir(importPath string, m module) (string, bool) {
	if m.path == "" || m.root == "" {
		return "", false
	}
	switch {
	case importPath == m.path:
		return m.root, true
	case strings.HasPrefix(importPath, m.path+"/"):
		rel := strings.TrimPrefix(importPath, m.path+"/")
		return filepath.Join(m.root, filepath.FromSlash(rel)), true
	}
	return "", false
}

// handlerTypeNames collects the type names this package declares that implement rotini.Handlers,
// found two ways:
//
//  1. The `var _ rotini.Handlers = (*T)(nil)` assertion every generated stub carries, which is
//     also what identifies a stub for pruning (see stubMarker).
//  2. Any function RETURNING rotini.Handlers, through the types its body names.
//
// The second is what reaches a hand-written handler package. The `handler: {import, convention}`
// seam is a package exporting `func Health() rotini.Handlers { return &handlers{} }` — the
// convention function's return type is the proof it implements the interface, so such a package
// has no reason to write the assertion as well, and the real ones do not. Finding types only
// through the assertion would have audited every generated stub and none of the files the seam
// exists for.
//
// Matching on the interface NAME rather than the qualified selector keeps this working when the
// runtime is imported under an alias. Value forms are read loosely — (*T)(nil), T{} and &T{} all
// name T — so a hand-rewritten assertion, and a constructor with a few branches, are covered.
func handlerTypeNames(files map[string]*ast.File) map[string]bool {
	declared := declaredTypeNames(files)
	found := map[string]bool{}
	collect := func(n ast.Node) {
		ast.Inspect(n, func(n ast.Node) bool {
			if id, ok := n.(*ast.Ident); ok && declared[id.Name] {
				found[id.Name] = true
			}
			return true
		})
	}

	for _, f := range files {
		for _, d := range f.Decls {
			switch d := d.(type) {
			case *ast.GenDecl:
				collectAssertedTypes(d, collect)
			case *ast.FuncDecl:
				collectConstructedTypes(d, collect)
			}
		}
	}
	return found
}

// declaredTypeNames is every type name the package declares, so the two scans below can tell a
// local type from an imported identifier that happens to appear in the same expression.
func declaredTypeNames(files map[string]*ast.File) map[string]bool {
	declared := map[string]bool{}
	for _, f := range files {
		for _, d := range f.Decls {
			gd, ok := d.(*ast.GenDecl)
			if !ok || gd.Tok != token.TYPE {
				continue
			}
			for _, sp := range gd.Specs {
				if ts, ok := sp.(*ast.TypeSpec); ok {
					declared[ts.Name.Name] = true
				}
			}
		}
	}
	return declared
}

// collectAssertedTypes reads `var _ rotini.Handlers = (*T)(nil)` — the generated stub's form.
func collectAssertedTypes(gd *ast.GenDecl, collect func(ast.Node)) {
	if gd.Tok != token.VAR {
		return
	}
	for _, sp := range gd.Specs {
		vs, ok := sp.(*ast.ValueSpec)
		if !ok || !assertsHandlers(vs.Type) || len(vs.Values) == 0 {
			continue
		}
		collect(vs.Values[0])
	}
}

// collectConstructedTypes reads `func Check() rotini.Handlers { return &handlers{} }` — the
// hand-written handler package's form, where the return type is the proof and no assertion is
// written.
func collectConstructedTypes(fd *ast.FuncDecl, collect func(ast.Node)) {
	if !returnsHandlers(fd) || fd.Body == nil {
		return
	}
	for _, st := range fd.Body.List {
		ret, ok := st.(*ast.ReturnStmt)
		if !ok {
			continue
		}
		for _, r := range ret.Results {
			collect(r)
		}
	}
}

// returnsHandlers reports whether fn's signature returns the Handlers interface — the proof a
// hand-written handler package gives that its type implements it, in place of an assertion.
func returnsHandlers(fn *ast.FuncDecl) bool {
	if fn.Type == nil || fn.Type.Results == nil {
		return false
	}
	for _, r := range fn.Type.Results.List {
		if assertsHandlers(r.Type) {
			return true
		}
	}
	return false
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

// ─── the inputs-type audit ──────────────────────────────────────────────────.

// collectFuncs are the generic entry points that acquire a command's own declared inputs. Each
// takes the inputs type as its type argument, which is what the audit reads.
var collectFuncs = map[string]bool{
	"Collect": true, "CollectP": true,
	"Defaults": true, "ParseArgv": true, "ParseEnv": true, "ParseFiles": true, "ParseStdin": true,
}

// inputsTypeExpectations maps each generated handler type to the inputs type its command owns,
// alongside the set of every inputs type this package generates.
//
// The pair is the whole check. An inputs struct binds to the frame whose hook is running, so a
// handler can only mean its OWN command's type; collecting an ancestor's is a silent wrong
// answer whenever the two commands share a flag name, which cascading flags guarantee. The
// runtime cannot tell them apart — it sees a struct that fits — but codegen can, because it
// knows which command each stub implements.
//
// That is why this is a generate-time check and not a runtime one. The runtime approach needs a
// frame to carry a grafted command's origin identity, which nothing does: a composed child is
// renamed by the umbrella that mounts it. Here the question never crosses a package, so renaming
// and composition simply do not arise.
func (p *program) inputsTypeExpectations() (expected map[string]string, known map[string]bool) {
	expected, known = map[string]string{}, map[string]bool{}
	note := func(c genCommand) {
		if c.handler == "" || c.prefix == "" {
			return
		}
		in := c.prefix + "Inputs"
		expected[c.handler] = in
		known[in] = true
	}
	note(p.root)
	for _, c := range p.own {
		note(c)
	}
	return expected, known
}

// wrongInputsTypes reports each call that acquires a DIFFERENT generated command's inputs type
// than the handler it sits in owns.
//
// An unrecognized type argument is ignored on purpose: a hand-written handler package declares
// its own struct (see the `handler:` seam), and that is a supported shape, not a mistake.
func wrongInputsTypes(fset *token.FileSet, files map[string]*ast.File, expected map[string]string, known map[string]bool, moduleRoot string) []error {
	type finding struct {
		file string
		line int
		msg  string
	}
	var found []finding

	for _, f := range files {
		for _, d := range f.Decls {
			fd, ok := d.(*ast.FuncDecl)
			if !ok || fd.Recv == nil || len(fd.Recv.List) == 0 || fd.Body == nil {
				continue
			}
			recv := receiverTypeName(fd.Recv.List[0].Type)
			want, isHandler := expected[recv]
			if !isHandler {
				continue
			}
			ast.Inspect(fd.Body, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				fn, arg, ok := genericCallTypeArg(call)
				if !ok || !collectFuncs[fn] || arg == want || !known[arg] {
					return true
				}
				pos := fset.Position(call.Pos())
				rel := pos.Filename
				if r, err := filepath.Rel(moduleRoot, pos.Filename); err == nil {
					rel = filepath.ToSlash(r)
				}
				found = append(found, finding{file: rel, line: pos.Line, msg: fmt.Sprintf(
					"%s:%d: %s in %s acquires %s, but this handler implements the command whose inputs are %s. "+
						"An inputs type binds to the command whose hook is running, so another command's type "+
						"reads THIS command's frame through the wrong shape — silently, when the two share a flag name",
					rel, pos.Line, fn, recv, arg, want)})
				return true
			})
		}
	}

	sort.Slice(found, func(i, j int) bool {
		if found[i].file != found[j].file {
			return found[i].file < found[j].file
		}
		return found[i].line < found[j].line
	})
	out := make([]error, 0, len(found))
	for _, f := range found {
		out = append(out, errors.New(f.msg))
	}
	return out
}

// genericCallTypeArg reads `pkg.Fn[Type](…)` — or `Fn[Type](…)` — returning the function name
// and the single type argument's name.
func genericCallTypeArg(call *ast.CallExpr) (fn, arg string, ok bool) {
	idx, isIndex := call.Fun.(*ast.IndexExpr)
	if !isIndex {
		return "", "", false
	}
	switch f := idx.X.(type) {
	case *ast.SelectorExpr:
		fn = f.Sel.Name
	case *ast.Ident:
		fn = f.Name
	default:
		return "", "", false
	}
	id, isIdent := idx.Index.(*ast.Ident)
	if !isIdent {
		return "", "", false
	}
	return fn, id.Name, true
}
