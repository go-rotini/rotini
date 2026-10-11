package contractdiff

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
)

// effectKinds ranks the effect kinds, least risky first.
var effectKinds = map[string]int{"read": 0, "write": 1, "destructive": 2}

// isTrue reports whether an optional boolean is stated and true.
func isTrue(b *bool) bool { return b != nil && *b }

// isFalse reports whether an optional boolean is stated and false.
func isFalse(b *bool) bool { return b != nil && !*b }

// effects compares what a command, or giving a flag, does. Absent effects mean "assume the
// worst", so declaring them is safe and dropping them may break agents and permission rules
// that relied on them. A riskier fact is possibly breaking; a safer one is safe.
func (d *differ) effects(st stability, where string, o, n *effects) {
	switch {
	case o == nil && n == nil:
		return
	case o == nil:
		d.add(st, Safe, RuleEffectsAdded, where, "effects declared ("+n.Kind+")", "")
		return
	case n == nil:
		d.add(st, PossiblyBreaking, RuleEffectsRemoved, where, "effects no longer declared",
			"agents assume the worst, and generated permission rules that allowed it go")
		return
	}
	var raised, lowered []string
	switch or, nr := effectKinds[o.Kind], effectKinds[n.Kind]; {
	case nr > or:
		raised = append(raised, "kind "+o.Kind+" → "+n.Kind)
	case nr < or:
		lowered = append(lowered, "kind "+o.Kind+" → "+n.Kind)
	}
	switch oi, ni := isTrue(o.Idempotent), isTrue(n.Idempotent); {
	case oi && !ni:
		raised = append(raised, "no longer idempotent")
	case !oi && ni:
		lowered = append(lowered, "now idempotent")
	}
	switch ol, nl := isFalse(o.OpenWorld), isFalse(n.OpenWorld); {
	case ol && !nl:
		raised = append(raised, "may now reach outside the machine")
	case !ol && nl:
		lowered = append(lowered, "now local only")
	}
	if len(raised) > 0 {
		d.add(st, PossiblyBreaking, RuleEffectsRaised, where, strings.Join(raised, "; "),
			"agents and generated permission rules treat it as riskier")
	}
	if len(lowered) > 0 {
		d.add(st, Safe, RuleEffectsLowered, where, strings.Join(lowered, "; "), "")
	}
}

// agent compares whether a command or input is offered to AI agents. `false` keeps it, and a
// command's subtree, out of the agent outputs; `true` brings back an item they leave out by
// default, such as a hidden or secret one.
func (d *differ) agent(st stability, where string, o, n *bool) {
	switch {
	case isFalse(o) == isFalse(n) && isTrue(o) == isTrue(n):
	case isFalse(n):
		d.add(st, PossiblyBreaking, RuleAgentRemoved, where, "now kept from AI agents", "its tool or parameter leaves the agent outputs")
	case isTrue(o):
		d.add(st, PossiblyBreaking, RuleAgentRemoved, where, "no longer offered to AI agents regardless of the defaults",
			"a hidden, deprecated or secret item leaves the agent outputs")
	case isFalse(o):
		d.add(st, Safe, RuleAgentAdded, where, "no longer kept from AI agents", "")
	default:
		d.add(st, Safe, RuleAgentAdded, where, "now offered to AI agents", "")
	}
}

// roleValue compares the value a machine-output string flag takes to select JSON.
func (d *differ) roleValue(st stability, where, o, n string) {
	switch {
	case o == n:
	case o == "":
		d.add(st, Safe, RuleFlagRoleValueChanged, where, "selects JSON with "+strconv.Quote(n), "")
	case n == "":
		d.add(st, PossiblyBreaking, RuleFlagRoleValueChanged, where, "role_value "+strconv.Quote(o)+" removed",
			"programs that ask for JSON with it lose the switch")
	default:
		d.add(st, PossiblyBreaking, RuleFlagRoleValueChanged, where, fmt.Sprintf("role_value %q → %q", o, n),
			"programs that ask for JSON pass the new value")
	}
}

// profiles compares how a configuration file's profiles are chosen. where names the file.
func (d *differ) profiles(st stability, oc, nc *command, where string, o, n *profiles) {
	switch {
	case o == nil && n == nil:
		return
	case o == nil:
		if n.Default != "" {
			d.add(st, PossiblyBreaking, RuleProfilesAdded, where, fmt.Sprintf("profiles under %q added", n.Under),
				fmt.Sprintf("a run that selects none now reads profile %q", n.Default))
			return
		}
		d.add(st, Safe, RuleProfilesAdded, where, fmt.Sprintf("profiles under %q added", n.Under), "")
		return
	case n == nil:
		d.add(st, Breaking, RuleProfilesNoDelete, where, fmt.Sprintf("profiles under %q no longer read", o.Under),
			"users' profile sections are ignored")
		return
	}
	if o.Under != n.Under {
		d.add(st, Breaking, RuleProfilesUnderChanged, where, fmt.Sprintf("profiles are read under %q, was %q", n.Under, o.Under),
			"users' files stop matching")
	}
	d.profileFlag(st, oc, nc, where, o.Select.Flag, n.Select.Flag)
	for _, v := range missing(o.Select.Env, n.Select.Env) {
		d.add(st, Breaking, RuleProfilesEnvNoDelete, where+" $"+v, "$"+v+" no longer selects a profile", "")
	}
	for _, v := range missing(n.Select.Env, o.Select.Env) {
		d.add(st, Safe, RuleProfilesEnvAdded, where+" $"+v, "$"+v+" selects a profile", "")
	}
	if o.Default != n.Default {
		d.add(st, PossiblyBreaking, RuleProfilesDefaultChanged, where, fmt.Sprintf("default profile %q → %q", o.Default, n.Default),
			"a run that selects none reads a different profile")
	}
}

// profileFlag compares a profile selector flag by what users type: a new logical name with the
// same identifiers is no change here, and a selector flag the command no longer accepts at all
// is reported once, by the flag rules.
func (d *differ) profileFlag(st stability, oc, nc *command, where, o, n string) {
	switch o {
	case n:
		return
	case "":
		d.add(st, Safe, RuleProfilesFlagAdded, where, "flag "+n+" selects a profile", "")
		return
	}
	oldIDs := flagIDs(oc, o)
	newIDs := flagIDs(nc, n)
	if len(oldIDs) > 0 && slices.ContainsFunc(oldIDs, func(id string) bool { return slices.Contains(newIDs, id) }) {
		return
	}
	if len(oldIDs) > 0 && !slices.ContainsFunc(oldIDs, func(id string) bool { return acceptsID(nc, id) }) {
		return // the flag itself is gone; the flag rules report it
	}
	msg := "flag " + o + " no longer selects a profile"
	if n != "" {
		msg = "the profile selector flag " + o + " → " + n
	}
	d.add(st, Breaking, RuleProfilesFlagNoDelete, where, msg, "")
}

// flagIDs lists the identifiers of command c's flag named name, inherited ones included.
func flagIDs(c *command, name string) []string {
	for i := range c.Flags {
		if c.Flags[i].Name == name {
			return accepted(&c.Flags[i])
		}
	}
	return nil
}

// acceptsID reports whether command c accepts flag identifier id.
func acceptsID(c *command, id string) bool {
	for i := range c.Flags {
		if slices.Contains(accepted(&c.Flags[i]), id) {
			return true
		}
	}
	return false
}
