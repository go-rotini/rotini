package codegen

import (
	"fmt"
	"regexp"
	"strings"
)

// lintAcross runs the rules that need both documents: spec problems that arise from how the
// conf configures generation. Problems are positioned in the spec.
func (p *Processor) lintAcross(rs *reconciledSpec, rc *reconciledConf) []error {
	if rs == nil || rc == nil {
		return nil
	}
	var problems []error
	for _, rule := range crossLints {
		problems = append(problems, rule(rs.spec, rc.conf)...)
	}
	locateProblems(problems, rs.path, rs.locate)
	return problems
}

// crossLints is the ordered set of rules that read the spec and the conf. The order is
// observable (problems are reported in rule order), so keep it stable.
var crossLints = []func(*Spec, *Conf) []error{
	lintManPageNames,
	lintUnshownMessages,
	lintFlagsFirst,
	lintPosixNames,
	lintToolStdinClash,
}

// lintManPageNames rejects two commands whose man pages would share a name when the man
// feature is enabled. Page names join the command path with "-" and lowercase it, so `notes
// tag-remove` and `notes tag remove` collide. Only this spec's commands are checked; generate
// checks composed ones.
func lintManPageNames(spec *Spec, conf *Conf) []error {
	if spec == nil || conf == nil || conf.Generate == nil {
		return nil
	}
	if f := conf.Generate.featureOf("man"); f == nil || !f.Enabled {
		return nil
	}
	first := map[string]string{}
	var problems []error
	walkChainsAt(spec, func(chain []*Command, _, ptr string) {
		names := make([]string, 0, len(chain))
		for _, c := range chain {
			if c.Name == "" {
				return // a $ref taking its name from the composed spec: generate checks it
			}
			names = append(names, c.Name)
		}
		page := manPageName(names[0], names[1:])
		command := strings.Join(names, " ")
		if prev, ok := first[page]; ok {
			problems = append(problems, &problem{
				kind: "spec", ptr: ptr, loc: "command " + command,
				msg: fmt.Sprintf("has the man page name %q, the same as command %q; both pages would be written to %s.<section>, so rename one of them", page, prev, page),
			})
			return
		}
		first[page] = command
	})
	return problems
}

// lintUnshownMessages warns about each `complete.message` the conf never shows: one the
// completion feature is off for, or doesn't turn messages on for.
func lintUnshownMessages(spec *Spec, conf *Conf) []error {
	if spec == nil || conf == nil {
		return nil
	}
	if mode, _ := completionMessages(conf); mode != "" {
		return nil
	}
	var problems []error
	walkCommandsAt(spec, func(c *Command, path, ptr string) {
		eachInputAt(c, ptr, func(channel, name, ptr string, schema *InputSchema) {
			if schema == nil || schema.Complete == nil || schema.Complete.Message == "" {
				return
			}
			p := inputProblem(ptr, path, channel, name, "sets `complete.message`, but the conf's completion feature is off or doesn't set `messages`, so it is never shown; enable completion with `messages: declared`, or remove the message")
			p.sev = severityWarning
			problems = append(problems, p)
		})
	})
	return problems
}

// lintFlagsFirst, turned on by the conf's `validate.flags_first`, warns about each env or
// config input that no flag in its command chain, and no argument of its command, can also
// set through its `variable:` or `key:`, so `--help` doesn't show it. Secret inputs are exempt
// (a secret doesn't belong on the command line), as are nested env inputs, which read a family
// of variables no single flag can mirror.
func lintFlagsFirst(spec *Spec, conf *Conf) []error {
	if spec == nil || conf == nil || conf.Validate == nil || !conf.Validate.FlagsFirst {
		return nil
	}
	prefix := spec.Command.EnvPrefix
	var problems []error
	walkChainsAt(spec, func(chain []*Command, path, ptr string) {
		vars, keys := map[string]bool{}, map[string]bool{}
		mirror := func(name string, schema *InputSchema) {
			key := flagReconKey(name, schema)
			if key == "" {
				return
			}
			keys[key] = true
			for v := range strings.SplitSeq(flagEnvVar(schema, key, prefix), ",") {
				vars[v] = true
			}
		}
		for _, c := range chain {
			for _, f := range c.Flags {
				mirror(f.Name, f.Schema)
			}
		}
		for _, a := range chain[len(chain)-1].Arguments {
			mirror(a.Name, a.Schema)
		}
		warn := func(at, channel, name, msg string) {
			p := inputProblem(at, path, channel, name, msg+" (validate.flags_first)")
			p.sev = severityWarning
			problems = append(problems, p)
		}
		leaf := chain[len(chain)-1]
		for i, e := range leaf.Env {
			if e.Schema != nil && (e.Schema.Secret || e.Schema.Nesting != "") {
				continue
			}
			names := strings.Split(envVarName(e, prefix), ",")
			read := false
			for _, v := range names {
				read = read || vars[v]
			}
			if !read {
				warn(fmt.Sprintf("%s/env/%d", ptr, i), "env", e.Name,
					fmt.Sprintf("no flag or argument reads %s, so --help doesn't show it; add a flag with `variable: %s`", names[0], names[0]))
			}
		}
		for i, c := range leaf.Config {
			if c.Schema != nil && c.Schema.Secret {
				continue
			}
			if key := configKey(c); !keys[key] {
				warn(fmt.Sprintf("%s/config/%d", ptr, i), "config", c.Name,
					fmt.Sprintf("no flag or argument reads config key %q, so --help doesn't show it; add a flag with `key: %s`", key, key))
			}
		}
	})
	return problems
}

// posixUtilityName matches a POSIX utility name (XBD 12.2, guidelines 1 and 2): 2 to 9
// lowercase letters and digits.
var posixUtilityName = regexp.MustCompile(`^[a-z0-9]{2,9}$`)

// lintPosixNames, turned on by the conf's `validate.posix_names`, warns when the root
// command's name, which is the program's name, isn't a POSIX utility name. Sub-command names
// aren't utilities, so they aren't checked; display_name is ignored, since users type the
// binary's name.
func lintPosixNames(spec *Spec, conf *Conf) []error {
	if spec == nil || conf == nil || conf.Validate == nil || !conf.Validate.PosixNames {
		return nil
	}
	name := spec.Command.Name
	if name == "" || posixUtilityName.MatchString(name) {
		return nil
	}
	return []error{&problem{
		kind: "spec", ptr: rootPointer + "/name", loc: rootLabel(spec), sev: severityWarning,
		msg: "POSIX utility names are 2 to 9 lowercase letters and digits (utility syntax guidelines 1 and 2); rename the program, or leave validate.posix_names off",
	}}
}
