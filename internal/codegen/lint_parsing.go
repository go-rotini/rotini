package codegen

import (
	"cmp"
	"fmt"
	"maps"
	"math"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/go-rotini/rotini"
)

// Rules for how input values are spelled: negated forms, argument fallbacks, time layouts and
// relative times, and the regexp and glob kinds.

// lintNegatable restricts negatable to bool flags, with a long identifier to derive
// "--no-<name>" from unless it names its own form, and rejects a negated form another
// identifier on the command path already means: a declared identifier wins, so the negated
// form would never match.
func lintNegatable(spec *Spec) []error {
	var problems []error
	walkChainsAt(spec, func(chain []*Command, path, ptr string) {
		c := chain[len(chain)-1]
		flagPtr := func(i int) string { return fmt.Sprintf("%s/flags/%d", ptr, i) }
		declared := map[string]string{} // identifier → the flag declaring it, ancestors included
		for _, a := range chain {
			for _, f := range a.Flags {
				for _, id := range flagIdentifiers(f) {
					declared[id] = f.Name
				}
			}
		}
		negated := map[string]string{} // negated form → its flag, on this command
		for _, f := range c.Flags {
			for _, neg := range negatedForms(f) {
				if _, ok := negated[neg]; !ok {
					negated[neg] = f.Name
				}
			}
		}
		for i, f := range c.Flags {
			for _, msg := range negationProblems(f, declared, negated) {
				problems = append(problems, inputProblem(flagPtr(i), path, "flag", f.Name, msg))
			}
		}
	})
	return problems
}

// negationProblems checks one flag's negated forms against the identifiers declared on its
// command path and the negated forms of its command's other flags.
func negationProblems(f FlagInput, declared, negated map[string]string) []string {
	on, custom := negation(f.Schema)
	if !on {
		return nil
	}
	if t := f.Schema.Type; t != "bool" && t != "boolean" {
		return []string{fmt.Sprintf("sets `negatable` but its type is %s; the negated form sets a bool false, so it applies to bool flags only", displayType(t))}
	}
	var msgs []string
	if custom == "" && !slices.ContainsFunc(flagIdentifiers(f), func(id string) bool { return strings.HasPrefix(id, "--") }) {
		msgs = append(msgs, "sets `negatable` but declares no long identifier; the negated form is derived as \"--no-<name>\", so there is nothing to derive it from")
	}
	what := "deriving"
	if custom != "" {
		what = "naming"
	}
	for _, neg := range negatedForms(f) {
		if owner, clash := declared[neg]; clash {
			msgs = append(msgs, fmt.Sprintf("sets `negatable`, %s %q, which flag %q already declares; one of the two would never match", what, neg, owner))
		} else if owner := negated[neg]; owner != f.Name {
			msgs = append(msgs, fmt.Sprintf("sets `negatable`, naming %q, which is also the negated form of flag %q; give each flag its own", neg, owner))
		}
	}
	return msgs
}

// lintArgumentFallback keeps an argument's env and config fallback (`variable:`, `key:`) to
// where a position can be filled: positionals fill left to right, so an argument after one
// with a fallback needs a fallback or a default too, or a typed word would land in the earlier
// argument. Raw words take no fallback: not on a passthrough argument, the arguments of a
// passthrough command, or an argument after a variadic one, whose position is unknown.
func lintArgumentFallback(spec *Spec) []error {
	var problems []error
	walkCommandsAt(spec, func(c *Command, path, ptr string) {
		seenFallback := ""
		afterVariadic := false
		for i, a := range c.Arguments {
			add := func(msg string) {
				problems = append(problems, inputProblem(fmt.Sprintf("%s/arguments/%d", ptr, i), path, "argument", a.Name, msg))
			}
			fallback := a.Schema != nil && (a.Schema.Key != "" || len(variables(a.Schema)) > 0)
			hasDefault := a.Schema != nil && a.Schema.Default != nil
			switch {
			case fallback && a.Passthrough:
				add("sets `variable` or `key` on the passthrough argument, whose words are taken as typed; a fallback would never apply")
			case fallback && c.Passthrough:
				add(fmt.Sprintf("sets `variable` or `key`, but %q is a passthrough command, whose words are taken as typed; a fallback would never apply", c.Name))
			case fallback && afterVariadic:
				add("sets `variable` or `key` after a variadic argument, so its position isn't known until every word is in; move the fallback to an argument before the variadic one")
			case seenFallback != "" && !fallback && !hasDefault && !a.Passthrough:
				add(fmt.Sprintf("has neither a fallback nor a default, but comes after <%s>, which has a fallback; when <%s> is left out, a word typed for <%s> fills <%s> instead. Give it a `variable`, `key` or `default`, or move it before <%s>",
					seenFallback, seenFallback, a.Name, seenFallback, seenFallback))
			}
			if fallback && seenFallback == "" {
				seenFallback = a.Name
			}
			if strings.HasPrefix(getSchemaType(a.Schema), "[]") {
				afterVariadic = true
			}
		}
	})
	return problems
}

