package codegen

import (
	"fmt"
	"maps"
	"slices"
	"strings"
)

// Lint rules for how argv is read: where flags stop (a passthrough argument, options_first),
// digit options, a variadic argument before fixed ones, response files, and described enums.

// lintPassthroughArgument checks a passthrough argument, where raw words start: it is the
// command's last argument and a variadic []string, the command has nothing else that reads
// the words after it (sub-commands, plugins, command-level passthrough), and it declares no
// rule that would judge, count or split the raw words (separator, enum, pattern, lengths, item
// counts, uniqueness).
func lintPassthroughArgument(spec *Spec) []error {
	var problems []error
	walkCommandsAt(spec, func(c *Command, path, ptr string) {
		for i, a := range c.Arguments {
			if !a.Passthrough {
				continue
			}
			aptr := fmt.Sprintf("%s/arguments/%d", ptr, i)
			add := func(key, msg string) {
				problems = append(problems, inputProblem(aptr+key, path, "argument", a.Name, msg))
			}
			if i != len(c.Arguments)-1 {
				add("/passthrough", "is a passthrough argument but not the last argument; raw words run to the end of the command line, so it must be last")
			}
			if getSchemaType(a.Schema) != "[]string" || (a.Schema != nil && a.Schema.Ref != "") {
				add("/passthrough", "is a passthrough argument, so its type must be []string, which receives the raw words")
			}
			switch {
			case len(c.Commands) > 0:
				add("/passthrough", "is a passthrough argument on a command with `commands`; a word naming a sub-command would be ambiguous, so declare one or the other")
			case len(c.Plugins) > 0 || c.PluginDiscovery != nil:
				add("/passthrough", "is a passthrough argument on a command with `plugins` or `plugin_discovery`; a word naming a plugin would be ambiguous, so declare one or the other")
			case c.Passthrough:
				add("/passthrough", "is a passthrough argument on a passthrough command, whose every word is already raw; remove one of the two")
			}
			s := a.Schema
			if s == nil {
				continue
			}
			for _, k := range []struct {
				key string
				set bool
			}{
				{"separator", s.Separator != ""},
				{"enum", len(s.Enum) > 0},
				{"pattern", s.Pattern != ""},
				{"minLength", s.MinLength != 0},
				{"maxLength", s.MaxLength != nil},
				{"minItems", s.MinItems != 0},
				{"maxItems", s.MaxItems != nil},
				{"uniqueItems", s.UniqueItems},
			} {
				if k.set {
					add("/schema/"+k.key, fmt.Sprintf("is a passthrough argument, whose words are passed on as typed, so it can't set %#q", k.key))
				}
			}
		}
	})
	return problems
}

// lintOptionsFirst checks options_first: it contradicts command-level passthrough (an error),
// and has no effect on a command with no arguments or whose first argument is a passthrough
// argument (warnings).
func lintOptionsFirst(spec *Spec) []error {
	var problems []error
	walkCommandsAt(spec, func(c *Command, path, ptr string) {
		if !c.OptionsFirst || c.Ref != "" {
			return // on a $ref node: lintRefNodeKeys
		}
		p := &problem{kind: "spec", ptr: ptr + "/options_first", loc: "command " + path}
		switch {
		case c.Passthrough:
			p.msg = "sets `options_first` and `passthrough`; a passthrough command parses no flags after its name, so options_first has nothing to stop"
		case len(c.Arguments) == 0:
			p.msg, p.sev = fmt.Sprintf("sets `options_first`, which has no effect: %q takes no arguments, so there is no first argument for flags to stop at", c.Name), severityWarning
		case c.Arguments[0].Passthrough:
			p.msg, p.sev = "sets `options_first`, which has no effect: its first argument is a passthrough argument, where flags already stop", severityWarning
		default:
			return
		}
		problems = append(problems, p)
	})
	return problems
}

// lintDigitIdentifiers checks digit options (-4): only a bool or count flag may declare one,
// and since a word such as -4 is then the flag on the declaring command and every command
// below it, none of those commands may have an argument that takes negative numbers.
func lintDigitIdentifiers(spec *Spec) []error {
	schemas := spec.Command.Schemas
	var problems []error
	walkChainsAt(spec, func(chain []*Command, path, ptr string) {
		c := chain[len(chain)-1]
		for i, f := range c.Flags {
			typ := getSchemaType(f.Schema)
			if f.Schema != nil && f.Schema.Type == "count" {
				typ = "count"
			}
			for _, id := range f.Identifiers {
				if isDigitIdentifier(id) && typ != "bool" && typ != "count" {
					problems = append(problems, inputProblem(fmt.Sprintf("%s/flags/%d/identifiers", ptr, i), path, "flag", f.Name,
						fmt.Sprintf("declares the digit option %s, which only a bool or count flag can have; a value after it would read like a number", id)))
				}
			}
		}
		type digit struct{ id, owner string }
		var digits []digit
		for _, a := range chain {
			for _, f := range a.Flags {
				for _, id := range f.Identifiers {
					if isDigitIdentifier(id) {
						digits = append(digits, digit{id, a.Name})
					}
				}
			}
		}
		if len(digits) == 0 {
			return
		}
		for i, a := range c.Arguments {
			if a.Passthrough || !takesNegativeNumbers(a.Schema, schemas) {
				continue
			}
			d := digits[0]
			problems = append(problems, inputProblem(fmt.Sprintf("%s/arguments/%d", ptr, i), path, "argument", a.Name,
				fmt.Sprintf("takes negative numbers, but %s is declared on %q, so %s would be read as the flag; declare minimum: 0, or drop the digit identifier", d.id, d.owner, d.id)))
		}
	})
	return problems
}

