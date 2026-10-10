package contractdiff

import (
	"fmt"
	"slices"
	"strings"
)

// Flag groups and flag dependencies narrow which command lines a command accepts. A rule that
// accepts fewer command lines than before is breaking; one that accepts more is safe.

// groupWhere names a flag group by its kind and sorted flags.
func groupWhere(cmd string, g flagGroup) string {
	return cmd + " group " + g.Kind + "(" + sortedJoin(g.Flags) + ")"
}

// flagGroups matches each old group to the new one with the same kind and flags, else the same
// flags, else the same kind and a shared flag, and compares the pair.
func (d *differ) flagGroups(st stability, oc, nc *command) {
	used := make([]bool, len(nc.FlagGroups))
	find := func(match func(o, n flagGroup) bool, og flagGroup) int {
		for i, ng := range nc.FlagGroups {
			if !used[i] && match(og, ng) {
				return i
			}
		}
		return -1
	}
	same := func(a, b []string) bool { return sortedJoin(a) == sortedJoin(b) }
	shares := func(a, b []string) bool {
		return slices.ContainsFunc(a, func(f string) bool { return slices.Contains(b, f) })
	}
	type pair struct{ o, n int }
	var pairs []pair
	matchedOld := make([]bool, len(oc.FlagGroups))
	for _, match := range []func(o, n flagGroup) bool{
		func(o, n flagGroup) bool { return o.Kind == n.Kind && same(o.Flags, n.Flags) },
		func(o, n flagGroup) bool { return same(o.Flags, n.Flags) },
		func(o, n flagGroup) bool { return o.Kind == n.Kind && shares(o.Flags, n.Flags) },
	} {
		for i, og := range oc.FlagGroups {
			if matchedOld[i] {
				continue
			}
			if j := find(match, og); j >= 0 {
				used[j], matchedOld[i] = true, true
				pairs = append(pairs, pair{i, j})
			}
		}
	}
	for _, p := range pairs {
		d.flagGroup(st, oc.Name, oc.FlagGroups[p.o], nc.FlagGroups[p.n])
	}
	for i, og := range oc.FlagGroups {
		if !matchedOld[i] {
			d.add(st, Safe, RuleFlagGroupRemoved, groupWhere(oc.Name, og), "flag group removed", "")
		}
	}
	for j, ng := range nc.FlagGroups {
		if !used[j] {
			d.add(st, Breaking, RuleFlagGroupAdded, groupWhere(oc.Name, ng), "flag group added", "")
		}
	}
}

// flagGroup compares a matched pair of groups.
func (d *differ) flagGroup(st stability, cmd string, o, n flagGroup) {
	w := groupWhere(cmd, o)
	if o.Kind != n.Kind {
		msg := "kind " + o.Kind + " → " + n.Kind
		if groupKindLooser(o.Kind, n.Kind) {
			d.add(st, Safe, RuleFlagGroupLoosened, w, msg, "")
		} else {
			d.add(st, Breaking, RuleFlagGroupTightened, w, msg, "")
		}
		return
	}
	var added, removed []string
	for _, f := range n.Flags {
		if !slices.Contains(o.Flags, f) {
			added = append(added, f)
		}
	}
	for _, f := range o.Flags {
		if !slices.Contains(n.Flags, f) {
			removed = append(removed, f)
		}
	}
	if len(added)+len(removed) == 0 {
		return
	}
	msg := groupChange(added, removed)
	// Adding a flag to an at_least_one group accepts more command lines, removing one fewer; the
	// other kinds work the other way round, and one_of both ways at once.
	tighter := len(added) > 0
	looser := len(removed) > 0
	if o.Kind == "at_least_one" {
		tighter, looser = looser, tighter
	}
	if o.Kind == "one_of" {
		tighter = true
	}
	if tighter {
		d.add(st, Breaking, RuleFlagGroupTightened, w, msg, "")
	} else if looser {
		d.add(st, Safe, RuleFlagGroupLoosened, w, msg, "")
	}
}

