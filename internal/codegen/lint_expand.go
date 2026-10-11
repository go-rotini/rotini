package codegen

import (
	"slices"
	"strings"
)

// lintExpand keeps `expand` and `relative_to` to inputs whose values are paths: one string, or
// a list of strings, of a string or path type. `expand` is rejected beside `enum`, whose values
// are words rather than paths, and on secret inputs, whose expanded value would appear in a
// path and its errors. `relative_to: config` needs a value read from a configuration file. A
// warning marks `expand: [env]` on an input a walk-up discovered file can set: a project file
// in a cloned repository could then write the user's environment into a path.
func lintExpand(spec *Spec) []error {
	var problems []error
	walkChainsAt(spec, func(chain []*Command, path, ptr string) {
		c := chain[len(chain)-1]
		walkUpConfig, walkUpEnv := chainWalkUp(chain)
		eachInputAt(c, ptr, func(channel, name, ptr string, schema *InputSchema) {
			if schema == nil || (len(schema.Expand) == 0 && schema.RelativeTo == "") {
				return
			}
			add := func(sev severity, msg string) {
				p := inputProblem(ptr, path, channel, name, msg)
				p.sev = sev
				problems = append(problems, p)
			}
			key := "expand"
			if len(schema.Expand) == 0 {
				key = "relative_to"
			}
			typ := definitionType(schema, nil)
			switch {
			case channel == "stdin":
				add(severityError, "sets `"+key+"`, which applies to path values; stdin's payload is data, not a path")
				return
			case !stringValued(strings.TrimPrefix(typ, "[]")):
				add(severityError, "sets `"+key+"`, but its type is "+displayType(typ)+"; it applies to paths: string, existingfile, existingdir, inputfile or outputfile, or a list of them")
				return
			}
			if len(schema.Expand) > 0 {
				switch {
				case len(schema.Enum) > 0:
					add(severityError, "sets both `expand` and `enum`; enum values are words, not paths, and an expanded value could never match one")
				case schema.Secret:
					add(severityError, "sets `expand` on a secret input; the expanded value would appear in a path and in its errors, so expand it in the handler if needed")
				}
			}
			readsConfig := channel == "config" || ((channel == "flag" || channel == "argument") && flagReconKey(name, schema) != "")
			if schema.RelativeTo != "" && !readsConfig {
				add(severityError, "sets `relative_to: config`, but nothing is read from a configuration file to resolve; use a config input, or give the flag or argument a configuration fallback with `key:`")
			}
			readsEnv := channel == "env" || ((channel == "flag" || channel == "argument") && flagReconKey(name, schema) != "")
			if slices.Contains(schema.Expand, "env") && ((walkUpConfig && readsConfig) || (walkUpEnv && readsEnv)) {
				add(severityWarning, "sets `expand: [env]` on an input a walk-up discovered file can set; a project file in a cloned repository could write the user's environment (a token, say) into this path, so prefer `expand: [home]`")
			}
		})
	})
	return problems
}

// chainWalkUp reports whether the chain declares a configuration file found by walk-up
// discovery, and whether it declares such a file read as environment variables (`as: env`).
func chainWalkUp(chain []*Command) (config, env bool) {
	for _, c := range chain {
		for _, f := range c.ConfigFiles {
			if f.Discover == nil || f.Discover.Strategy != "walk-up" {
				continue
			}
			if f.As == "env" {
				env = true
			} else {
				config = true
			}
		}
	}
	return config, env
}