// lintRelativeTime keeps `relative` to time inputs. A flag's or argument's relative default is
// checked by lintValuesParse, which reads it the way a run does; an env or config input's
// default is read as an absolute time.
func lintRelativeTime(spec *Spec) []error {
	var problems []error
	walkCommandsAt(spec, func(c *Command, path, ptr string) {
		eachInputAt(c, ptr, func(channel, name, ptr string, schema *InputSchema) {
			if relative(schema) == "" {
				return
			}
			add := func(msg string) { problems = append(problems, inputProblem(ptr, path, channel, name, msg)) }
			if channel == "stdin" {
				add("sets `relative` on stdin, which is a document, not a time; declare it on a time input")
				return
			}
			if t := strings.TrimPrefix(getSchemaType(schema), "[]"); t != "time.Time" && t != "*time.Time" {
				add(fmt.Sprintf("sets `relative` but its type is %s; a relative value is a time measured from now, so it applies to time, datetime and date", displayType(getSchemaType(schema))))
				return
			}
			if d := defaultString(schema.Default); d != "" && (channel == "env" || channel == "config") {
				absolute := *schema
				absolute.Relative = ""
				if runtimeRejects(&absolute, []string{d}) != "" {
					add(fmt.Sprintf("has a relative `default` %q; an env or config input's default is read as an absolute time, so write one (2026-09-29T14:00:00Z), or give a flag the relative default", d))
				}
			}
		})
	})
	return problems
}

// lintPatternKinds keeps the regexp and glob kinds to what they mean: a pattern is a value
// that is itself matched against, so an enum would check the pattern's text, and a regexp is
// already a pointer, so nullable adds nothing. Pattern and length bounds, which apply to
// strings only, are lintConstraintApplicability's.
func lintPatternKinds(spec *Spec) []error {
	var problems []error
	walkCommandsAt(spec, func(c *Command, path, ptr string) {
		eachInputAt(c, ptr, func(channel, name, ptr string, schema *InputSchema) {
			kind := patternKind(schema)
			if kind == "" {
				return
			}
			add := func(msg string) { problems = append(problems, inputProblem(ptr, path, channel, name, msg)) }
			if len(schema.Enum) > 0 || (schema.Items != nil && len(schema.Items.Enum) > 0) {
				add(fmt.Sprintf("sets `enum` on a %s input, which would check the pattern's text, not what it matches; remove it", kind))
			}
			if kind == "glob" && schema.Glob {
				add("sets `glob: true` on a glob input: `glob: true` expands a pattern into file names, while `type: glob` keeps the pattern as a value; use one")
			}
			if kind == "regexp" && schema.Nullable {
				add("sets `nullable` on a regexp input, whose field is already a pointer (*regexp.Regexp), nil when unset; remove it")
			}
		})
	})
	return problems
}

// patternKind is "regexp" or "glob" for an input of that kind (or a list of them), else "".
func patternKind(schema *InputSchema) string {
	if schema == nil {
		return ""
	}
	t := strings.TrimPrefix(schema.Type, "[]")
	if (schema.Type == "array" || schema.Type == "[]string") && schema.Items != nil {
		t = schema.Items.Type
	}
	if t == "regexp" || t == "glob" {
		return t
	}
	return ""
}

