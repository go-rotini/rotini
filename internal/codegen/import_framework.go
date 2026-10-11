package codegen

import (
	"errors"
	"fmt"
	"go/ast"
	"go/token"
	"maps"
	"reflect"
	"slices"
	"strconv"
	"strings"
)

// The frameworks `rotini import` reads, by ImportOptions.Framework.
const (
	FrameworkCobra  = "cobra"
	FrameworkKong   = "kong"
	FrameworkURFave = "urfave"
)

// importFramework is what an import needs to know about one framework.
type importFramework struct {
	// pkg is the importer's package in importerModule, and the suffix of the name the
	// generated test imports it under (rotiniimport<pkg>).
	pkg string
	// label names the framework in notes: "cobra upgraded from …".
	label string
	// module is the framework's module, whose version the import may raise.
	module string
	// findRoot picks the root expression from the package's code.
	findRoot func(pkgDir string) (expr, how string, err error)
	// call is the generated test's call to the importer: the root expression, the options,
	// and the default name (the last element of the package's import path).
	call func(root string, opts ImportOptions, name string) string
}

// importFrameworks are the frameworks by name.
var importFrameworks = map[string]importFramework{
	FrameworkCobra: {
		pkg: "cobra", label: "cobra", module: "github.com/spf13/cobra",
		findRoot: findImportRoot,
		call: func(root string, opts ImportOptions, _ string) string {
			return fmt.Sprintf("rotiniimportcobra.FromCobra((%s).Root(), rotiniimportcobra.Options{Strict: %t})", root, opts.Strict)
		},
	},
	FrameworkKong: {
		pkg: "kong", label: "kong", module: "github.com/alecthomas/kong",
		findRoot: findKongRoot,
		call: func(root string, opts ImportOptions, name string) string {
			var vars string
			if len(opts.Vars) > 0 {
				pairs := make([]string, 0, len(opts.Vars))
				for _, k := range slices.Sorted(maps.Keys(opts.Vars)) {
					pairs = append(pairs, strconv.Quote(k)+": "+strconv.Quote(opts.Vars[k]))
				}
				vars = ", Vars: map[string]string{" + strings.Join(pairs, ", ") + "}"
			}
			return fmt.Sprintf("rotiniimportkong.FromKong(%s, rotiniimportkong.Options{Name: %s%s})", root, strconv.Quote(rootName(opts, name)), vars)
		},
	},
	FrameworkURFave: {
		pkg: "urfave", label: "urfave/cli", module: "github.com/urfave/cli/v3",
		findRoot: findURFaveRoot,
		call: func(root string, opts ImportOptions, name string) string {
			return fmt.Sprintf("rotiniimporturfave.FromURFave(%s, rotiniimporturfave.Options{Name: %s, Strict: %t})", root, strconv.Quote(rootName(opts, name)), opts.Strict)
		},
	},
}

// frameworkFor returns the framework opts names; "" is Cobra.
func frameworkFor(opts ImportOptions) (importFramework, error) {
	name := opts.Framework
	if name == "" {
		name = FrameworkCobra
	}
	fw, ok := importFrameworks[name]
	if !ok {
		return importFramework{}, fmt.Errorf("unknown framework %q: want cobra, kong or urfave", name)
	}
	return fw, nil
}

// rootName is the name an importer gives the root when the program doesn't: --name, else
// the package's name as go build names its binary.
func rootName(opts ImportOptions, name string) string {
	if opts.Name != "" {
		return opts.Name
	}
	return name
}

// binaryName is the last element of an import path, skipping a major version suffix, which
// is the name go build gives a main package's binary.
func binaryName(importPath string) string {
	elems := strings.Split(importPath, "/")
	last := elems[len(elems)-1]
	if len(elems) > 1 && len(last) > 1 && last[0] == 'v' {
		if _, err := strconv.Atoi(last[1:]); err == nil {
			return elems[len(elems)-2]
		}
	}
	return last
}

