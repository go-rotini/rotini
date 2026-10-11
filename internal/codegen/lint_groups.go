package codegen

import "fmt"

// dependencyFlag is the flag whose presence triggers a flag_dependencies entry.
func dependencyFlag(d FlagDependency) string { return d.When }

// atMostOne reports whether a flag_groups kind allows at most one member to be set.
func atMostOne(kind string) bool { return kind == "mutually_exclusive" || kind == "one_of" }

// flagPair is an unordered pair of flag names, stored sorted.
type flagPair [2]string

func pairOf(a, b string) flagPair {
	if b < a {
		a, b = b, a
	}
	return flagPair{a, b}
}

// lintGroupConflicts rejects flag groups and dependencies that contradict each other, so a
// flag could never be set:
//   - a pair both required_together and in an "at most one" group (mutually_exclusive, one_of);
//   - a dependency between two members of an "at most one" group;
//   - a required member of a mutually_exclusive group with no env or config fallback, which
//     leaves the other members unusable. With a fallback the requirement can be met from env
//     or config while another member is given on the command line, so that is fine.
func lintGroupConflicts(spec *Spec) []error {
	var problems []error
	walkCommandsAt(spec, func(c *Command, path, ptr string) {
		if len(c.FlagGroups) == 0 {
			return
		}
		add := func(at, msg string) {
			problems = append(problems, &problem{kind: "spec", ptr: at, loc: "command " + path, msg: msg})
		}
		atMost := atMostPairs(c)
		togetherConflicts(c, ptr, atMost, add)
		dependencyConflicts(c, ptr, atMost, add)
		requiredExclusiveConflicts(c, ptr, add)
	})
	return problems
}

// atMostPairs maps each pair of flags in an "at most one" group to the first such group's
// index. Short-circuit flags are left out: lintShortCircuit rejects them in groups.
func atMostPairs(c *Command) map[flagPair]int {
	waived := map[string]bool{}
	for _, f := range c.Flags {
		waived[f.Name] = f.ShortCircuit
	}
	atMost := map[flagPair]int{}
	for i, g := range c.FlagGroups {
		if !atMostOne(g.Kind) {
			continue
		}
		for _, p := range groupPairs(g) {
			if _, seen := atMost[pairOf(p[0], p[1])]; !seen && !waived[p[0]] && !waived[p[1]] {
				atMost[pairOf(p[0], p[1])] = i
			}
		}
	}
	return atMost
}

// groupPairs lists every pair of distinct members of a group, in declared order; a repeated member is
// lintFlagGroups's to report.
func groupPairs(g FlagGroup) [][2]string {
	var out [][2]string
	for a := range g.Flags {
		for b := a + 1; b < len(g.Flags); b++ {
			if g.Flags[a] != g.Flags[b] {
				out = append(out, [2]string{g.Flags[a], g.Flags[b]})
			}
		}
	}
	return out
}

// groupMembers returns a group's members as a set.
func groupMembers(g FlagGroup) map[string]bool {
	set := map[string]bool{}
	for _, f := range g.Flags {
		set[f] = true
	}
	return set
}

// togetherConflicts reports each required_together group that shares a pair with an "at most
// one" group, once per pair of entries, at the later entry.
func togetherConflicts(c *Command, ptr string, atMost map[flagPair]int, add func(at, msg string)) {
	reported := map[[2]int]bool{}
	for i, g := range c.FlagGroups {
		if g.Kind != "required_together" {
			continue
		}
		for _, p := range groupPairs(g) {
			j, clash := atMost[pairOf(p[0], p[1])]
			if !clash || reported[[2]int{i, j}] {
				continue
			}
			reported[[2]int{i, j}] = true
			other := c.FlagGroups[j]
			outcome := "so neither can ever be set"
			if other.Kind == "one_of" && subset(groupMembers(other), groupMembers(g)) {
				outcome = "so no invocation can satisfy this command"
			}
			add(fmt.Sprintf("%s/flag_groups/%d", ptr, max(i, j)), fmt.Sprintf("flags %q and %q must be set together (a required_together group) but at most one of them may be set (a %s group), %s; remove one of the two `flag_groups` entries",
				p[0], p[1], other.Kind, outcome))
		}
	}
}

// dependencyConflicts reports a dependency between two members of an "at most one" group: the
// dependent flag can never be set.
func dependencyConflicts(c *Command, ptr string, atMost map[flagPair]int, add func(at, msg string)) {
	for i, d := range c.FlagDependencies {
		when := dependencyFlag(d)
		for _, r := range d.Requires {
			j, clash := atMost[pairOf(when, r)]
			if r == when || !clash {
				continue // a self-dependency: lintFlagDependencies
			}
			add(fmt.Sprintf("%s/flag_dependencies/%d", ptr, i), fmt.Sprintf("`flag_dependencies` entry makes flag %q require %q, but `flag_groups` entry (%s) allows at most one of them, so %q can never be set; remove the dependency or the group",
				when, r, c.FlagGroups[j].Kind, when))
		}
	}
}

// requiredExclusiveConflicts reports a required member of a mutually_exclusive group that only
// the command line can set, which leaves the other members unusable.
func requiredExclusiveConflicts(c *Command, ptr string, add func(at, msg string)) {
	flags := map[string]*InputSchema{}
	for _, f := range c.Flags {
		flags[f.Name] = f.Schema
	}
	for i, g := range c.FlagGroups {
		if g.Kind != "mutually_exclusive" || len(g.Flags) < 2 {
			continue
		}
		for _, name := range g.Flags {
			if s := flags[name]; s != nil && s.Required && flagReconKey(name, s) == "" {
				add(fmt.Sprintf("%s/flag_groups/%d", ptr, i), fmt.Sprintf("`flag_groups` entry (mutually_exclusive) includes required flag %q, so the other members can never be set; drop `required`, or give %q an env or config fallback", name, name))
			}
		}
	}
}

// subset reports whether every member of a is in b.
func subset(a, b map[string]bool) bool {
	for k := range a {
		if !b[k] {
			return false
		}
	}
	return true
}
