package codegen

import (
	"fmt"
	"slices"
	"strings"
)

// streamPathKind is the inputfile or outputfile kind an input declares (or its list
// element's), else "".
func streamPathKind(schema *InputSchema) string {
	if schema == nil {
		return ""
	}
	switch k := strings.TrimPrefix(definitionType(schema, nil), "[]"); k {
	case "inputfile", "outputfile":
		return k
	}
	return ""
}

// lintStreamPaths checks where the inputfile and outputfile kinds may appear. Both are argv
// grammar ("-" names a standard stream), so env and config inputs can't take them, and a
// passthrough argument keeps its words as typed. An inputfile reads stdin, which has one
// consumer per chain: it can't share a chain with a `from: [stdin]` flag, nor with a declared
// `stdin:` unless that stdin is read only when the input is empty (`unless_argument`).
func lintStreamPaths(spec *Spec) []error {
	var problems []error
	reported := map[string]bool{}
	add := func(ptr, path, channel, name, msg string) {
		if reported[ptr+msg] {
			return
		}
		reported[ptr+msg] = true
		problems = append(problems, inputProblem(ptr, path, channel, name, msg))
	}
	walkCommandsAt(spec, func(c *Command, path, ptr string) {
		eachInputAt(c, ptr, func(channel, name, ptr string, schema *InputSchema) {
			kind := streamPathKind(schema)
			if kind == "" {
				return
			}
			switch {
			case channel == "env" || channel == "config":
				instead := "existingfile"
				if kind == "outputfile" {
					instead = "string"
				}
				add(ptr, path, channel, name, fmt.Sprintf("is type %s, whose \"-\" names a standard stream; that is command-line grammar, so use it on a flag or argument (or %s here)", kind, instead))
			case len(schema.From) > 0:
				add(ptr, path, channel, name, fmt.Sprintf("is type %s and sets `from`; its \"-\" already names a standard stream, so remove `from`", kind))
			}
		})
		for i, a := range c.Arguments {
			if a.Passthrough && streamPathKind(a.Schema) != "" {
				add(fmt.Sprintf("%s/arguments/%d", ptr, i), path, "argument", a.Name, "is a passthrough argument, whose words are kept as typed; it can't be inputfile or outputfile")
			}
		}
	})

	walkChainsAt(spec, func(chain []*Command, path, ptr string) {
		leaf := chain[len(chain)-1]
		files, fromStdin := stdinConsumers(chain, path, ptr)
		for _, f := range files {
			switch {
			case fromStdin != "":
				add(f.ptr, f.path, f.channel, f.name, fmt.Sprintf("is type inputfile, whose \"-\" reads stdin, but %s already consumes stdin; stdin has one consumer", fromStdin))
			case leaf.Stdin != nil && (f.channel != "argument" || leaf.Stdin.UnlessArgument != f.name):
				add(f.ptr, f.path, f.channel, f.name, fmt.Sprintf("is type inputfile, whose \"-\" reads stdin, but command %s declares `stdin`; stdin has one consumer, unless `stdin.unless_argument` names this argument", path))
			}
		}
	})
	return problems
}

// chainPointer is the JSON pointer of the command up levels above the one at ptr.
func chainPointer(ptr string, up int) string {
	for range up {
		ptr = ptr[:strings.LastIndex(ptr, "/commands/")]
	}
	return ptr
}

// chainPath is the command path up levels above path.
func chainPath(path string, up int) string {
	for range up {
		if i := strings.LastIndex(path, "/"); i >= 0 {
			path = path[:i]
		}
	}
	return path
}

// lintRoles checks a flag's `role`: 'force' marks a bool flag, and a command gives each role to
// at most one flag.
func lintRoles(spec *Spec) []error {
	var problems []error
	walkCommandsAt(spec, func(c *Command, path, ptr string) {
		seen := map[string]string{}
		for i, f := range c.Flags {
			if f.Role == "" {
				continue
			}
			fptr := fmt.Sprintf("%s/flags/%d/role", ptr, i)
			if f.Role == "force" && getSchemaType(f.Schema) != "bool" {
				problems = append(problems, inputProblem(fptr, path, "flag", f.Name, "has role force but is not a bool; the force role marks the switch that allows replacing existing files"))
			}
			if prev, ok := seen[f.Role]; ok {
				problems = append(problems, inputProblem(fptr, path, "flag", f.Name, fmt.Sprintf("has role %s, which flag %q already has; a command gives each role to one flag", f.Role, prev)))
				continue
			}
			seen[f.Role] = f.Name
		}
	})
	return problems
}

// globKinds are the argument element types `glob: true` may expand into.
var globKinds = []string{"string", "existingfile", "existingdir", "inputfile"}

// lintGlob allows `glob: true` only on a variadic argument of a path-like type: flags, env and
// config values, passthrough arguments and `from:` inputs keep their words as given.
func lintGlob(spec *Spec) []error {
	var problems []error
	walkCommandsAt(spec, func(c *Command, path, ptr string) {
		eachInputAt(c, ptr, func(channel, name, ptr string, schema *InputSchema) {
			if schema == nil || !schema.Glob {
				return
			}
			add := func(msg string) {
				problems = append(problems, inputProblem(ptr+"/schema/glob", path, channel, name, msg))
			}
			if channel != "argument" {
				add("sets `glob`, which applies to variadic arguments only")
				return
			}
			t := definitionType(schema, nil)
			elem, list := strings.CutPrefix(t, "[]")
			switch {
			case !list:
				add("sets `glob` but is not variadic; a pattern expands to many paths, so declare a list")
			case !slices.Contains(globKinds, elem):
				add(fmt.Sprintf("sets `glob` but its elements are %s; glob expands to paths, so use string, existingfile, existingdir or inputfile", elem))
			case len(schema.From) > 0:
				add("sets `glob` and `from`; a value read from a file or stdin is not expanded")
			case schema.Separator != "":
				add("sets `glob` and `separator`; a pattern is one word, so remove `separator`")
			}
		})
		for i, a := range c.Arguments {
			if a.Passthrough && a.Schema != nil && a.Schema.Glob {
				problems = append(problems, inputProblem(fmt.Sprintf("%s/arguments/%d/schema/glob", ptr, i), path, "argument", a.Name, "sets `glob` on a passthrough argument, whose words are kept as typed"))
			}
		}
	})
	return problems
}

// stdinUse is an input on a command chain that may read stdin.
type stdinUse struct{ ptr, path, channel, name string }

// stdinConsumers lists a chain's inputfile inputs (every command's flags, the leaf's
// arguments), and names its first `from: [stdin]` flag ("" for none).
func stdinConsumers(chain []*Command, path, ptr string) (files []stdinUse, fromStdin string) {
	for depth, c := range chain {
		cptr := chainPointer(ptr, len(chain)-1-depth)
		cpath := chainPath(path, len(chain)-1-depth)
		for i, f := range c.Flags {
			if streamPathKind(f.Schema) == "inputfile" {
				files = append(files, stdinUse{fmt.Sprintf("%s/flags/%d", cptr, i), cpath, "flag", f.Name})
			}
			if f.Schema != nil && slices.Contains(f.Schema.From, "stdin") && fromStdin == "" {
				fromStdin = fmt.Sprintf("flag %q (from: stdin)", f.Name)
			}
		}
	}
	for i, a := range chain[len(chain)-1].Arguments {
		if streamPathKind(a.Schema) == "inputfile" {
			files = append(files, stdinUse{fmt.Sprintf("%s/arguments/%d", ptr, i), path, "argument", a.Name})
		}
	}
	return files, fromStdin
}