// kongTagKeys are struct tag keys that mark a field as part of a Kong grammar.
var kongTagKeys = []string{"cmd", "arg", "help", "short", "name", "env", "default", "enum", "required", "hidden", "placeholder", "embed"}

// kongGrammarTypes are the type names findKongRoot recognizes as a grammar, in the order it
// prefers them.
var kongGrammarTypes = []string{"CLI", "Cli", "cli", "Grammar", "grammar", "App", "app", "Root", "root"}

// findKongRoot picks the Go expression that yields a pointer to the package's Kong grammar,
// from its non-test files, without type-checking: a package-level variable holding a grammar
// struct (var CLI struct{…}, var cli CLI; one named cli is preferred) gives &CLI, else a
// struct type named like a grammar (type CLI struct{…}) gives &CLI{}. A grammar struct is
// one with a field tagged for Kong (cmd:"", arg:"", help:"…"). how says which rule matched.
// With neither, the error lists the grammar types it found.
func findKongRoot(pkgDir string) (expr, how string, err error) {
	files, err := parsePackage(pkgDir)
	if err != nil {
		return "", "", err
	}
	structs := map[string]*ast.StructType{}
	var tagged []string
	for _, spec := range packageSpecs(files, token.TYPE) {
		ts, ok := spec.(*ast.TypeSpec)
		if !ok {
			continue
		}
		if st, ok := ts.Type.(*ast.StructType); ok {
			structs[ts.Name.Name] = st
			if kongTagged(st) {
				tagged = append(tagged, ts.Name.Name)
			}
		}
	}
	grammarType := func(e ast.Expr) bool {
		id, ok := e.(*ast.Ident)
		if !ok {
			return false
		}
		_, declared := structs[id.Name]
		return slices.Contains(tagged, id.Name) || declared && slices.Contains(kongGrammarTypes, id.Name)
	}

	var vars []string
	for _, spec := range packageSpecs(files, token.VAR) {
		vs, ok := spec.(*ast.ValueSpec)
		if !ok {
			continue
		}
		for i, name := range vs.Names {
			var value ast.Expr
			if i < len(vs.Values) {
				value = vs.Values[i]
			}
			if e := kongGrammarVar(name.Name, vs.Type, value, grammarType); e != "" && name.Name != "_" {
				vars = append(vars, e)
			}
		}
	}
	if len(vars) > 0 {
		for _, v := range vars {
			if strings.EqualFold(strings.TrimPrefix(v, "&"), "cli") {
				return v, "a package-level grammar variable", nil
			}
		}
		return vars[0], "a package-level grammar variable", nil
	}
	for _, name := range kongGrammarTypes {
		if _, ok := structs[name]; ok {
			return "&" + name + "{}", "a grammar type", nil
		}
	}
	var b strings.Builder
	b.WriteString("no Kong grammar found: the package declares no package-level grammar variable (var CLI struct{…}) and no grammar type named ")
	b.WriteString(strings.Join(kongGrammarTypes[:4], ", "))
	if len(tagged) > 0 {
		b.WriteString("; struct types with Kong tags: ")
		b.WriteString(strings.Join(tagged, ", "))
	}
	b.WriteString(`. Pass --root with a Go expression that yields a pointer to the grammar, such as --root '&cli' or --root '&Commands{}'`)
	return "", "", errors.New(b.String())
}

// kongGrammarVar returns the expression for a package-level variable that holds a grammar
// (&name for a struct value, name for a pointer), or "" when it doesn't hold one.
func kongGrammarVar(name string, typ, value ast.Expr, grammarType func(ast.Expr) bool) string {
	if typ != nil {
		switch t := typ.(type) {
		case *ast.StructType:
			if kongTagged(t) {
				return "&" + name
			}
		case *ast.StarExpr:
			if grammarType(t.X) {
				return name
			}
		default:
			if grammarType(t) {
				return "&" + name
			}
		}
		return ""
	}
	switch v := value.(type) {
	case *ast.CompositeLit:
		if grammarType(v.Type) {
			return "&" + name
		}
	case *ast.UnaryExpr:
		if lit, ok := v.X.(*ast.CompositeLit); ok && v.Op == token.AND && grammarType(lit.Type) {
			return name
		}
	}
	return ""
}