// isDigitIdentifier reports whether id is a digit option: one dash and one digit.
func isDigitIdentifier(id string) bool {
	return len(id) == 2 && id[0] == '-' && id[1] >= '0' && id[1] <= '9'
}

// takesNegativeNumbers reports whether an argument's values (or each element of a list) may
// be negative numbers: a signed numeric type or a duration without a lower bound of 0 or more.
func takesNegativeNumbers(schema *InputSchema, schemas map[string]Schema) bool {
	if schema == nil {
		return false
	}
	_, elem := inputValueType(schema, schemas)
	switch elem {
	case "int", "int8", "int16", "int32", "int64", "float32", "float64", "duration", "time.Duration":
	default:
		return false
	}
	for _, b := range []any{schema.Minimum, schema.ExclusiveMinimum} {
		if v := bound(b); v != nil && *v >= 0 {
			return false
		}
	}
	return true
}

// lintVariadicArguments checks the arguments around a variadic: at most one variadic, and the fixed
// arguments after it (`<src...> <dst>`) bind from the end, so each must be required, with no
// default, and the variadic can't split its values on a separator, since where it ends is
// known only once every word is in. A passthrough argument can't follow it. It warns when the
// variadic and the argument after it complete differently, since completion can't tell which
// one a word is for.
func lintVariadicArguments(spec *Spec) []error {
	var problems []error
	walkCommandsAt(spec, func(c *Command, path, ptr string) {
		v := -1
		for i, a := range c.Arguments {
			if strings.HasPrefix(getSchemaType(a.Schema), "[]") {
				v = i
				break
			}
		}
		if v < 0 || v == len(c.Arguments)-1 {
			return
		}
		at := func(i int) string { return fmt.Sprintf("%s/arguments/%d", ptr, i) }
		variadic := c.Arguments[v]
		if c.Passthrough {
			problems = append(problems, inputProblem(at(v), path, "argument", variadic.Name,
				"is variadic but not last, on a passthrough command, which needs its last argument to receive the raw words"))
			return
		}
		if variadic.Schema != nil && variadic.Schema.Separator != "" {
			problems = append(problems, inputProblem(at(v)+"/schema/separator", path, "argument", variadic.Name,
				"is variadic and followed by other arguments, so it can't set `separator`: where its values end is known only once every word is in"))
		}
		for i := v + 1; i < len(c.Arguments); i++ {
			a := c.Arguments[i]
			switch {
			case strings.HasPrefix(getSchemaType(a.Schema), "[]"):
				problems = append(problems, inputProblem(at(i), path, "argument", a.Name,
					fmt.Sprintf("is a second variadic argument after %q; only one argument can take a variable number of words", variadic.Name)))
			case a.Schema == nil || !a.Schema.Required:
				problems = append(problems, inputProblem(at(i), path, "argument", a.Name,
					fmt.Sprintf("follows variadic argument %q, so it must be required: arguments after a variadic bind from the end", variadic.Name)))
			case a.Schema.Default != nil:
				problems = append(problems, inputProblem(at(i)+"/schema/default", path, "argument", a.Name,
					fmt.Sprintf("follows variadic argument %q, so it can't have a default: arguments after a variadic bind from the end", variadic.Name)))
			}
		}
		next := c.Arguments[v+1]
		if completeKind(variadic.Schema) != completeKind(next.Schema) {
			p := inputProblem(at(v+1), path, "argument", next.Name,
				fmt.Sprintf("completes differently from variadic argument %q before it; completion can't tell which of the two a word is for, so it offers %q's", variadic.Name, variadic.Name))
			p.sev = severityWarning
			problems = append(problems, p)
		}
	})
	return problems
}

// completeKind is an input's declared completion kind, "" for none.
func completeKind(schema *InputSchema) string {
	if schema == nil || schema.Complete == nil {
		return ""
	}
	return schema.Complete.Kind
}

