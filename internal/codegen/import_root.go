package codegen

import (
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
)

// cobraImportPath is the package whose *Command a Cobra program's tree is built from.
const cobraImportPath = "github.com/spf13/cobra"

// rootConstructors are the parameterless functions findImportRoot recognizes as returning
// the root command, in the order it prefers them.
var rootConstructors = []string{
	"NewRootCmd", "NewRootCommand", "NewCommand", "New", "Root", "RootCmd",
	"newRootCmd", "newRootCommand", "newCommand", "root", "rootCommand",
}

// findImportRoot picks the Go expression that yields a command of the package in pkgDir,
// from its non-test files, without type-checking: a package-level *cobra.Command variable
// (any one works, since the import walks up to its root; one named like a root is
// preferred), else a parameterless function returning one, named like a root constructor.
// how says which rule matched. With neither, the error lists what it found.
func findImportRoot(pkgDir string) (expr, how string, err error) {
	files, err := parsePackage(pkgDir)
	if err != nil {
		return "", "", err
	}
	var vars, funcs, others []string
	for _, f := range files {
		alias := importAlias(f, cobraImportPath, "cobra")
		if alias == "" {
			continue
		}
		for _, decl := range f.Decls {
			switch d := decl.(type) {
			case *ast.GenDecl:
				vars = append(vars, commandVars(d, alias)...)
			case *ast.FuncDecl:
				if d.Recv != nil || !returnsCommand(d.Type, alias) {
					continue
				}
				if d.Type.Params.NumFields() == 0 {
					funcs = append(funcs, d.Name.Name)
				} else {
					others = append(others, d.Name.Name)
				}
			}
		}
	}
	if len(vars) > 0 {
		for _, v := range vars {
			if strings.Contains(strings.ToLower(v), "root") {
				return v, "a package-level command variable", nil
			}
		}
		return vars[0], "a package-level command variable", nil
	}
	for _, name := range rootConstructors {
		if slices.Contains(funcs, name) {
			return name + "()", "a root constructor", nil
		}
	}
	return "", "", rootNotFound(funcs, others)
}

// rootNotFound explains how to name the root when findImportRoot can't pick one.
func rootNotFound(funcs, others []string) error {
	var b strings.Builder
	b.WriteString("no root command found: the package declares no package-level *cobra.Command variable and no root constructor (")
	b.WriteString(strings.Join(rootConstructors[:6], ", "))
	b.WriteString(")")
	if found := slices.Concat(funcs, others); len(found) > 0 {
		b.WriteString("; functions returning *cobra.Command: ")
		b.WriteString(strings.Join(found, ", "))
	}
	b.WriteString(`. Pass --root with a Go expression that yields a command, such as --root 'cmds.New(false)', ` +
		`or move the tree out of main into a function such as newRootCmd(). ` +
		`A func Execute() alone can't be used: point at the package that declares rootCmd (usually ./cmd)`)
	return errors.New(b.String())
}

// parsePackage parses the non-test files of the package in pkgDir, in name order, skipping
// any that don't parse.
func parsePackage(pkgDir string) ([]*ast.File, error) {
	paths, err := filepath.Glob(filepath.Join(pkgDir, "*.go"))
	if err != nil {
		return nil, fmt.Errorf("read the package: %w", err)
	}
	slices.Sort(paths)
	var files []*ast.File
	fset := token.NewFileSet()
	for _, path := range paths {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		src, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("read the package: %w", err)
		}
		if f, err := parser.ParseFile(fset, path, src, parser.SkipObjectResolution); err == nil {
			files = append(files, f)
		}
	}
	return files, nil
}

// packageSpecs returns the package-level specs of files declared with tok: *ast.TypeSpec for
// token.TYPE, *ast.ValueSpec for token.VAR.
func packageSpecs(files []*ast.File, tok token.Token) []ast.Spec {
	var specs []ast.Spec
	for _, f := range files {
		for _, decl := range f.Decls {
			if d, ok := decl.(*ast.GenDecl); ok && d.Tok == tok {
				specs = append(specs, d.Specs...)
			}
		}
	}
	return specs
}

// importAlias returns the name a file imports the package at path under (def when it names
// none), or "" when it doesn't import it.
func importAlias(f *ast.File, path, def string) string {
	for _, imp := range f.Imports {
		p, err := strconv.Unquote(imp.Path.Value)
		if err != nil || p != path {
			continue
		}
		if imp.Name != nil {
			if imp.Name.Name == "_" || imp.Name.Name == "." {
				return ""
			}
			return imp.Name.Name
		}
		return def
	}
	return ""
}

// commandVars lists the variables d declares as a *cobra.Command: by type, or by an
// &cobra.Command{…} value.
func commandVars(d *ast.GenDecl, alias string) []string {
	if d.Tok != token.VAR {
		return nil
	}
	var names []string
	for _, spec := range d.Specs {
		vs, ok := spec.(*ast.ValueSpec)
		if !ok {
			continue
		}
		for i, name := range vs.Names {
			if name.Name == "_" {
				continue
			}
			byType := vs.Type != nil && isCommandPointer(vs.Type, alias)
			byValue := i < len(vs.Values) && isCommandLiteral(vs.Values[i], alias)
			if byType || byValue {
				names = append(names, name.Name)
			}
		}
	}
	return names
}

// returnsCommand reports whether a function's only result is a *cobra.Command.
func returnsCommand(ft *ast.FuncType, alias string) bool {
	return ft.Results != nil && len(ft.Results.List) == 1 && len(ft.Results.List[0].Names) <= 1 &&
		isCommandPointer(ft.Results.List[0].Type, alias)
}

// isCommandPointer reports whether e is the type *alias.Command.
func isCommandPointer(e ast.Expr, alias string) bool {
	star, ok := e.(*ast.StarExpr)
	return ok && isCommandType(star.X, alias)
}

// isCommandLiteral reports whether e is &alias.Command{…}.
func isCommandLiteral(e ast.Expr, alias string) bool {
	u, ok := e.(*ast.UnaryExpr)
	if !ok || u.Op != token.AND {
		return false
	}
	lit, ok := u.X.(*ast.CompositeLit)
	return ok && isCommandType(lit.Type, alias)
}

// isCommandType reports whether e names alias.Command.
func isCommandType(e ast.Expr, alias string) bool {
	sel, ok := e.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != "Command" {
		return false
	}
	id, ok := sel.X.(*ast.Ident)
	return ok && id.Name == alias
}