// kongTagged reports whether a field of st carries a Kong tag.
func kongTagged(st *ast.StructType) bool {
	for _, field := range st.Fields.List {
		if field.Tag == nil {
			continue
		}
		tag, err := strconv.Unquote(field.Tag.Value)
		if err != nil {
			continue
		}
		for _, key := range kongTagKeys {
			if _, ok := reflect.StructTag(tag).Lookup(key); ok {
				return true
			}
		}
	}
	return false
}

// urfaveImportPath is the package whose *Command a urfave/cli v3 program's tree is built from.
const urfaveImportPath = "github.com/urfave/cli/v3"

// urfaveConstructors are the parameterless functions findURFaveRoot recognizes as returning
// the root command, in the order it prefers them.
var urfaveConstructors = append(slices.Clone(rootConstructors), "NewApp", "newApp", "App", "app")

// findURFaveRoot picks the Go expression that yields the root *cli.Command of the package in
// pkgDir, from its non-test files, without type-checking. A command another command lists
// in its Commands is not a root. In order: a package-level *cli.Command variable (one named
// like a root is preferred), a parameterless function returning one named like a root
// constructor, or the only such function. how says which rule matched. With none, the
// error lists what it found.
func findURFaveRoot(pkgDir string) (expr, how string, err error) {
	files, err := parsePackage(pkgDir)
	if err != nil {
		return "", "", err
	}
	var vars, funcs, others []string
	listed := map[string]bool{} // variables and functions named in a Commands list
	for _, f := range files {
		alias := importAlias(f, urfaveImportPath, "cli")
		if alias == "" {
			continue
		}
		ast.Inspect(f, func(n ast.Node) bool {
			if kv, ok := n.(*ast.KeyValueExpr); ok {
				if key, ok := kv.Key.(*ast.Ident); ok && key.Name == "Commands" {
					markListed(kv.Value, listed)
				}
			}
			return true
		})
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
	vars = slices.DeleteFunc(vars, func(v string) bool { return listed[v] })
	if len(vars) > 0 {
		for _, v := range vars {
			if l := strings.ToLower(v); strings.Contains(l, "root") || strings.Contains(l, "app") {
				return v, "a package-level command variable", nil
			}
		}
		return vars[0], "a package-level command variable", nil
	}
	for _, name := range urfaveConstructors {
		if slices.Contains(funcs, name) {
			return name + "()", "a root constructor", nil
		}
	}
	if unlisted := slices.DeleteFunc(slices.Clone(funcs), func(f string) bool { return listed[f] }); len(unlisted) == 1 {
		return unlisted[0] + "()", "the only function returning a command no other command lists", nil
	}
	var b strings.Builder
	b.WriteString("no root command found: the package declares no package-level *cli.Command variable and no root constructor (")
	b.WriteString(strings.Join(urfaveConstructors[:6], ", "))
	b.WriteString(", newApp)")
	if found := slices.Concat(funcs, others); len(found) > 0 {
		b.WriteString("; functions returning *cli.Command: ")
		b.WriteString(strings.Join(found, ", "))
	}
	b.WriteString(`. Pass --root with a Go expression that yields the root command, such as --root 'newApp()', ` +
		`or move the tree out of main into a function such as newApp()`)
	return "", "", errors.New(b.String())
}

// markListed records the variables and parameterless calls in a Commands list.
func markListed(e ast.Expr, listed map[string]bool) {
	lit, ok := e.(*ast.CompositeLit)
	if !ok {
		return
	}
	for _, elt := range lit.Elts {
		switch x := elt.(type) {
		case *ast.Ident:
			listed[x.Name] = true
		case *ast.CallExpr:
			if id, ok := x.Fun.(*ast.Ident); ok && len(x.Args) == 0 {
				listed[id.Name] = true
			}
		}
	}
}
