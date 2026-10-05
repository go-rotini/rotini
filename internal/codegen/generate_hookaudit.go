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

// Generate-time audit of the author's handler code.
//
// A handler embeds the No* no-ops and overrides the hooks it wants. A misspelled override is
// dead code: the embedded no-op still satisfies Handler, so the `var _ rotini.Handler`
// assertion holds and neither the compiler nor vet reports it. The audit reports such methods
// as warnings with a file:line, since an unused method is legal Go.
//
// The same pass also reports a handler that acquires a different command's generated inputs
// type (see wrongInputsTypes), which compiles but reads the wrong shape at run time.

// auditedHooks are the hook names a near-miss is measured against. Run is excluded: it has no
// no-op, so a misspelled Run already fails the assertion, and a three-letter target would match
// ordinary helper names within edit distance 2.
var auditedHooks = []string{"CascadingPreRun", "PreRun", "PostRun", "CascadingPostRun"}

// auditHooks records warnings for near-miss hook names on handler types and for calls that
// acquire another command's inputs type. It runs last in a generate pass, so it sees the final
// set of stubs.
//
// It covers the cmd package and every in-module package named by a spec's `handler:` block;
// handler packages in other modules are skipped. It is best-effort: unreadable directories and
// files that do not parse are skipped, leaving the compiler to report them.
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
			continue
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

	p.auditWarnings = nearMissHooks(fset, files, handlerTypes, p.module.root)

	expected, known := p.inputsTypeExpectations()
	p.auditWarnings = append(p.auditWarnings, wrongInputsTypes(fset, files, expected, known, p.module.root)...)
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

// auditDirs returns the cmd package directory plus each in-module handler package directory,
// deduplicated and sorted.
func (p *program) auditDirs() ([]string, error) {
	seen := map[string]bool{}
	var dirs []string
	add := func(d string) {
		if d != "" && !seen[d] {
			seen[d] = true
			dirs = append(dirs, d)
		}
	}

	// Unlike a handler package, a cmd directory that exists but cannot be read is an error.
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

// inModuleDir maps a Go import path under m's path to its directory beneath m's root,
// reporting false when the path belongs to another module.
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

// handlerTypeNames collects the declared type names that implement rotini.Handler, found two
// ways:
//
//  1. The `var _ rotini.Handler = (*T)(nil)` assertion every generated stub carries.
//  2. The types named in the return statements of any function returning rotini.Handler, as a
//     hand-written handler package does (`func Health() rotini.Handler { return &handlers{} }`)
//     without an assertion.
//
// The interface is matched by name, not qualifier, so an aliased import still matches. Value
// forms are read loosely: (*T)(nil), T{} and &T{} all name T.
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

// declaredTypeNames returns every type name declared in files, so the handler scans can tell a
// local type from an imported identifier.
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

// collectAssertedTypes reads the generated stub's `var _ rotini.Handler = (*T)(nil)`.
func collectAssertedTypes(gd *ast.GenDecl, collect func(ast.Node)) {
	if gd.Tok != token.VAR {
		return
	}
	for _, sp := range gd.Specs {
		vs, ok := sp.(*ast.ValueSpec)
		if !ok || !assertsHandler(vs.Type) || len(vs.Values) == 0 {
			continue
		}
		collect(vs.Values[0])
	}
}

// collectConstructedTypes reads the return statements of a function such as
// `func Check() rotini.Handler { return &handlers{} }`.
func collectConstructedTypes(fd *ast.FuncDecl, collect func(ast.Node)) {
	if !returnsHandler(fd) || fd.Body == nil {
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

// returnsHandler reports whether fn's signature returns the Handler interface.
func returnsHandler(fn *ast.FuncDecl) bool {
	if fn.Type == nil || fn.Type.Results == nil {
		return false
	}
	for _, r := range fn.Type.Results.List {
		if assertsHandler(r.Type) {
			return true
		}
	}
	return false
}

// assertsHandler reports whether t names the Handler interface, under any
// package qualifier (rotini.Handler, rt.Handler) or none (a dot import).
func assertsHandler(t ast.Expr) bool {
	switch v := t.(type) {
	case *ast.SelectorExpr:
		return v.Sel != nil && v.Sel.Name == "Handler"
	case *ast.Ident:
		return v.Name == "Handler"
	}
	return false
}

// nearMissHooks returns one warning per method on a handler type whose name is within an edit
// distance of 2 of a hook name without being one, sorted by file then line. The threshold and
// phrasing come from closestName and didYouMean, shared with the spec lint rules.
func nearMissHooks(fset *token.FileSet, files map[string]*ast.File, handlerTypes map[string]bool, moduleRoot string) []error {
	hook := map[string]bool{"Run": true}
	for _, h := range auditedHooks {
		hook[h] = true
	}

	var found []auditFinding

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
				continue
			}
			match := closestName(name, auditedHooks)
			if match == "" {
				continue
			}
			at := findingAt(fset, fd.Name.Pos(), moduleRoot)
			at.msg = didYouMean(fmt.Sprintf(
				"%s:%d: method %q on %s is not a lifecycle hook, so it will never run; rotini.No%s is what supplies %s",
				at.file, at.line, name, recv, match, match,
			), name, auditedHooks)
			found = append(found, at)
		}
	}

	return sortedWarnings(found)
}