// lintResponseFiles checks response files against the inputs they could take words from: the
// prefix `@` beside a `from: [file]` flag or argument is an error, since `--token @x` would
// expand instead of reading the file; raw words (a passthrough command or argument) and plugin arguments that
// start with the prefix are expanded too unless written after `--`, a warning.
func lintResponseFiles(spec *Spec) []error {
	rf := spec.Command.ResponseFiles
	if rf == nil {
		return nil
	}
	var problems []error
	var raw []string
	walkCommandsAt(spec, func(c *Command, path, ptr string) {
		for i, f := range c.Flags {
			if rf.Prefix == "@" && f.Schema != nil && slices.Contains(f.Schema.From, "file") {
				problems = append(problems, &problem{kind: "spec", ptr: rootPointer + "/response_files/prefix", loc: rootLabel(spec),
					msg: fmt.Sprintf("uses the response-file prefix \"@\", which flag %q of command %s also reads with `from: [file]`; `%s @x` would expand a response file instead of reading x. Choose another prefix, such as \"+\"", f.Name, path, flagIdentifiers(c.Flags[i])[0])})
			}
		}
		for _, a := range c.Arguments {
			if rf.Prefix == "@" && a.Schema != nil && slices.Contains(a.Schema.From, "file") {
				problems = append(problems, &problem{kind: "spec", ptr: rootPointer + "/response_files/prefix", loc: rootLabel(spec),
					msg: fmt.Sprintf("uses the response-file prefix \"@\", which argument %q of command %s also reads with `from: [file]`; `@x` there would expand a response file instead of reading x. Choose another prefix, such as \"+\"", a.Name, path)})
			}
		}
		if c.Passthrough || len(c.Plugins) > 0 || c.PluginDiscovery != nil || hasPassthroughArgument(c) {
			raw = append(raw, path)
		}
	})
	if len(raw) > 0 {
		problems = append(problems, &problem{kind: "spec", ptr: rootPointer + "/response_files", loc: rootLabel(spec), sev: severityWarning,
			msg: fmt.Sprintf("turns on response files, which also expand the words passed on raw by %s (a passthrough or a plugin); a raw word starting with %q is expanded unless it is written after `--` or with the prefix doubled", strings.Join(raw, ", "), rf.Prefix)})
	}
	return problems
}

// hasPassthroughArgument reports whether c declares a passthrough argument.
func hasPassthroughArgument(c *Command) bool {
	for _, a := range c.Arguments {
		if a.Passthrough {
			return true
		}
	}
	return false
}

// lintEnumSummaries checks the `{value, summary}` form of enum members: a summary is one line,
// since help, man and completion show it on one, and only an input's own schema carries
// summaries; an output or stdin schema, or a named schema under `schemas`, takes plain values.
func lintEnumSummaries(spec *Spec) []error {
	var problems []error
	checkLines := func(members []any, report func(msg string)) {
		for _, v := range enumValues(members) {
			if strings.ContainsAny(v.Summary, "\r\n") {
				report(fmt.Sprintf("gives `enum` value %q a summary of more than one line; help and completion show it on one line", v.Value))
			}
		}
	}
	for _, name := range slices.Sorted(maps.Keys(spec.Command.Schemas)) {
		s := spec.Command.Schemas[name]
		if enumDescribed(s.Enum) || schemaHasDescribedEnum(s.Properties) || (s.Items != nil && enumDescribed(s.Items.Enum)) {
			problems = append(problems, &problem{kind: "spec", ptr: rootPointer + "/schemas/" + name, loc: rootLabel(spec),
				msg: fmt.Sprintf("schema %q gives an `enum` value a summary; summaries belong on an input's own schema, so write plain values here", name)})
		}
	}
	walkCommandsAt(spec, func(c *Command, path, ptr string) {
		eachInputAt(c, ptr, func(channel, name, iptr string, schema *InputSchema) {
			if schema == nil {
				return
			}
			if channel == "stdin" {
				if enumDescribed(schema.Enum) || schemaHasDescribedEnum(schema.Properties) || (schema.Items != nil && enumDescribed(schema.Items.Enum)) {
					problems = append(problems, inputProblem(iptr, path, channel, name, "gives an `enum` value a summary; summaries belong on flags, arguments, env and config inputs, so write plain values here"))
				}
				return
			}
			checkLines(schema.Enum, func(msg string) {
				problems = append(problems, inputProblem(iptr+"/schema/enum", path, channel, name, msg))
			})
			if schema.Items != nil {
				checkLines(schema.Items.Enum, func(msg string) {
					problems = append(problems, inputProblem(iptr+"/schema/items/enum", path, channel, name, msg))
				})
			}
		})
		if c.Output != nil && (enumDescribed(c.Output.Enum) || schemaHasDescribedEnum(c.Output.Properties) || (c.Output.Items != nil && enumDescribed(c.Output.Items.Enum))) {
			problems = append(problems, &problem{kind: "spec", ptr: ptr + "/output", loc: "command " + path,
				msg: "gives an `enum` value in `output` a summary; summaries belong on an input's own schema, so write plain values here"})
		}
	})
	return problems
}

// schemaHasDescribedEnum reports whether any property schema, at any depth, gives an enum
// member a summary.
func schemaHasDescribedEnum(props map[string]Schema) bool {
	for _, p := range props {
		if enumDescribed(p.Enum) || schemaHasDescribedEnum(p.Properties) || (p.Items != nil && enumDescribed(p.Items.Enum)) {
			return true
		}
	}
	return false
}
