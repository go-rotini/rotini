package codegen

import (
	"fmt"
	"slices"
	"strings"
)

// dependencyEquals is a flag_dependencies entry's `equals` values as text, the form the
// runtime compares.
func dependencyEquals(d FlagDependency) []string {
	out := make([]string, len(d.Equals))
	for i, v := range d.Equals {
		out[i] = defaultString(v)
	}
	return out
}

// dependencyNames is every flag a flag_dependencies entry names, with the key naming it.
func dependencyNames(d FlagDependency) [][2]string {
	var out [][2]string
	if d.When != "" {
		out = append(out, [2]string{"when", d.When})
	}
	for key, names := range map[string][]string{"unless": d.Unless, "requires": d.Requires, "forbids": d.Forbids} {
		for _, n := range names {
			out = append(out, [2]string{key, n})
		}
	}
	slices.SortStableFunc(out, func(a, b [2]string) int { return dependencyKeyOrder(a[0]) - dependencyKeyOrder(b[0]) })
	return out
}

// dependencyKeyOrder orders an entry's keys as the spec reference lists them.
func dependencyKeyOrder(key string) int {
	return slices.Index([]string{"when", "unless", "requires", "forbids"}, key)
}

// lintFlagDependencies checks each flag_dependencies entry: every name is a flag of the
// command; it has a trigger (`when` or `unless`) and a consequence (`requires` or `forbids`);
// `equals` sits with a `when` flag holding one value, and each value is one the flag accepts;
// and no flag is asked to be and not be set at once.
func lintFlagDependencies(spec *Spec) []error {
	var problems []error
	walkCommandsAt(spec, func(c *Command, path, ptr string) {
		if c.inputs() == nil || len(c.inputs().FlagDependencies) == 0 {
			return
		}
		known, ordered := flagNames(c)
		for i, dep := range c.FlagDependencies {
			at := fmt.Sprintf("%s/flag_dependencies/%d", ptr, i)
			add := func(msg string) {
				problems = append(problems, &problem{kind: "spec", ptr: at, loc: "command " + path, msg: msg})
			}
			for _, kn := range dependencyNames(dep) {
				if !known[kn[1]] {
					add(didYouMean(fmt.Sprintf("`flag_dependencies` entry references unknown flag %q; it has no matching entry in this command's `flags`", kn[1]), kn[1], ordered))
				}
			}
			if dep.When == "" && len(dep.Unless) == 0 {
				add("`flag_dependencies` entry has no trigger; give `when` (the flag that turns the rule on) or `unless` (flags that turn it off)")
			}
			if len(dep.Requires) == 0 && len(dep.Forbids) == 0 {
				add("`flag_dependencies` entry has no effect; give `requires` (flags that must be set) or `forbids` (flags that can't be)")
			}
			for _, msg := range dependencyContradictions(dep) {
				add(msg)
			}
			if len(dep.Equals) > 0 {
				for _, msg := range dependencyEqualsProblems(c, spec.Command.Schemas, dep) {
					add(msg)
				}
			}
		}
	})
	return problems
}

// dependencyContradictions lists the ways an entry asks for a flag to be and not be set.
func dependencyContradictions(dep FlagDependency) []string {
	var msgs []string
	if dep.When != "" && slices.Contains(dep.Requires, dep.When) {
		msgs = append(msgs, fmt.Sprintf("`flag_dependencies` entry makes flag %q require itself, which is always true; remove %q from `requires`", dep.When, dep.When))
	}
	if dep.When != "" && slices.Contains(dep.Forbids, dep.When) {
		msgs = append(msgs, fmt.Sprintf("`flag_dependencies` entry forbids flag %q when %q is set, so it can never be set; remove %q from `forbids`", dep.When, dep.When, dep.When))
	}
	if dep.When != "" && slices.Contains(dep.Unless, dep.When) {
		msgs = append(msgs, fmt.Sprintf("`flag_dependencies` entry lists flag %q in both `when` and `unless`, so it never applies; remove it from `unless`", dep.When))
	}
	for _, name := range dep.Requires {
		if slices.Contains(dep.Forbids, name) {
			msgs = append(msgs, fmt.Sprintf("`flag_dependencies` entry both requires and forbids flag %q; keep it in one of `requires` and `forbids`", name))
		}
		if slices.Contains(dep.Unless, name) {
			msgs = append(msgs, fmt.Sprintf("`flag_dependencies` entry requires flag %q, which also turns the rule off (`unless`), so the requirement can never fail; remove it from one of the two", name))
		}
	}
	return msgs
}

// dependencyEqualsProblems checks an entry's `equals`: it needs a `when` flag that holds one
// value, and each value must be one that flag accepts.
func dependencyEqualsProblems(c *Command, schemas map[string]Schema, dep FlagDependency) []string {
	if dep.When == "" {
		return []string{"`flag_dependencies` entry sets `equals` without `when`; `equals` names values of the `when` flag"}
	}
	i := slices.IndexFunc(c.Flags, func(f FlagInput) bool { return f.Name == dep.When })
	if i < 0 {
		return nil // unknown flag: reported above
	}
	schema := c.Flags[i].Schema
	if schema == nil {
		schema = &InputSchema{}
	}
	t := definitionType(schema, schemas)
	if t == "count" || strings.HasPrefix(t, "[]") || strings.HasPrefix(t, "map[") || objectRef(schema, schemas) != "" {
		return []string{fmt.Sprintf("`flag_dependencies` entry sets `equals` on flag %q, which is a %s; `equals` compares one value, so the `when` flag must hold a single value (a string, number, bool or enum)", dep.When, displayType(getSchemaType(schema)))}
	}
	var msgs []string
	values := dependencyEquals(dep)
	for _, v := range values {
		if len(schema.Enum) > 0 && !enumMember(schema, v) {
			msgs = append(msgs, fmt.Sprintf("`flag_dependencies` entry compares flag %q with %q, which is not one of its `enum` values (%s), so the rule never applies", dep.When, v, strings.Join(enumStrings(schema.Enum), ", ")))
			continue
		}
		if complaint := runtimeRejects(schema, []string{v}); complaint != "" {
			msgs = append(msgs, fmt.Sprintf("`flag_dependencies` entry compares flag %q with a value it can't take: %s, so the rule never applies", dep.When, complaint))
		}
	}
	return msgs
}
