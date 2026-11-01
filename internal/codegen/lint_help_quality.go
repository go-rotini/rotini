package codegen

import (
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"
)

// Opt-in help-quality warnings, turned on in the conf's `validate` section. They point at text
// the author wrote; nothing is rewritten or wrapped.

// lintInputSummaries, turned on by the conf's `validate.input_summaries`, warns about each
// visible flag and argument with no summary: help lists it with nothing beside it.
func lintInputSummaries(spec *Spec, conf *Conf) []error {
	if spec == nil || conf == nil || conf.Validate == nil || !conf.Validate.InputSummaries {
		return nil
	}
	var problems []error
	add := func(ptr, path, channel, name string) {
		p := inputProblem(ptr, path, channel, name, "has no `summary`, so help lists it with nothing beside it; add a one-line summary (validate.input_summaries)")
		p.sev = severityWarning
		problems = append(problems, p)
	}
	walkCommandsAt(spec, func(c *Command, path, ptr string) {
		for i, f := range c.Flags {
			if !f.Hidden && strings.TrimSpace(f.Summary) == "" {
				add(fmt.Sprintf("%s/flags/%d", ptr, i), path, "flag", f.Name)
			}
		}
		for i, a := range c.Arguments {
			if !a.Hidden && strings.TrimSpace(a.Summary) == "" {
				add(fmt.Sprintf("%s/arguments/%d", ptr, i), path, "argument", a.Name)
			}
		}
	})
	return problems
}

// lintHelpWidth, turned on by the conf's `validate.help_width`, warns about each field of help
// text with a line longer than that many characters: a command's summary, description,
// usage, header, footer, examples and verbatim help page, each input's summary, and each help
// topic's summary and body.
func lintHelpWidth(spec *Spec, conf *Conf) []error {
	if spec == nil || conf == nil || conf.Validate == nil || conf.Validate.HelpWidth <= 0 {
		return nil
	}
	width := conf.Validate.HelpWidth
	var problems []error
	check := func(ptr, loc, subject, text string) {
		lines := strings.Split(text, "\n")
		for i, line := range lines {
			n := utf8.RuneCountInString(strings.TrimRight(line, " \t\r"))
			if n <= width {
				continue
			}
			where := subject
			if len(lines) > 1 {
				where += fmt.Sprintf(", line %d,", i+1)
			}
			problems = append(problems, &problem{kind: "spec", ptr: ptr, loc: loc, sev: severityWarning,
				msg: fmt.Sprintf("%s is %d characters long, more than validate.help_width (%d); break or shorten it", where, n, width)})
		}
	}
	walkCommandsAt(spec, func(c *Command, path, ptr string) {
		loc := "command " + path
		for _, field := range []struct{ key, text string }{
			{"summary", c.Summary}, {"description", c.Description}, {"usage", c.Usage},
			{"header", c.Header}, {"footer", c.Footer}, {"help", c.Help},
		} {
			check(ptr+"/"+field.key, loc, "the "+field.key, field.text)
		}
		for i, ex := range examplesOf(c.Examples) {
			at := examplePointer(c, ptr, i)
			check(at, loc, "example "+strconv.Quote(ex.Run), ex.Run)
			if ex.Summary != "" {
				check(strings.TrimSuffix(at, "/run")+"/summary", loc, "the summary of example "+strconv.Quote(ex.Run), ex.Summary)
			}
		}
		eachInputSummaryAt(c, ptr, func(channel, name, at, summary string) {
			check(at+"/summary", loc, fmt.Sprintf("the summary of %s %q", channel, name), summary)
		})
		for i, t := range c.Topics {
			at := fmt.Sprintf("%s/topics/%d", ptr, i)
			check(at+"/summary", loc, fmt.Sprintf("the summary of topic %q", t.Name), t.Summary)
			check(at+"/body", loc, fmt.Sprintf("the body of topic %q", t.Name), t.Body)
		}
	})
	return problems
}

// eachInputSummaryAt visits the summary of each of c's flags, arguments, env and config inputs.
func eachInputSummaryAt(c *Command, ptr string, visit func(channel, name, at, summary string)) {
	for i, f := range c.Flags {
		visit("flag", f.Name, fmt.Sprintf("%s/flags/%d", ptr, i), f.Summary)
	}
	for i, a := range c.Arguments {
		visit("argument", a.Name, fmt.Sprintf("%s/arguments/%d", ptr, i), a.Summary)
	}
	for i, e := range c.Env {
		visit("env", e.Name, fmt.Sprintf("%s/env/%d", ptr, i), e.Summary)
	}
	for i, cfg := range c.Config {
		visit("config", cfg.Name, fmt.Sprintf("%s/config/%d", ptr, i), cfg.Summary)
	}
}
