package codegen

import (
	"fmt"
	"slices"
)

// lintShortCircuitScope warns about a short_circuit flag (a --help or --version) that isn't
// cascading on a command whose sub-commands don't declare their own: the flag applies only to
// its own command, so `<command> <sub-command> --help` is a usage error.
func lintShortCircuitScope(spec *Spec) []error {
	var problems []error
	walkCommandsAt(spec, func(c *Command, path, ptr string) {
		for i, f := range c.Flags {
			if !f.ShortCircuit || f.Cascading {
				continue
			}
			missing := subCommandsWithout(c, f.Name)
			if len(missing) == 0 {
				continue
			}
			p := inputProblem(fmt.Sprintf("%s/flags/%d", ptr, i), path, "flag", f.Name, fmt.Sprintf(
				"is short_circuit but not cascading, so it applies only to %q: `%s %s %s` is a usage error; set `cascading: true` so every sub-command answers it",
				c.Name, c.Name, missing[0], longForm(f)))
			p.sev = severityWarning
			problems = append(problems, p)
		}
	})
	return problems
}

// subCommandsWithout lists c's sub-commands, by name, that declare no flag called name. A
// composed child declares its own flags, so it isn't judged here.
func subCommandsWithout(c *Command, name string) []string {
	var out []string
	for _, sub := range c.Commands {
		if sub.Ref != "" {
			continue
		}
		if !slices.ContainsFunc(sub.Flags, func(f FlagInput) bool { return f.Name == name }) {
			out = append(out, sub.Name)
		}
	}
	return out
}
