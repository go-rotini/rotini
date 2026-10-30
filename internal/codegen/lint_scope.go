package codegen

import (
	"fmt"
	"slices"
	"strings"
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
			p := inputProblem(fmt.Sprintf("%s/flags/%d/short_circuit", ptr, i), path, "flag", f.Name, fmt.Sprintf(
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

// lintRequiredFlagScope warns about a required flag on a command with sub-commands that isn't
// cascading: it applies only when that command itself runs, so a sub-command neither takes it
// nor requires it. Flag groups and dependencies aren't judged: they constrain flags only their
// own command takes, a coherent rule for a command that runs on its own.
func lintRequiredFlagScope(spec *Spec) []error {
	var problems []error
	walkCommandsAt(spec, func(c *Command, path, ptr string) {
		if len(c.Commands) == 0 {
			return
		}
		invocation := strings.ReplaceAll(path, "/", " ")
		example := ""
		if sub := firstSubCommand(c); sub != "" {
			example = fmt.Sprintf(" (`%s %s` neither takes it nor requires it)", invocation, sub)
		}
		for i, f := range c.Flags {
			if f.Cascading || f.ShortCircuit || f.Schema == nil || !f.Schema.Required {
				continue
			}
			p := inputProblem(fmt.Sprintf("%s/flags/%d/schema/required", ptr, i), path, "flag", f.Name, fmt.Sprintf(
				"is required but isn't cascading, so it applies only when %#q itself runs, not to its sub-commands%s; set `cascading: true` if the sub-commands need it too",
				invocation, example))
			p.sev = severityWarning
			problems = append(problems, p)
		}
	})
	return problems
}

// firstSubCommand names c's first sub-command declared by name, or "" when it has none. A
// composed child's name comes from its own spec, so it is skipped.
func firstSubCommand(c *Command) string {
	for _, sub := range c.Commands {
		if sub.Name != "" {
			return sub.Name
		}
	}
	return ""
}
