package codegen

import (
	"errors"
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// lintFlagSets checks `flag_sets` and `use`: sets are declared on the root and used by name,
// not beside `$ref`; a set's groups and dependencies name only its own flags; a command's own
// flags and the sets it uses don't declare the same flag name or generated field name twice;
// and a declared set is used somewhere.
func lintFlagSets(spec *Spec) []error {
	var problems []error
	sets := spec.Command.FlagSets
	names := slices.Sorted(maps.Keys(sets))
	used := map[string]bool{}
	walkCommandsAt(spec, func(c *Command, path, ptr string) {
		add := func(at, msg string) {
			problems = append(problems, &problem{kind: "spec", ptr: at, loc: "command " + path, msg: msg})
		}
		if c != &spec.Command && len(c.FlagSets) > 0 {
			add(ptr+"/flag_sets", "declares `flag_sets`, which only the root command can; move them to the root, where every command of the spec can `use` them")
		}
		if len(c.Use) == 0 {
			return
		}
		if c.Ref != "" {
			add(ptr+"/use", "sets `use` beside `$ref`; the mounted command runs the composed spec's handler, built against that spec's own flags, so declare `use` in the composed spec")
			return
		}
		for _, name := range c.Use {
			used[name] = true
			if _, ok := sets[name]; !ok {
				add(ptr+"/use", didYouMean(fmt.Sprintf("`use` names flag set %q, which the root's `flag_sets` doesn't declare", name), name, names))
			}
		}
		for _, msg := range flagSetClashes(c, sets) {
			add(ptr+"/use", msg)
		}
	})
	for _, name := range names {
		at := rootPointer + "/flag_sets/" + escapePointer(name)
		loc := "flag set " + name
		set := sets[name]
		own := map[string]bool{}
		for _, f := range set.Flags {
			own[f.Name] = true
		}
		check := func(at string, flags []string) {
			for _, f := range flags {
				if f != "" && !own[f] {
					problems = append(problems, &problem{kind: "spec", ptr: at, loc: loc,
						msg: fmt.Sprintf("names flag %q, which is not one of the set's flags; a set's rules can name only its own flags, so the rule holds wherever the set is used", f)})
				}
			}
		}
		for j, g := range set.FlagGroups {
			check(fmt.Sprintf("%s/flag_groups/%d", at, j), g.Flags)
		}
		for j, d := range set.FlagDependencies {
			var named []string
			for _, kn := range dependencyNames(d) {
				named = append(named, kn[1])
			}
			check(fmt.Sprintf("%s/flag_dependencies/%d", at, j), named)
		}
		if !used[name] {
			problems = append(problems, &problem{kind: "spec", ptr: at, loc: loc, sev: severityWarning,
				msg: "is declared but no command uses it; add `use: [" + name + "]` to the commands that should have its flags, or remove it"})
		}
	}
	return problems
}

// flagSetClashes lists the flags c would declare twice once its sets are added: the same flag
// name, or the same generated field name, which would make the promoted field ambiguous or
// hidden. The same identifier is lintDuplicateFlagIdentifiers's to report.
func flagSetClashes(c *Command, sets map[string]FlagSet) []string {
	var msgs []string
	origins := flagOrigins(c, sets)
	byName, byField := map[string]int{}, map[string]int{}
	from := func(i int) string {
		if origins[i].set == "" {
			return "the command's own flags"
		}
		return "flag set " + origins[i].set
	}
	for i, f := range c.Flags {
		if prev, dup := byName[f.Name]; dup && origins[prev].set != origins[i].set {
			msgs = append(msgs, fmt.Sprintf("flag %q is declared by both %s and %s; rename one", f.Name, from(prev), from(i)))
			continue
		}
		byName[f.Name] = i
		field := toPascalCase(f.Name)
		if prev, dup := byField[field]; dup && origins[prev].set != origins[i].set {
			msgs = append(msgs, fmt.Sprintf("flags %q (%s) and %q (%s) both generate the field %s; rename one", c.Flags[prev].Name, from(prev), f.Name, from(i), field))
			continue
		}
		byField[field] = i
	}
	return msgs
}

// setItemPointer matches the pointer of a command's flag, flag group or flag dependency.
var setItemPointer = regexp.MustCompile(`^(.*)/(flags|flag_groups|flag_dependencies)/(\d+)(/.*)?$`)

// remapFlagSetProblems points each problem about a flag, flag group or flag dependency a
// command got from a flag set at the set's declaration instead, where the source has it, and
// reports it once however many commands use the set.
func remapFlagSetProblems(spec *Spec, problems []error) []error {
	sets := spec.Command.FlagSets
	if len(sets) == 0 {
		return problems
	}
	cmds := map[string]*Command{}
	walkCommandsAt(spec, func(c *Command, _, ptr string) { cmds[ptr] = c })
	seen := map[string]bool{}
	out := problems[:0]
	for _, e := range problems {
		p := &problem{}
		if !errors.As(e, &p) || p.pos != "" {
			out = append(out, e)
			continue
		}
		if at, ok := setPointer(cmds, sets, p.ptr); ok {
			p.ptr = at
			p.loc = "flag set " + strings.Split(strings.TrimPrefix(at, rootPointer+"/flag_sets/"), "/")[0]
			key := p.ptr + "\x00" + p.msg
			if seen[key] {
				continue
			}
			seen[key] = true
		}
		out = append(out, e)
	}
	return out
}

// setPointer maps ptr, if it points at an item a command got from a flag set, to the item in
// the set.
func setPointer(cmds map[string]*Command, sets map[string]FlagSet, ptr string) (string, bool) {
	m := setItemPointer.FindStringSubmatch(ptr)
	if m == nil {
		return "", false
	}
	c, ok := cmds[m[1]]
	if !ok || len(c.Use) == 0 {
		return "", false
	}
	i, err := strconv.Atoi(m[3])
	if err != nil {
		return "", false
	}
	var own int
	var size func(FlagSet) int
	switch m[2] {
	case "flags":
		own, size = ownFlagCount(c, sets), func(s FlagSet) int { return len(s.Flags) }
	case "flag_groups":
		own, size = ownGroupCount(c, sets), func(s FlagSet) int { return len(s.FlagGroups) }
	default:
		own, size = ownDependencyCount(c, sets), func(s FlagSet) int { return len(s.FlagDependencies) }
	}
	if i < own {
		return "", false
	}
	i -= own
	for _, name := range c.Use {
		set, ok := sets[name]
		if !ok {
			continue
		}
		if i < size(set) {
			return fmt.Sprintf("%s/flag_sets/%s/%s/%d%s", rootPointer, escapePointer(name), m[2], i, m[4]), true
		}
		i -= size(set)
	}
	return "", false
}
