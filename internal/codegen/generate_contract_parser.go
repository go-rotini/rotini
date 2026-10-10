package codegen

import "slices"

// contractParserFields fills the contract facts that flag sets, hidden spellings and
// replacements add to a command and its flags. c.Flags holds the command's own flags, then
// the ones it inherits, in the order of in.Flags and n.inherited.
func (p *program) contractParserFields(c *contractCommand, n contractNode, in *Inputs) {
	c.HiddenAliases = n.hiddenAliases
	c.ReplacedBy = n.replacedBy.path
	for i, f := range slices.Concat(in.Flags, n.inherited) {
		if i >= len(c.Flags) {
			break
		}
		cf := &c.Flags[i]
		cf.HiddenIdentifiers = f.HiddenIdentifiers
		cf.ReplacedBy = f.ReplacedBy
		if i < len(in.Flags) && i < len(n.flagSets) {
			cf.FlagSet = n.flagSets[i]
		}
	}
}
