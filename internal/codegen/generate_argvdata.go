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
	})
	if l == "" {
		return ""
	}
	return "ExitStatus: " + l + ",\n"
}

// composedInputsType names the inputs type of a command in a composed child's package, or ""
// when the subtree delegates to a hand-written package, or comes from a further `$ref`, whose
// types the parent doesn't import.
func composedInputsType(ctx composeCtx, method string) string {
	if ctx.passthrough || ctx.nested {
		return ""
	}
	return ctx.alias + "." + method + "Inputs"
}