// shadowingLayout returns the index of the first of earlier that reads a value written under
// layout as a different time, so layout is never tried for it; -1 when none does.
func shadowingLayout(earlier []string, layout string) int {
	sample, want := layoutSample(layout)
	for j, e := range earlier {
		if got, ok := readLayout(e, sample); ok && !got.Equal(want) {
			return j
		}
	}
	return -1
}

// layoutSample writes sampleTime under layout, returning the text and the time it reads back as.
func layoutSample(layout string) (string, time.Time) {
	switch layout {
	case "unix":
		return strconv.FormatInt(sampleTime.Unix(), 10), sampleTime
	case "unixmilli":
		return strconv.FormatInt(sampleTime.UnixMilli(), 10), sampleTime
	}
	s := sampleTime.Format(layout)
	t, err := time.Parse(layout, s)
	if err != nil {
		return s, time.Time{} // lintLayout reports a layout that can't read what it writes
	}
	return s, t
}

// readLayout reads s under layout as the runtime does.
func readLayout(layout, s string) (time.Time, bool) {
	switch layout {
	case "unix":
		secs, err := strconv.ParseFloat(s, 64)
		if err != nil {
			return time.Time{}, false
		}
		whole, frac := math.Modf(secs)
		return time.Unix(int64(whole), int64(math.Round(frac*1e9))).UTC(), true
	case "unixmilli":
		ms, err := strconv.ParseInt(s, 10, 64)
		if err != nil {
			return time.Time{}, false
		}
		return time.UnixMilli(ms).UTC(), true
	}
	t, err := time.Parse(layout, s)
	return t, err == nil
}

// lintContext is the context lint parses a value in, as a run of def with argv would, with the
// clock fixed at sampleTime so a relative value parses the same way every time.
func lintContext(def rotini.Definition, argv []string) *rotini.Context {
	return rotini.NewContextFor(def, argv).WithClock(func() time.Time { return sampleTime })
}

// withTimeForms sets fd's time layouts and relative forms from schema, as generated code does.
func withTimeForms(fd *rotini.FlagDef, schema *InputSchema) {
	if l := layoutsFor(schema); len(l) > 0 {
		fd.Layout = l[0]
		if len(l) > 1 {
			fd.Layouts = l
		}
	}
	fd.Relative = relative(schema)
}

// lintFrom restricts `from` to non-bool flags and to arguments whose position is known as the
// words arrive (before any variadic argument, and not raw words), and allows one stdin consumer
// on a command path: a from:stdin flag or argument, an ancestor's from:stdin flag, and a
// `stdin:` channel all read the same stream, which can be read once.
func lintFrom(spec *Spec) []error {
	var problems []error
	walkChainsAt(spec, func(chain []*Command, path, ptr string) {
		c := chain[len(chain)-1]
		stdinClaim := ancestorStdinClaim(chain[:len(chain)-1]) // what already claimed stdin on this command path
		if c.Stdin != nil {
			if stdinClaim != "" {
				problems = append(problems, &problem{kind: "spec", ptr: ptr + "/stdin", loc: "command " + path,
					msg: fmt.Sprintf("declares `stdin`, but %s takes `from: stdin` on the same command path; stdin has one consumer", stdinClaim)})
			} else {
				stdinClaim = "the `stdin` channel"
			}
		}
		variadicAt := slices.IndexFunc(c.Arguments, func(a ArgumentInput) bool { return strings.HasPrefix(getSchemaType(a.Schema), "[]") })
		argIndex := map[string]int{}
		for i, a := range c.Arguments {
			argIndex[a.Name] = i
		}
		eachInputAt(c, ptr, func(channel, name, ptr string, schema *InputSchema) {
			if schema == nil || len(schema.From) == 0 {
				return
			}
			add := func(msg string) { problems = append(problems, inputProblem(ptr, path, channel, name, msg)) }
			switch channel {
			case "flag":
				if getSchemaType(schema) == "bool" {
					add("sets `from` but its type is bool; a bool takes no value to resolve")
				}
			case "argument":
				if msg := argumentFromProblem(c, argIndex[name], variadicAt); msg != "" {
					add(msg)
					return
				}
			default:
				add("sets `from`, which applies to flags and arguments only")
				return
			}
			if slices.Contains(schema.From, "stdin") {
				if stdinClaim != "" {
					add(fmt.Sprintf("sets `from: stdin` but %s already consumes stdin; stdin has one consumer", stdinClaim))
					return
				}
				stdinClaim = fmt.Sprintf("%s %q", channel, name)
			}
		})
	})
	return problems
}

