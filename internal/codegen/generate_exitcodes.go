package codegen

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"slices"
	"strings"
)

// This file generates a constant for each named exit code (`exit_status: [{code: 3, name:
// not_found}]` gives `const TaskrGetExitNotFound = 3`), and checks that the generated files
// declare no top-level name twice.

// exitConst is one named exit code's generated constant.
type exitConst struct {
	name  string // the Go name: TaskrGetExitNotFound
	code  int
	owner string // the command's invocation: "taskr get"
	entry string // the spec's name for the code: not_found
}

// exitConstants lists the constants of every own command's named exit codes, root first, in
// tree order. A composed command's codes are its own package's.
func (p *program) exitConstants() []exitConst {
	own := map[string]string{} // prefix → invocation
	for _, c := range p.ownCommands() {
		own[c.prefix] = c.invocation
	}
	var out []exitConst
	add := func(prefix string, entries []ExitStatusEntry) {
		invocation, ok := own[prefix]
		if !ok {
			return
		}
		for _, e := range entries {
			if e.Name != "" {
				out = append(out, exitConst{name: prefix + "Exit" + toPascalCase(e.Name), code: e.Code, owner: invocation, entry: e.Name})
			}
		}
	}
	add(p.rootPascal, p.rootHelp.ExitStatus)
	var walk func(nodes []rnode)
	walk = func(nodes []rnode) {
		for i := range nodes {
			add(nodes[i].prefix, nodes[i].help.ExitStatus)
			walk(nodes[i].children)
		}
	}
	walk(p.tree)
	return out
}

// exitConstantsDecl renders the constants as a const block, or, with reexport set, as
// re-exports of the models package's (`const X = models.X`), so handler code in the cmd
// package reads the same either way. It is "" when no code is named.
func exitConstantsDecl(consts []exitConst, reexport bool) string {
	if len(consts) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("// Named exit codes, from each command's exit_status, for rtx.HaltWithCode and rtx.Exit.\nconst (\n")
	for _, c := range consts {
		if reexport {
			fmt.Fprintf(&b, "\t%s = models.%s\n", c.name, c.name)
			continue
		}
		fmt.Fprintf(&b, "\t%s = %d // %q: %s\n", c.name, c.code, c.owner, c.entry)
	}
	b.WriteString(")\n")
	return b.String()
}

// contractDecl renders the Contract variable (generate.contract.go), or "" when it is off.
func contractDecl(doc []byte) string {
	if doc == nil {
		return ""
	}
	return "// Contract is the program's contract document (rotini-contract/1): its commands, inputs,\n" +
		"// outputs and exit statuses as JSON.\nvar Contract = " + goRawString(string(doc)) + "\n"
}

// generatedNames says what spec entry a generated top-level name comes from, for a collision
// message: each exit constant, the Usage function, Contract and ToolsMCP.
func generatedNames(consts []exitConst, contract, tools bool) map[string]string {
	out := map[string]string{"Usage": "the generated Usage function"}
	for _, c := range consts {
		out[c.name] = fmt.Sprintf("the exit_status name %q of %q", c.entry, c.owner)
	}
	if contract {
		out["Contract"] = "generate.contract.go's Contract variable"
	}
	if tools {
		out["ToolsMCP"] = "the tools feature's ToolsMCP variable (go: true)"
	}
	return out
}

// duplicateDecls reports each top-level name the generated Go sources declare more than once,
// together: Go names derive from command paths, schema names and exit-code names, so two
// spec entries can produce the same one. origins names the spec entry behind a name, where
// known.
func duplicateDecls(file string, origins map[string]string, sources ...[]byte) error {
	seen := map[string]bool{}
	var dups []string
	for _, src := range sources {
		if src == nil {
			continue
		}
		f, err := parser.ParseFile(token.NewFileSet(), file, src, parser.SkipObjectResolution)
		if err != nil {
			return fmt.Errorf("parse the generated %s: %w", file, err)
		}
		for _, name := range topLevelNames(f) {
			if seen[name] && !slices.Contains(dups, name) {
				dups = append(dups, name)
			}
			seen[name] = true
		}
	}
	if len(dups) == 0 {
		return nil
	}
	var msgs []string
	for _, name := range dups {
		msg := fmt.Sprintf("the generated name %s is declared twice", name)
		if origin, ok := origins[name]; ok {
			msg += fmt.Sprintf(": %s makes the same Go name as a command, input type or schema; rename it", origin)
		} else {
			msg += "; two spec entries (command paths, schema names) make the same Go name; rename one"
		}
		msgs = append(msgs, msg)
	}
	return fmt.Errorf("%s: %s", file, strings.Join(msgs, "; "))
}

// topLevelNames lists the names a file declares at package level (methods excluded).
func topLevelNames(f *ast.File) []string {
	var out []string
	for _, d := range f.Decls {
		switch decl := d.(type) {
		case *ast.FuncDecl:
			if decl.Recv == nil {
				out = append(out, decl.Name.Name)
			}
		case *ast.GenDecl:
			for _, s := range decl.Specs {
				switch spec := s.(type) {
				case *ast.TypeSpec:
					out = append(out, spec.Name.Name)
				case *ast.ValueSpec:
					for _, n := range spec.Names {
						if n.Name != "_" {
							out = append(out, n.Name)
						}
					}
				}
			}
		}
	}
	return out
}
