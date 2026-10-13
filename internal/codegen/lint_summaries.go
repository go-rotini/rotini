package codegen

import (
	"fmt"
	"strings"
)

// lintSummaryLines warns about a summary of more than one line, on a command, input, plugin,
// topic or exit status: help lists it in a one-line column, and completion and the generated
// examples show it on one line. A trailing line break (a one-line block scalar) is trimmed
// everywhere, so it doesn't count.
func lintSummaryLines(spec *Spec) []error {
	var problems []error
	multi := func(s string) bool { return strings.ContainsAny(strings.TrimRight(s, "\r\n"), "\r\n") }
	const why = "has a summary of more than one line; help, completion and the examples show it on one line, so keep the summary to one and put the rest in `description`"
	walkCommandsAt(spec, func(c *Command, path, ptr string) {
		if multi(c.Summary) {
			problems = append(problems, &problem{kind: "spec", ptr: ptr + "/summary", loc: "command " + path, msg: why, sev: severityWarning})
		}
		add := func(channel, name, at, summary string) {
			if multi(summary) {
				p := inputProblem(at+"/summary", path, channel, name, why)
				p.sev = severityWarning
				problems = append(problems, p)
			}
		}
		for i, f := range c.Flags {
			add("flag", f.Name, fmt.Sprintf("%s/flags/%d", ptr, i), f.Summary)
		}
		for i, a := range c.Arguments {
			add("argument", a.Name, fmt.Sprintf("%s/arguments/%d", ptr, i), a.Summary)
		}
		for i, e := range c.Env {
			add("env", e.Name, fmt.Sprintf("%s/env/%d", ptr, i), e.Summary)
		}
		for i, k := range c.Config {
			add("config", k.Name, fmt.Sprintf("%s/config/%d", ptr, i), k.Summary)
		}
		other := func(subject, at, summary string) {
			if multi(summary) {
				problems = append(problems, &problem{kind: "spec", ptr: at + "/summary", loc: "command " + path, msg: subject + ": " + why, sev: severityWarning})
			}
		}
		for i, pl := range c.Plugins {
			other(fmt.Sprintf("plugin %q", pl.Name), fmt.Sprintf("%s/plugins/%d", ptr, i), pl.Summary)
		}
		for i, t := range c.Topics {
			other(fmt.Sprintf("topic %q", t.Name), fmt.Sprintf("%s/topics/%d", ptr, i), t.Summary)
		}
		for i, e := range c.ExitStatus {
			other(fmt.Sprintf("exit status %d", e.Code), fmt.Sprintf("%s/exit_status/%d", ptr, i), e.Summary)
		}
	})
	return problems
}
