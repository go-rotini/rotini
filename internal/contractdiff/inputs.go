package contractdiff

import (
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// Input kinds, for the facts only some of them carry.
const (
	kindFlag     = "flag"
	kindArgument = "argument"
	kindEnv      = "env"
	kindConfig   = "config"
)

// flagWhere names a flag by its first long identifier, else its first identifier.
func flagWhere(cmd string, f *input) string {
	for _, id := range f.Identifiers {
		if strings.HasPrefix(id, "--") {
			return cmd + " " + id
		}
	}
	if len(f.Identifiers) > 0 {
		return cmd + " " + f.Identifiers[0]
	}
	return cmd + " " + f.Name
}

// accepted lists every identifier a flag accepts on the command line.
func accepted(f *input) []string { return slices.Concat(f.Identifiers, f.HiddenIdentifiers) }

// digitIdentifier matches a flag identifier that is a dash and digits, such as -4, which an
// argument starting with a digit can no longer be.
var digitIdentifier = regexp.MustCompile(`^-\d+$`)

// flags compares the flags a command accepts, matched by identifier. An inherited flag is
// compared where it is declared, so a cascading flag's change is reported once. On the
// inheriting command it is compared only where an identifier moves between the command's own
// flag and an inherited one: the flag that takes the identifier now is compared with the one
// that took it before. A new digit identifier is also reported there, since it changes how the
// command's arguments parse.
func (d *differ) flags(st stability, oc, nc *command) {
	newAccepts, oldTakers := takers(nc), takers(oc)
	oldAccepts := map[string]bool{}
	for id := range oldTakers {
		oldAccepts[id] = true
	}
	matched := map[*input]bool{}
	for i := range oc.Flags {
		of := &oc.Flags[i]
		if of.Inherited {
			continue
		}
		fst := leastStable(st, of.Stability)
		w := flagWhere(oc.Name, of)
		nf := takerOf(of, newAccepts)
		if nf == nil {
			if of.ReplacedBy != "" && newAccepts[of.ReplacedBy] != nil {
				d.replaced(fst, RuleFlagReplaced, w, "flag removed; replaced by "+of.ReplacedBy, of.RemovedIn, of.Hidden, "")
				continue
			}
			d.removal(fst, RuleFlagNoDelete, w, "flag removed", of.RemovedIn, of.Hidden)
			continue
		}
		matched[nf] = true
		d.flag(fst, oc, of, nf, newAccepts, oldAccepts)
	}
	for i := range oc.Flags {
		of := &oc.Flags[i]
		if !of.Inherited {
			continue
		}
		for _, id := range accepted(of) {
			if nf := newAccepts[id]; oldTakers[id] == of && nf != nil && !nf.Inherited && !matched[nf] {
				matched[nf] = true
				d.flag(leastStable(st, of.Stability), oc, of, nf, newAccepts, oldAccepts)
				break
			}
		}
	}
	d.addedFlags(st, oc, nc, matched, oldAccepts)
}

// takers maps each identifier a command accepts to the flag that takes it: the command's own
// flag before an inherited one, which it shadows.
func takers(c *command) map[string]*input {
	m := map[string]*input{}
	for _, inherited := range []bool{false, true} {
		for i := range c.Flags {
			f := &c.Flags[i]
			if f.Inherited != inherited {
				continue
			}
			for _, id := range accepted(f) {
				if m[id] == nil {
					m[id] = f
				}
			}
		}
	}
	return m
}

// takerOf returns the new flag that takes one of of's identifiers: a flag of the command's own
// first, else an inherited one, else nil.
func takerOf(of *input, newAccepts map[string]*input) *input {
	var inherited *input
	for _, id := range accepted(of) {
		f := newAccepts[id]
		switch {
		case f == nil:
		case !f.Inherited:
			return f
		case inherited == nil:
			inherited = f
		}
	}
	return inherited
}

// addedFlags reports the flags the new contract adds, and a digit identifier any flag gains on a
// command with arguments.
func (d *differ) addedFlags(st stability, oc, nc *command, matched map[*input]bool, oldAccepts map[string]bool) {
	hasArgs := len(oc.Arguments) > 0
	for i := range nc.Flags {
		nf := &nc.Flags[i]
		if hasArgs {
			for _, id := range accepted(nf) {
				if digitIdentifier.MatchString(id) && !oldAccepts[id] {
					d.add(st, PossiblyBreaking, RuleDigitFlagAdded, oc.Name+" "+id,
						"flag "+id+" added to a command with arguments", "an argument such as "+id+" now reads as the flag")
				}
			}
		}
		if nf.Inherited || matched[nf] || slices.ContainsFunc(accepted(nf), func(id string) bool { return oldAccepts[id] }) {
			continue
		}
		w := flagWhere(oc.Name, nf)
		switch {
		case nf.Required && !nf.ShortCircuit:
			d.add(st, Breaking, RuleFlagRequiredAdded, w, "required flag added", "")
		case !nf.Hidden:
			d.add(st, Safe, RuleFlagAdded, w, "flag added", "")
		}
	}
}

// flag compares a flag present in both contracts.
func (d *differ) flag(st stability, oc *command, of, nf *input, newAccepts map[string]*input, oldAccepts map[string]bool) {
	d.flagIdentifiers(st, oc, of, nf, newAccepts, oldAccepts)
	w := flagWhere(oc.Name, of)
	if of.Name != nf.Name {
		d.add(st, PossiblyBreaking, RuleFlagNameChanged, w, fmt.Sprintf("name %q → %q", of.Name, nf.Name),
			"the command line is unchanged; the parameters key a program calls it with changes")
	}
	d.flipped(st, w, of.Required, nf.Required, flip{Breaking, RuleFlagRequiredAdded, "now required"}, flip{Safe, RuleFlagRequiredRemoved, "no longer required"})
	if !of.Inherited && !nf.Inherited {
		// Where one side is inherited, the ancestor's flag still reaches the sub-commands.
		d.flipped(st, w, of.Cascading, nf.Cascading, flip{Safe, RuleFlagCascades, "sub-commands now accept it"}, flip{Breaking, RuleFlagNoLongerCascades, "sub-commands no longer accept it"})
	}
	d.flipped(st, w, of.ShortCircuit, nf.ShortCircuit, flip{Safe, RuleFlagShortCircuitAdded, "now waives the command's requirements"}, flip{PossiblyBreaking, RuleFlagShortCircuitRemoved, "no longer waives the command's requirements"})
	d.flipped(st, w, forbidsRepeat(of), forbidsRepeat(nf), flip{Breaking, RuleFlagRepeatForbidden, "giving it twice is now an error"}, flip{Safe, RuleFlagRepeatAllowed, "may now be given more than once"})
	switch {
	case of.Role == nf.Role:
	case of.Role == "" && nf.Role != "chdir":
		d.add(st, Safe, RuleFlagRoleAdded, w, "role "+strconv.Quote(nf.Role)+" declared", "")
	default:
		note := "programs that drive the CLI read it"
		if nf.Role == "chdir" {
			note += "; its value now sets the directory relative paths and config discovery use"
		}
		d.add(st, PossiblyBreaking, RuleFlagRoleChanged, w, fmt.Sprintf("role %q → %q", of.Role, nf.Role), note)
	}
	d.roleValue(st, w, of.RoleValue, nf.RoleValue)
	d.effects(st, w, of.Effects, nf.Effects)
	if of.DottedKeys != nf.DottedKeys {
		d.add(st, Breaking, RuleInputDottedKeysChanged, w, onOff("a dotted key assigns into nested maps", nf.DottedKeys), "")
	}
	d.input(st, kindFlag, w, of, nf)
}

// forbidsRepeat reports whether giving a flag twice is an error.
func forbidsRepeat(f *input) bool { return f.Repeatable != nil && !*f.Repeatable }

// flagIdentifiers compares a flag's identifiers, hidden identifiers and negated forms.
func (d *differ) flagIdentifiers(st stability, oc *command, of, nf *input, newAccepts map[string]*input, oldAccepts map[string]bool) {
	for _, l := range []struct {
		old, new, other []string
		gone, added     string
		what            string
	}{
		{of.Identifiers, nf.Identifiers, nf.HiddenIdentifiers, RuleFlagIdentifierNoDelete, RuleFlagIdentifierAdded, "identifier"},
		{of.HiddenIdentifiers, nf.HiddenIdentifiers, nf.Identifiers, RuleFlagHiddenIdentifierNoDelete, RuleFlagHiddenIdentifierAdded, "hidden identifier"},
	} {
		for _, id := range l.old {
			switch {
			case slices.Contains(l.new, id):
			case slices.Contains(l.other, id):
				d.add(st, Safe, RuleFlagIdentifierMoved, oc.Name+" "+id, l.what+" "+id+" moved between identifiers and hidden_identifiers", "")
			case newAccepts[id] != nil:
				// Now another flag's identifier; the other flag is compared on its own.
			default:
				d.removal(st, l.gone, oc.Name+" "+id, l.what+" "+id+" removed", of.IdentifiersRemovedIn[id], false)
			}
		}
		for _, id := range l.new {
			if !oldAccepts[id] && (!digitIdentifier.MatchString(id) || len(oc.Arguments) == 0) {
				d.add(st, Safe, l.added, oc.Name+" "+id, l.what+" "+id+" added", "")
			}
		}
	}
	for _, neg := range of.Negated {
		if !slices.Contains(nf.Negated, neg) {
			d.add(st, Breaking, RuleFlagNegatedNoDelete, oc.Name+" "+neg, "negated form "+neg+" removed", "")
		}
	}
	for _, neg := range nf.Negated {
		if !slices.Contains(of.Negated, neg) {
			d.add(st, Safe, RuleFlagNegatedAdded, oc.Name+" "+neg, "negated form "+neg+" added", "")
		}
	}
}

// arguments compares a command's arguments, matched by name, else by position for one renamed
// in place. An argument now at another position binds a different word.
func (d *differ) arguments(st stability, oc, nc *command) {
	named := func(list []input, name string) int {
		return slices.IndexFunc(list, func(a input) bool { return a.Name == name })
	}
	matched := map[int]bool{}
	for i := range oc.Arguments {
		oa := &oc.Arguments[i]
		ast := leastStable(st, oa.Stability)
		w := oc.Name + " <" + oa.Name + ">"
		j := named(nc.Arguments, oa.Name)
		if j < 0 && i < len(nc.Arguments) && named(oc.Arguments, nc.Arguments[i].Name) < 0 {
			j = i
		}
		if j < 0 {
			d.removal(ast, RuleArgumentNoDelete, w, "argument removed", oa.RemovedIn, oa.Hidden)
			continue
		}
		matched[j] = true
		na := &nc.Arguments[j]
		if j != i {
			d.add(ast, Breaking, RuleArgumentMoved, w, fmt.Sprintf("position %d → %d", i+1, j+1), "the word given at its old position binds to another argument")
		}
		if oa.Name != na.Name {
			d.add(ast, PossiblyBreaking, RuleArgumentNameChanged, w, "renamed "+strconv.Quote(na.Name), "the parameters key a program calls it with changes")
		}
		d.flipped(ast, w, oa.Required, na.Required, flip{Breaking, RuleArgumentRequiredAdded, "now required"}, flip{Safe, RuleArgumentRequiredRemoved, "no longer required"})
		d.flipped(ast, w, oa.Variadic, na.Variadic, flip{PossiblyBreaking, RuleArgumentVariadicAdded, "now takes several values; later positionals bind differently, and its parameter becomes a list"}, flip{Breaking, RuleArgumentVariadicRemoved, "no longer takes several values"})
		if oa.Passthrough != na.Passthrough {
			d.add(ast, Breaking, RuleArgumentPassthroughChanged, w, onOff("raw words start at this argument", na.Passthrough), "")
		}
		if oa.Glob != na.Glob {
			d.add(ast, PossiblyBreaking, RuleArgumentGlobChanged, w, onOff("patterns in its words are expanded on Windows", na.Glob), "")
		}
		d.input(ast, kindArgument, w, oa, na)
	}
	for j := range nc.Arguments {
		if matched[j] {
			continue
		}
		na := &nc.Arguments[j]
		w := oc.Name + " <" + na.Name + ">"
		switch {
		case na.Required:
			d.add(st, Breaking, RuleArgumentRequiredAdded, w, "required argument added", "")
		case na.Hidden:
		case j >= len(oc.Arguments):
			d.add(st, Safe, RuleArgumentAdded, w, "optional argument added at the end", "")
		default:
			d.add(st, Safe, RuleArgumentAdded, w, "optional argument added", "")
		}
	}
}

// envs compares a command's environment variables, matched by variable.
func (d *differ) envs(st stability, oc, nc *command) {
	matched := map[int]bool{}
	for i := range oc.Env {
		oe := &oc.Env[i]
		est := leastStable(st, oe.Stability)
		w := oc.Name + " $" + first(oe.Variables, oe.Name)
		j := slices.IndexFunc(nc.Env, func(ne input) bool {
			return slices.ContainsFunc(oe.Variables, func(v string) bool { return slices.Contains(ne.Variables, v) })
		})
		if j < 0 {
			d.removal(est, RuleEnvNoDelete, w, "environment variable no longer read", oe.RemovedIn, oe.Hidden)
			continue
		}
		matched[j] = true
		ne := &nc.Env[j]
		for _, v := range oe.Variables {
			if !slices.Contains(ne.Variables, v) {
				d.add(est, Breaking, RuleEnvVariableNoDelete, oc.Name+" $"+v, "$"+v+" no longer read", "")
			}
		}
		for _, v := range ne.Variables {
			if !slices.Contains(oe.Variables, v) {
				d.add(est, Safe, RuleEnvVariableAdded, oc.Name+" $"+v, "$"+v+" also read", "")
			}
		}
		d.flipped(est, w, oe.Required, ne.Required, flip{Breaking, RuleEnvRequiredAdded, "now required"}, flip{Safe, RuleEnvRequiredRemoved, "no longer required"})
		if oe.Nesting != ne.Nesting {
			d.add(est, Breaking, RuleEnvNestingChanged, w, fmt.Sprintf("nesting separator %q → %q", oe.Nesting, ne.Nesting), "")
		}
		d.input(est, kindEnv, w, oe, ne)
	}
	for j := range nc.Env {
		ne := &nc.Env[j]
		if matched[j] {
			continue
		}
		w := oc.Name + " $" + first(ne.Variables, ne.Name)
		switch {
		case ne.Required:
			d.add(st, Breaking, RuleEnvRequiredAdded, w, "required environment variable added", "")
		case !ne.Hidden:
			d.add(st, Safe, RuleEnvAdded, w, "environment variable added", "")
		}
	}
}

// configs compares a command's config entries, matched by key.
func (d *differ) configs(st stability, oc, nc *command) {
	matched := map[int]bool{}
	for i := range oc.Config {
		ok := &oc.Config[i]
		kst := leastStable(st, ok.Stability)
		w := oc.Name + " config " + ok.Key
		j := slices.IndexFunc(nc.Config, func(n input) bool { return n.Key == ok.Key })
		if j < 0 {
			d.removal(kst, RuleConfigNoDelete, w, "config key no longer read", ok.RemovedIn, ok.Hidden)
			continue
		}
		matched[j] = true
		nk := &nc.Config[j]
		if ok.File != nk.File {
			from, to := d.old.configFileOf(oc.Path, ok.File), d.new.configFileOf(nc.Path, nk.File)
			if from == "" || from != to {
				d.add(kst, Breaking, RuleConfigMoved, w, fmt.Sprintf("read from file %q, was %q", nk.File, ok.File), "config moved")
			}
		}
		d.flipped(kst, w, ok.Required, nk.Required, flip{Breaking, RuleConfigRequiredAdded, "now required"}, flip{Safe, RuleConfigRequiredRemoved, "no longer required"})
		d.input(kst, kindConfig, w, ok, nk)
	}
	for j := range nc.Config {
		nk := &nc.Config[j]
		if matched[j] {
			continue
		}
		w := oc.Name + " config " + nk.Key
		switch {
		case nk.Required:
			d.add(st, Breaking, RuleConfigRequiredAdded, w, "required config key added", "")
		case !nk.Hidden:
			d.add(st, Safe, RuleConfigAdded, w, "config key added", "")
		}
	}
}

// configFileOf resolves the configuration file name a command's config entry names, through
// the config_files of the command and its ancestors, to where the file is read from.
func (doc *document) configFileOf(path []string, name string) string {
	for i := len(path); i >= 0; i-- {
		c := doc.byPath[pathKey(path[:i])]
		if c == nil {
			continue
		}
		for _, f := range c.ConfigFiles {
			if f.Name == name {
				return location(f) + " as " + f.As
			}
		}
	}
	return ""
}

func first(list []string, fallback string) string {
	if len(list) > 0 {
		return list[0]
	}
	return fallback
}

// input compares the facts every input kind shares, then its schema.
func (d *differ) input(st stability, kind, w string, o, n *input) {
	d.lifecycle(st, w, inputLifecycle(o), inputLifecycle(n))
	d.hidden(st, RuleInputHidden, RuleInputUnhidden, w, o.Hidden, n.Hidden)
	d.agent(st, w, o.Agent, n.Agent)
	d.metadata(st, w, o.Metadata, n.Metadata)
	if o.Secret != n.Secret {
		d.add(st, Safe, RuleInputSecretChanged, w, onOff("the value is a secret", n.Secret), "")
	}
	d.inputType(st, w, o, n)
	if o.Kind != "" && n.Kind != "" && o.Kind != n.Kind {
		d.add(st, Breaking, RuleInputKindChanged, w, fmt.Sprintf("kind %s → %s", o.Kind, n.Kind), "it is supplied differently")
	}
	os, ns := o.Separator, n.Separator
	if kind == kindEnv {
		os, ns = orComma(os), orComma(ns)
	}
	if os != ns {
		d.add(st, Breaking, RuleInputSeparatorChanged, w, fmt.Sprintf("separator %q → %q", os, ns), "values split differently")
	}
	d.from(st, w, o, n)
	d.implicitValue(st, w, o, n)
	d.flipped(st, w, o.IgnoreCase, n.IgnoreCase, flip{Safe, RuleInputIgnoreCaseAdded, "enum values now match whatever their case"}, flip{Breaking, RuleInputIgnoreCaseRemoved, "enum values now match case"})
	d.flipped(st, w, o.Base != "", n.Base != "", flip{Safe, RuleInputBaseAdded, "0x, 0o and 0b integer literals now accepted"}, flip{Breaking, RuleInputBaseRemoved, "0x, 0o and 0b integer literals no longer accepted"})
	d.layouts(st, w, o.Layouts, n.Layouts)
	switch o.Relative {
	case n.Relative:
	case "":
		d.add(st, Safe, RuleInputRelativeAdded, w, "relative values ("+n.Relative+") accepted", "")
	default:
		d.add(st, Breaking, RuleInputRelativeChanged, w, fmt.Sprintf("relative values %q → %q", o.Relative, n.Relative), "")
	}
	if added := missing(n.Expand, o.Expand); len(added) > 0 {
		d.add(st, PossiblyBreaking, RuleInputExpandAdded, w, "now expands "+strings.Join(added, ", "), "a literal value that looks like one changes meaning")
	}
	if removed := missing(o.Expand, n.Expand); len(removed) > 0 {
		d.add(st, Breaking, RuleInputExpandRemoved, w, "no longer expands "+strings.Join(removed, ", "), "")
	}
	if o.RelativeTo != n.RelativeTo {
		d.add(st, Breaking, RuleInputRelativeToChanged, w, fmt.Sprintf("relative paths resolve against %q, was %q", n.RelativeTo, o.RelativeTo), "")
	}
	d.inputSources(st, kind, w, o, n)
	meta := d.valuesFrom(st, w, o, n)
	if isEmptySchema(o.Schema) && hasType(n.Schema) {
		d.add(st, Safe, RuleInputTypeNowDescribed, w, "the schema now describes the value's type", "")
		return
	}
	meta.oldEnum, meta.newEnum = o.EnumValues, n.EnumValues
	meta.typed = o.Type != "" && n.Type != ""
	meta.skipFormat = len(o.Layouts)+len(n.Layouts) > 0 || o.Relative != "" || n.Relative != ""
	meta.skipPattern = o.Base != "" || n.Base != ""
	d.walk(st, in, w, o.Schema, n.Schema, meta)
}

func orComma(s string) string {
	if s == "" {
		return ","
	}
	return s
}

// missing lists the entries of a that b lacks.
func missing(a, b []string) []string {
	var out []string
	for _, s := range a {
		if !slices.Contains(b, s) {
			out = append(out, s)
		}
	}
	return out
}

// inputSources compares where else an input's value comes from: environment variables, a
// variable naming a file, a config key, and a config source.
func (d *differ) inputSources(st stability, kind, w string, o, n *input) {
	if kind == kindFlag || kind == kindArgument {
		for _, v := range missing(o.Env, n.Env) {
			d.add(st, Breaking, RuleInputEnvNoDelete, w+" $"+v, "fallback $"+v+" no longer read", "")
		}
		for _, v := range missing(n.Env, o.Env) {
			d.add(st, Safe, RuleInputEnvAdded, w+" $"+v, "fallback $"+v+" read", "")
		}
		switch o.ConfigKey {
		case n.ConfigKey:
		case "":
			d.add(st, Safe, RuleInputConfigKeyAdded, w, "fallback config key "+n.ConfigKey+" read", "")
		default:
			d.add(st, Breaking, RuleInputConfigKeyChanged, w, fmt.Sprintf("fallback config key %q → %q", o.ConfigKey, n.ConfigKey), "")
		}
	}
	switch o.VariableFile {
	case n.VariableFile:
	case "":
		d.add(st, Safe, RuleInputVariableFileAdded, w, "$"+n.VariableFile+" may name a file holding the value", "")
	default:
		d.add(st, Breaking, RuleInputVariableFileNoDelete, w, "$"+o.VariableFile+" no longer names a file holding the value", "")
	}
	if o.ConfigSource != n.ConfigSource && o.ConfigSource != "" {
		d.add(st, Breaking, RuleInputConfigSourceChanged, w, fmt.Sprintf("config source %q → %q", o.ConfigSource, n.ConfigSource), "")
	}
}

// from compares where a typed value may come from besides the text itself. On a secret input,
// adding `value` accepts literals again, which is safe; on any other input `value` changes
// nothing.
func (d *differ) from(st stability, w string, o, n *input) {
	for _, v := range missing(o.From, n.From) {
		if v == "value" && !o.Secret {
			continue
		}
		msg := "a value no longer comes from " + v
		if v == "value" {
			msg = "a secret typed on the command line is now refused"
		}
		d.add(st, Breaking, RuleInputFromNoDelete, w, msg, "")
	}
	for _, v := range missing(n.From, o.From) {
		switch {
		case v == "value" && n.Secret:
			d.add(st, Safe, RuleInputFromAdded, w, "a secret typed on the command line is now accepted", "")
		case v == "value":
		default:
			d.add(st, PossiblyBreaking, RuleInputFromAdded, w, "a value may now come from "+v, fromNote(v))
		}
	}
}

func fromNote(v string) string {
	switch v {
	case "file":
		return "a value starting with @ now reads a file"
	case "stdin":
		return "a value of - now reads stdin"
	}
	return ""
}

// layouts compares the time layouts an input accepts. Help and ArgvOf write the first.
func (d *differ) layouts(st stability, w string, o, n []string) {
	removed := missing(o, n)
	if len(removed) > 0 {
		d.add(st, Breaking, RuleInputLayoutNoDelete, w, "layouts no longer accepted: "+strings.Join(removed, ", "), "")
	}
	if added := missing(n, o); len(added) > 0 {
		d.add(st, Safe, RuleInputLayoutAdded, w, "layouts accepted: "+strings.Join(added, ", "), "")
	}
	if len(o) > 0 && len(n) > 0 && o[0] != n[0] && slices.Contains(n, o[0]) {
		d.add(st, PossiblyBreaking, RuleInputLayoutFirstChanged, w, "first layout "+o[0]+" → "+n[0], "help and ArgvOf write the first layout")
	}
}

// valuesFrom compares the output path an input's enum follows, and returns how the schema walk
// treats its enum.
func (d *differ) valuesFrom(st stability, w string, o, n *input) *enumMeta {
	meta := &enumMeta{}
	switch {
	case o.ValuesFrom == n.ValuesFrom:
		if n.ValuesFrom != "" {
			meta.note = "follows " + n.ValuesFrom
		}
	case o.ValuesFrom == "":
		if !hasEnum(o.Schema, d.old) {
			d.add(st, Breaking, RuleInputValuesFromAdded, w, "now limited to the fields of "+n.ValuesFrom, "")
		}
		meta.skipEnumPresence = true
	case n.ValuesFrom == "":
		d.add(st, Safe, RuleInputValuesFromRemoved, w, "no longer limited to the fields of "+o.ValuesFrom, "")
		meta.skipEnumPresence = true
	default:
		d.add(st, PossiblyBreaking, RuleInputValuesFromChanged, w, "follows "+n.ValuesFrom+", was "+o.ValuesFrom, "")
		meta.note = "follows " + n.ValuesFrom
	}
	return meta
}

// inputType compares the rotini type of an input.
func (d *differ) inputType(st stability, w string, o, n *input) {
	if o.Type == "" || n.Type == "" || o.Type == n.Type {
		return
	}
	msg := "type " + o.Type + " → " + n.Type
	switch {
	case typeWidened(o.Type, n.Type):
		if o.Kind == "" || n.Kind == "" || o.Kind == n.Kind {
			d.add(st, Safe, RuleInputTypeWidened, w, msg, "")
		} // else the kind change is reported, and breaks
	case n.Type == "float64" && (intBits[o.Type] == 64 || uintBits[o.Type] == 64):
		d.add(st, PossiblyBreaking, RuleInputTypeChanged, w, msg, "an integer beyond 2^53 is rounded")
	default:
		d.add(st, Breaking, RuleInputTypeChanged, w, msg, "")
	}
}

// intBits is the width of each integer type; int is taken as 64 bits.
var intBits = map[string]int{"int8": 8, "int16": 16, "int32": 32, "int64": 64, "int": 64}
var uintBits = map[string]int{"uint8": 8, "uint16": 16, "uint32": 32, "uint64": 64, "uint": 64}

// typeWidened reports whether type n accepts every value type o does.
func typeWidened(o, n string) bool {
	if elem, ok := strings.CutPrefix(n, "[]"); ok && (elem == o || typeWidened(o, elem)) {
		return true // one value is still a list of one
	}
	oe, ook := strings.CutPrefix(o, "[]")
	ne, nok := strings.CutPrefix(n, "[]")
	if ook && nok {
		return typeWidened(oe, ne)
	}
	switch {
	case intBits[o] > 0 && intBits[n] > 0:
		return intBits[n] >= intBits[o]
	case uintBits[o] > 0 && uintBits[n] > 0:
		return uintBits[n] >= uintBits[o]
	case uintBits[o] > 0 && intBits[n] > 0:
		return intBits[n] > uintBits[o]
	case (intBits[o] > 0 && intBits[o] <= 32 || uintBits[o] > 0 && uintBits[o] <= 32 || o == "float32") && n == "float64":
		return true // float64 holds every such value exactly
	case o == "existingfile" && n == "inputfile":
		return true
	}
	return false
}

// implicitValue compares the value an input takes when given without one.
func (d *differ) implicitValue(st stability, w string, o, n *input) {
	switch {
	case o.ImplicitValue == nil && n.ImplicitValue != nil:
		d.add(st, Breaking, RuleInputImplicitValueAdded, w, "the value is now optional and must be attached with =", "")
	case o.ImplicitValue != nil && n.ImplicitValue == nil:
		d.add(st, Breaking, RuleInputImplicitValueRemoved, w, "a value is now required", "")
	case o.ImplicitValue != nil && !deepEqual(o.ImplicitValue, n.ImplicitValue):
		d.add(st, PossiblyBreaking, RuleInputImplicitValueChanged, w,
			fmt.Sprintf("value without one given %s → %s", jsonText(o.ImplicitValue), jsonText(n.ImplicitValue)), "")
	}
}
