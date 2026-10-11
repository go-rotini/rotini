package codegen

import (
	"fmt"
	"slices"
)

// lintUsageSchemaName rejects a named schema called Usage: it becomes a generated type beside
// the generated Usage function, and the package would not compile.
func lintUsageSchemaName(spec *Spec) []error {
	if _, ok := spec.Command.Schemas["Usage"]; !ok {
		return nil
	}
	return []error{&problem{kind: "spec", ptr: rootPointer + "/schemas/Usage", loc: rootLabel(spec),
		msg: "names a schema `Usage`, which collides with the generated Usage function; rename the schema"}}
}

// lintMulticall keeps `multicall` on the root, where it is read, and requires something to
// route to: a root with neither sub-commands nor plugins can only answer completion.
func lintMulticall(spec *Spec) []error {
	var problems []error
	walkCommandsAt(spec, func(c *Command, path, ptr string) {
		if c.Multicall == nil || c == &spec.Command {
			return
		}
		problems = append(problems, &problem{kind: "spec", ptr: ptr + "/multicall", loc: "command " + path,
			msg: "sets `multicall`, a root-command-level key valid only on the root command; remove it (it is read only at the root, so here it is silently ignored)"})
	})
	on, prefix, complete := multicallOf(&spec.Command)
	routes := prefix != "" || complete == ""
	if on && routes && len(spec.Command.Commands) == 0 && len(spec.Command.Plugins) == 0 {
		problems = append(problems, &problem{kind: "spec", ptr: rootPointer + "/multicall", loc: rootLabel(spec),
			msg: "routes by the invoked name, but the root has no sub-commands or plugins to route to; add them, or keep only `complete`"})
	}
	return problems
}

// chdirTypes are the types a `role: chdir` flag may have.
var chdirTypes = []string{"existingdir", "string"}

// lintChdirRole keeps `role: chdir` narrow, since it is the one role the runtime acts on: a
// cascading root flag holding a directory, read from the command line only, once per run.
func lintChdirRole(spec *Spec) []error {
	var problems []error
	walkCommandsAt(spec, func(c *Command, path, ptr string) {
		for i, f := range c.Flags {
			if f.Role != "chdir" {
				continue
			}
			fptr := fmt.Sprintf("%s/flags/%d", ptr, i)
			add := func(at, msg string) {
				problems = append(problems, inputProblem(fptr+at, path, "flag", f.Name, msg))
			}
			if c != &spec.Command {
				add("/role", "has role chdir but is not on the root command; the run's directory is set once, before any command is chosen, so declare it on the root with `cascading: true`")
				continue
			}
			if !f.Cascading {
				add("/role", "has role chdir but is not cascading; set `cascading: true`, so every command accepts it and documents it")
			}
			if f.ShortCircuit {
				add("/short_circuit", "has role chdir and is short_circuit; a directory flag doesn't replace the command's run")
			}
			if in := flagGroupsNaming(c, f.Name); in != "" {
				add("/role", fmt.Sprintf("has role chdir but is named by %s; the directory flag is read before any rule is checked, so leave it out of flag groups and dependencies", in))
			}
			s := f.Schema
			if s == nil {
				continue
			}
			if !slices.Contains(chdirTypes, s.Type) || isVariadicSchema(s) {
				add("/schema/type", "has role chdir, so its type must be existingdir or string, one directory")
			}
			if s.Repeatable != nil && *s.Repeatable {
				add("/schema/repeatable", "has role chdir and is repeatable; the last directory given wins, so leave `repeatable` unset")
			}
			for _, k := range []struct {
				key string
				set bool
			}{
				{"default", s.Default != nil},
				{"key", s.Key != ""},
				{"variable", s.Variable != nil},
				{"variable_file", s.VariableFile != ""},
				{"from", len(s.From) > 0},
			} {
				if k.set {
					add("/schema/"+k.key, fmt.Sprintf("has role chdir and sets %#q; the directory comes from the command line only", k.key))
				}
			}
		}
	})
	return problems
}

// flagGroupsNaming describes the first flag group or dependency of c that names flag, or "".
func flagGroupsNaming(c *Command, flag string) string {
	for i, g := range c.FlagGroups {
		if slices.Contains(g.Flags, flag) {
			return fmt.Sprintf("flag group %d", i)
		}
	}
	for i, d := range c.FlagDependencies {
		if d.When == flag || slices.Contains(d.Requires, flag) || slices.Contains(d.Unless, flag) || slices.Contains(d.Forbids, flag) {
			return fmt.Sprintf("flag dependency %d", i)
		}
	}
	return ""
}
