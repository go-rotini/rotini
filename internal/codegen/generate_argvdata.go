package codegen

import (
	"fmt"
	"strings"
)

// This file emits the Definition data tests and tools read about each command: the generated
// inputs type rotini.ArgvOf finds a command by, and the exit codes the command documents.

// inputsTypeLiteral renders a Definition or CommandDef literal's Inputs field for the inputs
// type named typeExpr, or "" when the command has no inputs type this package can name.
func inputsTypeLiteral(typeExpr string) string {
	if typeExpr == "" {
		return ""
	}
	return "Inputs: " + typeOfLiteral(typeExpr) + ",\n"
}

// typeOfLiteral renders the reflect.Type of the type named typeExpr through the generated
// rotiniTypeOf rather than reflect.TypeFor: each reflect.TypeFor[T] call links a generic
// dictionary for T, a pointer the loader relocates at start-up, and this form links none.
func typeOfLiteral(typeExpr string) string {
	return "rotiniTypeOf((*" + typeExpr + ")(nil))"
}

// exitStatusLiteral renders a Definition or CommandDef literal's ExitStatus field, or "" when
// the command documents no exit codes.
func exitStatusLiteral(entries []ExitStatusEntry) string {
	l := sliceLiteral("ExitStatusDef", entries, func(b *strings.Builder, e ExitStatusEntry) {
		fmt.Fprintf(b, "Code: %d", e.Code)
		if e.Name != "" {
			fmt.Fprintf(b, ", Name: %q", e.Name)
		}
		if e.Summary != "" {
			fmt.Fprintf(b, ", Summary: %q", e.Summary)
		}
		if e.Retryable {
			b.WriteString(", Retryable: true")
		}
		if e.DocsUrl != "" {
			fmt.Fprintf(b, ", DocsURL: %q", e.DocsUrl)
		}
	})
	if l == "" {
		return ""
	}
	return "ExitStatus: " + l + ",\n"
}

// composedTypeName names a generated type (suffix "Inputs" or "Output") of the command at path
// in a composed subtree, qualified by the package of the spec that declares the command, or ""
// when the subtree's types live in no package the parent imports (a hand-written handler
// package).
func composedTypeName(ctx composeCtx, path, suffix string) string {
	if ctx.typeAlias == "" {
		return ""
	}
	rel := strings.TrimPrefix(strings.TrimPrefix(path, ctx.typeRoot), "_")
	return ctx.typeAlias + "." + ctx.typePascal + toPascalCase(rel) + suffix
}

// nestedTypeAlias imports the generated package of a spec `$ref`'d inside a composed child, for
// its commands' types, and returns its alias; "" when the parent can't name them: the `$ref`
// (or the composed child above it) delegates to a hand-written package, or Go forbids the import.
func (gp *program) nestedTypeAlias(rr resolvedRef, c Command, ctx composeCtx, moduleName string) string {
	if ctx.typeAlias == "" || c.Handler != nil {
		return ""
	}
	imp := childCmdImport(rr.dir, rr.module)
	if checkImportableAcrossModules(imp, rr.module, moduleName, c.Ref) != nil {
		return ""
	}
	return gp.addImport(identAlias(rr.spec.Command.Name), imp)
}

// exampleRunsDecl renders the cmd file's Examples variable: each example of the program's
// commands whose first command running the program has literal words, with those words, for a
// test to run. "" when there are none, or when a file of the author's in the package already
// declares Examples (generate then leaves it out, with a notice).
func (gp *program) exampleRunsDecl() string {
	program := gp.exampleProgram()
	var rows []string
	for _, n := range gp.contractNodes() {
		command := strings.Join(append([]string{gp.rootName}, n.path...), " ")
		for _, e := range n.help.Examples {
			argv, ok := exampleRunArgv(e.Run, program)
			if !ok {
				continue
			}
			row := fmt.Sprintf("{Command: %q, Run: %q", command, e.Run)
			if e.Summary != "" {
				row += fmt.Sprintf(", Summary: %q", e.Summary)
			}
			rows = append(rows, row+", Argv: "+goStringSlice(argv)+"}")
		}
	}
	if len(rows) == 0 {
		return ""
	}
	if file, ok := gp.authorNames["Examples"]; ok {
		gp.auditWarnings = append(gp.auditWarnings, fmt.Errorf("%s declares Examples, the name of the generated variable listing the spec's examples; generate leaves the variable out", file))
		return ""
	}
	var b strings.Builder
	b.WriteString("// Examples are the spec's examples that run the program with literal words (no expansion,\n")
	b.WriteString("// glob or <placeholder>), in the spec's order, each with the words after the program's name\n")
	b.WriteString("// that a test can run:\n//\n")
	b.WriteString("//\tfor _, ex := range Examples {\n//\t\tres := rotinitest.RunArgv(t, NewProgram(), ex.Argv)\n//\t\t// check res\n//\t}\n")
	b.WriteString("var Examples = []struct {\n")
	b.WriteString("\tCommand string   // the command whose examples list it, by its path\n")
	b.WriteString("\tRun     string   // the line as the spec writes it\n")
	b.WriteString("\tSummary string   // what it does; \"\" for none\n")
	b.WriteString("\tArgv    []string // the words after the program's name\n")
	b.WriteString("}{\n")
	for _, r := range rows {
		fmt.Fprintf(&b, "\t%s,\n", r)
	}
	b.WriteString("}\n")
	return b.String()
}
