package codegen

import (
	"fmt"
	"slices"
	"strings"
)

// This file holds the facts the agent-facing outputs share: effects (what running a command
// does), flag roles and the `agent` opt-in and opt-out. Help, man and markdown show effects;
// the contract carries all three; the tool exports, the skill page and the permission snippets
// read them from the contract.

// effectRank orders effect kinds from least to most impactful.
var effectRank = map[string]int{"read": 0, "write": 1, "destructive": 2}

// effectsLiteral renders a spec `effects` as a rotini.Effects composite literal.
func effectsLiteral(e *Effects) string {
	var b strings.Builder
	fmt.Fprintf(&b, "&%s.Effects{Kind: %q", rotiniPkgName, e.Kind)
	if e.Idempotent != nil {
		fmt.Fprintf(&b, ", Idempotent: new(%t)", *e.Idempotent)
	}
	if e.OpenWorld != nil {
		fmt.Fprintf(&b, ", OpenWorld: new(%t)", *e.OpenWorld)
	}
	b.WriteString("}")
	return b.String()
}

// effectsField renders a command's `Effects: …,` line for the Definition, or "" when it
// declares none.
func effectsField(e *Effects) string {
	if e == nil {
		return ""
	}
	return "Effects: " + effectsLiteral(e) + ",\n"
}

// flagEffectsField renders a flag literal's `, Effects: …` field, or "" when it declares none.
func flagEffectsField(e *Effects) string {
	if e == nil {
		return ""
	}
	return ", Effects: " + effectsLiteral(e)
}

// effectsText describes effects for people, stating only the facts the spec states:
// "destructive, not idempotent, open world". "" when e is nil.
func effectsText(e *Effects) string {
	if e == nil {
		return ""
	}
	parts := []string{e.Kind}
	if e.Idempotent != nil {
		parts = append(parts, map[bool]string{true: "idempotent", false: "not idempotent"}[*e.Idempotent])
	}
	if e.OpenWorld != nil {
		parts = append(parts, map[bool]string{true: "open world", false: "local only"}[*e.OpenWorld])
	}
	return strings.Join(parts, ", ")
}

// worstEffects combines a command's effects with every flag effect that can apply to it, for
// consumers that can't see the actual command line (tool annotations, permission rules, the
// skill page): the highest kind, idempotent only when every stated value is, open world when
// any is. A flag's unstated idempotent or open world follows the command's. nil when the
// command declares none.
func worstEffects(cmd *Effects, flags []*Effects) *Effects {
	if cmd == nil {
		return nil
	}
	out := *cmd
	for _, f := range flags {
		if f == nil {
			continue
		}
		if effectRank[f.Kind] > effectRank[out.Kind] {
			out.Kind = f.Kind
		}
		if f.Idempotent != nil && !*f.Idempotent {
			out.Idempotent = new(false)
		}
		if f.OpenWorld != nil && *f.OpenWorld {
			out.OpenWorld = new(true)
		}
	}
	return &out
}

// agentRoles are the flag roles a program driving the CLI reads; each is linted to appear once
// per command, counting inherited cascading flags.
var agentRoles = []string{"dry-run", "confirm", "machine-output", "page"}

// isAgentRole reports whether role is one of agentRoles.
func isAgentRole(role string) bool { return slices.Contains(agentRoles, role) }