// receiverTypeName returns the bare type name of a method receiver: T, *T, or a generic T[…].
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

// inputsMethods are the generic Context methods that acquire a command's own declared inputs.
// Each takes the inputs type as its type argument, which is what the audit reads.
var inputsMethods = map[string]bool{
	"Inputs": true, "InputsWithReport": true,
	"DefaultInputs": true, "ArgvInputs": true, "EnvInputs": true, "FileInputs": true, "StdinInputs": true,
}

// inputsTypeExpectations maps each generated handler type to its command's inputs type, and
// returns the set of every generated inputs type.
//
// An inputs struct binds to the frame whose hook is running, so a handler should only acquire
// its own command's type. Acquiring another (an ancestor's, say) reads silently wrong values
// when the commands share a flag name. The runtime cannot detect this; codegen can, because it
// knows which command each stub implements.
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

// wrongInputsTypes reports each call in a handler method that acquires another generated
// command's inputs type. Type arguments that are not generated inputs types are ignored, since a
// hand-written handler package may declare its own struct.
func wrongInputsTypes(fset *token.FileSet, files map[string]*ast.File, expected map[string]string, known map[string]bool, moduleRoot string) []error {
	var found []auditFinding

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
				if !ok || !inputsMethods[fn] || arg == want || !known[arg] {
					return true
				}
				at := findingAt(fset, call.Pos(), moduleRoot)
				at.msg = fmt.Sprintf(
					"%s:%d: %s in %s acquires %s, but this handler implements the command whose inputs are %s. "+
						"An inputs type binds to the command whose hook is running, so another command's type "+
						"reads THIS command's frame through the wrong shape, silently when the two share a flag name",
					at.file, at.line, fn, recv, arg, want)
				found = append(found, at)
				return true
			})
		}
	}

	return sortedWarnings(found)
}

// auditFinding is one thing the audit reports, placed by its module-relative file and line.
type auditFinding struct {
	file string
	line int
	msg  string
}

// findingAt places a finding at pos, with the file relative to the module root.
func findingAt(fset *token.FileSet, pos token.Pos, moduleRoot string) auditFinding {
	p := fset.Position(pos)
	rel := p.Filename
	if r, err := filepath.Rel(moduleRoot, p.Filename); err == nil {
		rel = filepath.ToSlash(r)
	}
	return auditFinding{file: rel, line: p.Line}
}

// sortedWarnings returns findings as warnings, sorted by file then line.
func sortedWarnings(found []auditFinding) []error {
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

// genericCallTypeArg reads `x.Fn[Type](…)` or `Fn[Type](…)`, returning the function name and
// the single type argument's name.
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
