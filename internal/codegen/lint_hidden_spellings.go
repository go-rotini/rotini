package codegen

import (
	"fmt"
	"slices"
)

// Hidden spellings (`hidden_aliases` on commands, `hidden_identifiers` on flags) dispatch and
// parse like the listed ones, so the collision rules check them with the rest; this rule adds
// what is particular to them.

// dispatchTokens is every word that dispatches to c: its name, aliases and hidden aliases. A
// hidden alias repeating the name or an alias is left out here; lintHiddenSpellings reports it.
func dispatchTokens(c *Command) []string {
	tokens := append([]string{c.Name}, c.Aliases...)
	for _, h := range c.HiddenAliases {
		if !slices.Contains(tokens, h) {
			tokens = append(tokens, h)
		}
	}
	return tokens
}

// matchIdentifiers is every identifier the command line accepts for f: its listed ones, then
// its hidden ones that aren't also listed (lintHiddenSpellings reports those).
func matchIdentifiers(f FlagInput) []string {
	ids := slices.Clone(flagIdentifiers(f))
	for _, h := range f.HiddenIdentifiers {
		if !slices.Contains(ids, h) {
			ids = append(ids, h)
		}
	}
	return ids
}

// lintHiddenSpellings rejects a hidden alias that repeats the command's name or one of its
// aliases, and a hidden identifier that repeats one of the flag's identifiers or its negated
// form: a spelling can't be both listed and unlisted.
func lintHiddenSpellings(spec *Spec) []error {
	var problems []error
	walkCommandsAt(spec, func(c *Command, path, ptr string) {
		for _, h := range c.HiddenAliases {
			if h == c.Name || slices.Contains(c.Aliases, h) {
				problems = append(problems, &problem{kind: "spec", ptr: ptr + "/hidden_aliases", loc: "command " + path,
					msg: fmt.Sprintf("`hidden_aliases` entry %q is already the command's name or one of its `aliases`; a name is either listed or hidden, so remove it from one of the two", h)})
			}
		}
		for i, f := range c.Flags {
			listed := slices.Concat(flagIdentifiers(f), negatedForms(f))
			for _, h := range f.HiddenIdentifiers {
				if slices.Contains(listed, h) {
					problems = append(problems, inputProblem(fmt.Sprintf("%s/flags/%d", ptr, i), path, "flag", f.Name,
						fmt.Sprintf("`hidden_identifiers` entry %q is already one of its listed spellings; an identifier is either listed or hidden, so remove it from one of the two", h)))
				}
			}
		}
	})
	return problems
}