// groupKindLooser reports whether kind n accepts every command line kind o does: one_of is
// both mutually_exclusive and at_least_one.
func groupKindLooser(o, n string) bool {
	return o == "one_of" && (n == "mutually_exclusive" || n == "at_least_one")
}

func groupChange(added, removed []string) string {
	var parts []string
	if len(added) > 0 {
		parts = append(parts, "flags added: "+strings.Join(added, ", "))
	}
	if len(removed) > 0 {
		parts = append(parts, "flags removed: "+strings.Join(removed, ", "))
	}
	return strings.Join(parts, "; ")
}

// dependencyKey is how flag dependencies match: by `when` and the sorted `unless`.
func dependencyKey(dep flagDependency) string {
	return dep.When + "|" + sortedJoin(dep.Unless)
}

func dependencyWhere(cmd string, dep flagDependency) string {
	w := cmd + " dependency"
	if dep.When != "" {
		w += " " + dep.When
	}
	if len(dep.Unless) > 0 {
		w += " unless " + sortedJoin(dep.Unless)
	}
	return w
}

// flagDependencies compares flag dependencies, matched by `when` and `unless`. A changed key
// is a removal plus an addition.
func (d *differ) flagDependencies(st stability, oc, nc *command) {
	for _, od := range oc.FlagDependencies {
		w := dependencyWhere(oc.Name, od)
		i := slices.IndexFunc(nc.FlagDependencies, func(n flagDependency) bool { return dependencyKey(n) == dependencyKey(od) })
		if i < 0 {
			d.add(st, Safe, RuleFlagDependencyRemoved, w, "flag dependency removed", "")
			continue
		}
		nd := nc.FlagDependencies[i]
		var tight, loose []string
		grow := func(what string, o, n []string, growTightens bool) {
			for _, f := range n {
				if !slices.Contains(o, f) {
					if growTightens {
						tight = append(tight, what+" "+f)
					} else {
						loose = append(loose, what+" "+f)
					}
				}
			}
			for _, f := range o {
				if !slices.Contains(n, f) {
					if growTightens {
						loose = append(loose, "no longer "+what+" "+f)
					} else {
						tight = append(tight, "no longer "+what+" "+f)
					}
				}
			}
		}
		grow("requires", od.Requires, nd.Requires, true)
		grow("forbids", od.Forbids, nd.Forbids, true)
		// equals limits the values of `when` that trigger the rule; absent means any value.
		switch {
		case len(od.Equals) == 0 && len(nd.Equals) > 0:
			loose = append(loose, "applies only when "+od.When+" is "+values(nd.Equals))
		case len(od.Equals) > 0 && len(nd.Equals) == 0:
			tight = append(tight, "applies to any value of "+od.When)
		default:
			grow("applies when "+od.When+" is", stringsOf(od.Equals), stringsOf(nd.Equals), true)
		}
		if len(tight) > 0 {
			d.add(st, Breaking, RuleFlagDependencyTightened, w, strings.Join(tight, "; "), "")
		}
		if len(loose) > 0 {
			d.add(st, Safe, RuleFlagDependencyLoosened, w, strings.Join(loose, "; "), "")
		}
	}
	for _, nd := range nc.FlagDependencies {
		if !slices.ContainsFunc(oc.FlagDependencies, func(o flagDependency) bool { return dependencyKey(o) == dependencyKey(nd) }) {
			d.add(st, Breaking, RuleFlagDependencyAdded, dependencyWhere(oc.Name, nd), "flag dependency added", "")
		}
	}
}

func stringsOf(vs []any) []string {
	out := make([]string, 0, len(vs))
	for _, v := range vs {
		out = append(out, jsonText(v))
	}
	return out
}

func values(vs []any) string { return strings.Join(stringsOf(vs), " or ") }

// jsonText renders a JSON value as it would be written in the contract.
func jsonText(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return fmt.Sprint(v)
}
