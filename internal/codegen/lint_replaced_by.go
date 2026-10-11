package codegen

import (
	"fmt"
	"slices"
	"strings"
)

// lintReplacedBy checks `replaced_by` on commands and flags: it sits beside `deprecated`, names
// something that exists, and doesn't name the item itself. A replacement that is itself
// deprecated is a warning. A command path runs from the spec's root through names and
// aliases; one that enters a `$ref` child can't be followed here and is accepted, since the
// child is checked as its own document.
func lintReplacedBy(spec *Spec) []error {
	var problems []error
	walkChainsAt(spec, func(chain []*Command, path, ptr string) {
		c := chain[len(chain)-1]
		if r := c.ReplacedBy; r != "" {
			add := func(msg string, warn bool) {
				p := &problem{kind: "spec", ptr: ptr + "/replaced_by", loc: "command " + path, msg: msg}
				if warn {
					p.sev = severityWarning
				}
				problems = append(problems, p)
			}
			switch target, found, opaque := commandAt(&spec.Command, strings.Fields(r)); {
			case c.Deprecated == "":
				add(fmt.Sprintf("sets `replaced_by: %s` without `deprecated`; it names what to use instead of a deprecated command, so say why with `deprecated`", r), false)
			case opaque:
			case !found:
				add(fmt.Sprintf("sets `replaced_by: %s`, which names no command; write the path below the root command, without the program's name (`purge`, `remote add`)", r), false)
			case target == c:
				add(fmt.Sprintf("sets `replaced_by: %s`, which is the command itself", r), false)
			case target.Deprecated != "":
				add(fmt.Sprintf("sets `replaced_by: %s`, which is deprecated too; name the command to move to", r), true)
			}
		}
		for i, f := range c.Flags {
			if f.ReplacedBy == "" {
				continue
			}
			add := func(msg string, warn bool) {
				p := inputProblem(fmt.Sprintf("%s/flags/%d", ptr, i), path, "flag", f.Name, msg)
				if warn {
					p.sev = severityWarning
				}
				problems = append(problems, p)
			}
			target, found := flagInScope(chain, f.ReplacedBy)
			switch {
			case f.Deprecated == "":
				add(fmt.Sprintf("sets `replaced_by: %s` without `deprecated`; it names what to use instead of a deprecated flag, so say why with `deprecated`", f.ReplacedBy), false)
			case !found:
				add(fmt.Sprintf("sets `replaced_by: %s`, which is no identifier of this command's flags or of an ancestor's cascading ones", f.ReplacedBy), false)
			case target.Name == f.Name:
				add(fmt.Sprintf("sets `replaced_by: %s`, which is the flag itself", f.ReplacedBy), false)
			case target.Deprecated != "":
				add(fmt.Sprintf("sets `replaced_by: %s`, which is deprecated too; name the flag to move to", f.ReplacedBy), true)
			}
		}
	})
	return problems
}

// commandAt follows words from root through sub-command names and aliases. opaque reports a
// path that enters a `$ref` child, which can't be followed in this document.
func commandAt(root *Command, words []string) (target *Command, found, opaque bool) {
	cur := root
	for _, w := range words {
		var next *Command
		for i := range cur.Commands {
			child := &cur.Commands[i]
			if child.Ref != "" && child.Name == "" {
				continue
			}
			if child.Name == w || slices.Contains(child.Aliases, w) || slices.Contains(child.HiddenAliases, w) {
				next = child
				break
			}
		}
		if next == nil {
			for i := range cur.Commands {
				if cur.Commands[i].Ref != "" {
					return nil, false, true // the word may name a command inside a composed child
				}
			}
			return nil, false, false
		}
		if next.Ref != "" && w != words[len(words)-1] {
			return nil, false, true
		}
		cur = next
	}
	return cur, len(words) > 0, false
}

// flagInScope finds the flag an identifier names on the leaf of chain: one of its own flags,
// else a cascading flag of an ancestor, nearest first.
func flagInScope(chain []*Command, id string) (FlagInput, bool) {
	for i, c := range slices.Backward(chain) {
		for _, f := range c.Flags {
			if i < len(chain)-1 && !f.Cascading {
				continue
			}
			if slices.Contains(flagIdentifiers(f), id) {
				return f, true
			}
		}
	}
	return FlagInput{}, false
}