// lintEnumValueKeys checks what enum values declare beyond a summary: aliases are spellings of
// their own (no alias repeats another alias or a value, in the enum's case when it ignores
// case), deprecated_aliases are among the aliases, the deprecation plan needs a deprecation
// and runs forward, replaced_by names a value still in use, a default is written as a value
// rather than an alias, and something is left to offer. Only an input's own enum declares
// these; an output, stdin or named schema takes plain values.
func lintEnumValueKeys(spec *Spec) []error {
	var problems []error
	for _, name := range slices.Sorted(maps.Keys(spec.Command.Schemas)) {
		s := spec.Command.Schemas[name]
		if schemaHasEnumDetail(s) {
			problems = append(problems, &problem{kind: "spec", ptr: rootPointer + "/schemas/" + name, loc: rootLabel(spec),
				msg: fmt.Sprintf("schema %q gives an `enum` value aliases, `hidden` or `deprecated`; these belong on an input's own schema, so write plain values here", name)})
		}
	}
	walkCommandsAt(spec, func(c *Command, path, ptr string) {
		if c.Output != nil && schemaHasEnumDetail(*c.Output) {
			problems = append(problems, &problem{kind: "spec", ptr: ptr + "/output", loc: "command " + path,
				msg: "gives an `enum` value in `output` aliases, `hidden` or `deprecated`; these describe what a user types, so write plain values here"})
		}
		eachInputAt(c, ptr, func(channel, name, iptr string, schema *InputSchema) {
			if schema == nil {
				return
			}
			add := func(msg string) { problems = append(problems, inputProblem(iptr, path, channel, name, msg)) }
			for _, members := range [][]any{schema.Enum, itemsEnum(schema)} {
				if !enumHasDetail(members) {
					continue
				}
				if channel == "stdin" {
					add("gives an `enum` value aliases, `hidden` or `deprecated`; these describe what a user types, so write plain values here")
					return
				}
				for _, msg := range enumValueProblems(enumValues(members), schema.IgnoreCase) {
					add(msg)
				}
				if d := defaultString(schema.Default); d != "" {
					for _, v := range append(defaultList(schema.Default), d) {
						if to, ok := enumAliases(members)[v]; ok {
							add(fmt.Sprintf("has a `default` of %q, an alias; write the value %q, not its alias", v, to))
							break
						}
					}
				}
			}
		})
	})
	return problems
}

// itemsEnum is a list input's per-item enum, nil when it has none.
func itemsEnum(schema *InputSchema) []any {
	if schema.Items == nil {
		return nil
	}
	return schema.Items.Enum
}

// enumHasDetail reports whether any member declares aliases, hidden or a deprecation.
func enumHasDetail(members []any) bool {
	return slices.ContainsFunc(enumValues(members), enumValue.detailed)
}

