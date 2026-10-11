package rotini

import (
	"slices"
	"strings"
)

// Hidden spellings ([CommandDef.HiddenAliases], [FlagDef.HiddenIdentifiers]) are matched
// wherever the command line is read and listed nowhere: the listing sites keep reading Aliases
// and Identifiers.

// matches reports whether tok invokes c: its name, an alias or a hidden alias.
func (c CommandDef) matches(tok string) bool {
	return c.Name == tok || slices.Contains(c.Aliases, tok) || slices.Contains(c.HiddenAliases, tok)
}

// hasIdentifier reports whether id is one of f's identifiers, hidden ones included.
func (f FlagDef) hasIdentifier(id string) bool {
	return slices.Contains(f.Identifiers, id) || slices.Contains(f.HiddenIdentifiers, id)
}

// negatedForms is every negated spelling the command line accepts for f: the listed ones
// ([negatedIdentifiers]) and the "--no-<x>" form of each hidden long identifier.
func negatedForms(f FlagDef) []string {
	forms := negatedIdentifiers(f)
	if !f.Negatable || f.Negation != "" {
		return forms
	}
	for _, id := range f.HiddenIdentifiers {
		if name, ok := strings.CutPrefix(id, "--"); ok {
			forms = append(forms, "--no-"+name)
		}
	}
	return forms
}

// matchSpellings is every spelling the command line accepts for f, hidden ones included.
func matchSpellings(f FlagDef) []string {
	ids := slices.Concat(f.Identifiers, f.HiddenIdentifiers)
	return append(ids, negatedForms(f)...)
}
