package codegen

import "encoding/json"

// Flag sets (the root's `flag_sets`, added to a command with `use:`) are expanded when a spec
// is decoded: each using command gets copies of the set's flags after its own, with the set's
// flag groups and dependencies, so every later stage (lint, literals, pages, the contract)
// sees ordinary flags. Expansion only appends, so a command's own flags are always its first
// ones, and flagOrigins recovers which set each later flag came from.

// expandFlagSets appends each used set's flags, flag groups and flag dependencies to every
// command that uses it, in `use` order. A member with no group of its own takes the set's
// group. A name that names no set is left for lint to report; a `$ref` node is never expanded.
func expandFlagSets(s *Spec) {
	sets := s.Command.FlagSets
	if len(sets) == 0 {
		return
	}
	walkCommands(s, func(c *Command, _ string) {
		if c.Ref != "" {
			return
		}
		for _, name := range c.Use {
			set, ok := sets[name]
			if !ok {
				continue
			}
			for _, f := range set.Flags {
				cp := deepCopy(f)
				if cp.Group == "" {
					cp.Group = set.Group
				}
				c.Flags = append(c.Flags, cp)
			}
			for _, g := range set.FlagGroups {
				c.FlagGroups = append(c.FlagGroups, deepCopy(g))
			}
			for _, d := range set.FlagDependencies {
				c.FlagDependencies = append(c.FlagDependencies, deepCopy(d))
			}
		}
	})
}

// deepCopy returns a copy of v sharing no pointer, slice or map with it. Normalization edits
// schemas in place and isn't idempotent, so each using command needs its own copy.
func deepCopy[T any](v T) T {
	data, err := json.Marshal(v)
	if err != nil {
		return v
	}
	var out T
	if err := json.Unmarshal(data, &out); err != nil {
		return v
	}
	return out
}

// flagOrigin is where one of a command's flags was declared: set is "" for the command's own
// flags, else the set's name, with index the flag's position in that set's `flags`.
type flagOrigin struct {
	set   string
	index int
}

// flagOrigins maps each of c's (expanded) flags to where it was declared, given the spec's
// sets. Own flags come first; each used set's flags follow in `use` order.
func flagOrigins(c *Command, sets map[string]FlagSet) []flagOrigin {
	out := make([]flagOrigin, len(c.Flags))
	i := ownFlagCount(c, sets)
	for _, name := range c.Use {
		set, ok := sets[name]
		if !ok {
			continue
		}
		for j := range set.Flags {
			if i < len(out) {
				out[i] = flagOrigin{set: name, index: j}
			}
			i++
		}
	}
	return out
}

// ownFlagCount is how many of c's flags it declares itself, before any set's.
func ownFlagCount(c *Command, sets map[string]FlagSet) int {
	return max(0, len(c.Flags)-setsLen(c, sets, func(s FlagSet) int { return len(s.Flags) }))
}

// ownGroupCount and ownDependencyCount are ownFlagCount for flag groups and dependencies.
func ownGroupCount(c *Command, sets map[string]FlagSet) int {
	return max(0, len(c.FlagGroups)-setsLen(c, sets, func(s FlagSet) int { return len(s.FlagGroups) }))
}

func ownDependencyCount(c *Command, sets map[string]FlagSet) int {
	return max(0, len(c.FlagDependencies)-setsLen(c, sets, func(s FlagSet) int { return len(s.FlagDependencies) }))
}

// setsLen sums n over the declared sets c uses. A $ref node is never expanded.
func setsLen(c *Command, sets map[string]FlagSet, n func(FlagSet) int) int {
	if c.Ref != "" {
		return 0
	}
	total := 0
	for _, name := range c.Use {
		if set, ok := sets[name]; ok {
			total += n(set)
		}
	}
	return total
}