// enumValueProblems checks one enum's declared values; see lintEnumValueKeys.
func enumValueProblems(values []enumValue, ignoreCase bool) []string {
	var msgs []string
	fold := func(s string) string {
		if ignoreCase {
			return strings.ToLower(s)
		}
		return s
	}
	owner := map[string]string{} // spelling (folded) → the value it means
	for _, v := range values {
		owner[fold(v.Value)] = v.Value
	}
	byValue := map[string]enumValue{}
	for _, v := range values {
		byValue[v.Value] = v
	}
	offered := false
	for _, v := range values {
		for _, a := range v.Aliases {
			if prev, ok := owner[fold(a)]; ok {
				msgs = append(msgs, fmt.Sprintf("gives `enum` value %q the alias %q, which already means %q; each spelling means one value", v.Value, a, prev))
				continue
			}
			owner[fold(a)] = v.Value
		}
		for _, a := range v.DeprecatedAliases {
			if !slices.Contains(v.Aliases, a) {
				msgs = append(msgs, fmt.Sprintf("lists %q in `deprecated_aliases` of `enum` value %q, but not in its `aliases`; deprecate an alias it declares", a, v.Value))
			}
		}
		if v.Deprecated == "" && (v.DeprecatedSince != "" || v.RemovedIn != "" || v.ReplacedBy != "") {
			msgs = append(msgs, fmt.Sprintf("gives `enum` value %q `deprecated_since`, `removed_in` or `replaced_by` without `deprecated`; they plan a deprecation, so say why with `deprecated`", v.Value))
		}
		if v.DeprecatedSince != "" && v.RemovedIn != "" && compareVersions(v.RemovedIn, v.DeprecatedSince) <= 0 {
			msgs = append(msgs, fmt.Sprintf("gives `enum` value %q `removed_in: %s`, not after `deprecated_since: %s`", v.Value, v.RemovedIn, v.DeprecatedSince))
		}
		if r := v.ReplacedBy; r != "" {
			to, ok := byValue[r]
			switch {
			case !ok:
				msgs = append(msgs, fmt.Sprintf("gives `enum` value %q `replaced_by: %s`, which is not a value of this enum", v.Value, r))
			case r == v.Value:
				msgs = append(msgs, fmt.Sprintf("gives `enum` value %q `replaced_by` itself", v.Value))
			case to.Deprecated != "":
				msgs = append(msgs, fmt.Sprintf("gives `enum` value %q `replaced_by: %s`, which is deprecated too; name the value to move to", v.Value, r))
			}
		}
		if v.listed() {
			offered = true
		}
	}
	if !offered {
		msgs = append(msgs, "marks every `enum` value hidden or deprecated, so help, completion and errors have nothing to offer; leave at least one value in use")
	}
	return msgs
}

// compareVersions compares two MAJOR.MINOR.PATCH versions, as -1, 0 or 1.
func compareVersions(a, b string) int {
	pa, pb := strings.Split(a, "."), strings.Split(b, ".")
	for i := range min(len(pa), len(pb)) {
		x, y := versionPart(pa[i]), versionPart(pb[i])
		if c := cmp.Compare(x, y); c != 0 {
			return c
		}
	}
	return cmp.Compare(len(pa), len(pb))
}

// schemaHasEnumDetail reports whether s, or any schema inside it, gives an enum value aliases,
// hidden or a deprecation.
func schemaHasEnumDetail(s Schema) bool {
	if enumHasDetail(s.Enum) || (s.Items != nil && schemaHasEnumDetail(*s.Items)) {
		return true
	}
	for _, p := range s.Properties {
		if schemaHasEnumDetail(p) {
			return true
		}
	}
	return false
}

// versionPart reads one part of a version as a number; the schema's pattern makes it one.
func versionPart(s string) int {
	n, err := strconv.Atoi(s)
	if err != nil {
		return 0
	}
	return n
}

// ancestorStdinClaim names the first from:stdin flag of the ancestors, which every command below
// them shares stdin with; "" when none takes it.
func ancestorStdinClaim(ancestors []*Command) string {
	for _, a := range ancestors {
		for _, f := range a.Flags {
			if f.Schema != nil && slices.Contains(f.Schema.From, "stdin") {
				return fmt.Sprintf("flag %q of %q", f.Name, a.Name)
			}
		}
	}
	return ""
}

// argumentFromProblem says why argument i of c can't take `from`, or "": its words are raw, or
// its position isn't known as the words arrive. variadicAt is the first variadic argument's
// index, -1 for none.
func argumentFromProblem(c *Command, i, variadicAt int) string {
	switch {
	case c.Arguments[i].Passthrough || c.Passthrough:
		return "sets `from` on raw words, which are taken as typed; `@` and `-` stay literal there"
	case variadicAt >= 0 && i >= variadicAt:
		return "sets `from` on a variadic argument, or one after it; only an argument before any variadic one takes `from`, since its position is known as the words arrive"
	}
	return ""
}
