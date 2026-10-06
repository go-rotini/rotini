package codegen

import (
	"fmt"
	"strings"
)

// lintAcross runs the rules that need both documents: spec problems that arise from how the
// conf configures generation. Problems are positioned in the spec.
func (p *Processor) lintAcross(rs *reconciledSpec, rc *reconciledConf) []error {
	if rs == nil || rc == nil {
		return nil
	}
	problems := lintManPageNames(rs.spec, rc.conf)
	problems = append(problems, lintUnshownMessages(rs.spec, rc.conf)...)
	locateProblems(problems, rs.path, rs.locate)
	return problems
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
