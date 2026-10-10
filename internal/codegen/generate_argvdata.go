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
	return fmt.Sprintf("Inputs: reflect.TypeFor[%s](),\n", typeExpr)
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
// (or the composed child above it) delegates to a hand-written package, Go forbids the import,
// or the alias is taken by another package.
func (gp *program) nestedTypeAlias(rr resolvedRef, c Command, ctx composeCtx, moduleName string) string {
	if ctx.typeAlias == "" || c.Handler != nil {
		return ""
	}
	imp := childCmdImport(rr.dir, rr.module)
	if checkImportableAcrossModules(imp, rr.module, moduleName, c.Ref) != nil {
		return ""
	}
	alias := identAlias(rr.spec.Command.Name)
	for _, ci := range gp.childImports {
		if ci.Alias == alias && ci.Path != imp {
			return ""
		}
	}
	gp.addImport(alias, imp)
	return alias
}
