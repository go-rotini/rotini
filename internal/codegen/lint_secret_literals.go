package codegen

import (
	"fmt"
	"slices"
)

// literalFreeSecret reports whether a secret input refuses a value typed on the command line:
// its `from` list leaves out `value`, so the value comes from a file or stdin.
func literalFreeSecret(s *InputSchema) bool {
	return s != nil && s.Secret && len(s.From) > 0 && !slices.Contains(s.From, "value")
}

// lintSecretLiterals warns about a secret flag that accepts its value on the command line,
// where it shows in the process list and shell history, and rejects `implicit_value` on one
// that refuses literals, since that value would be a literal.
func lintSecretLiterals(spec *Spec) []error {
	var problems []error
	walkCommandsAt(spec, func(c *Command, path, ptr string) {
		for i, f := range c.Flags {
			s := f.Schema
			if s == nil || !s.Secret || isBoolFlag(f) || isCountFlag(f) {
				continue
			}
			at := fmt.Sprintf("%s/flags/%d", ptr, i)
			if literalFreeSecret(s) {
				if s.ImplicitValue != nil {
					problems = append(problems, inputProblem(at, path, "flag", f.Name,
						"is secret and refuses a value typed on the command line (its `from` leaves out `value`), so it can't have an `implicit_value`, which is one; remove `implicit_value`"))
				}
				continue
			}
			p := inputProblem(at, path, "flag", f.Name,
				"is secret but accepts its value on the command line, where it shows in the process list and shell history; declare `from: [file]` or `from: [stdin]`, without `value`, to refuse it")
			p.sev = severityWarning
			problems = append(problems, p)
		}
	})
	return problems
}
