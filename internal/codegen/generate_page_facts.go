package codegen

import (
	"fmt"
	"slices"
	"strings"
)

// Page facts the spec declares beside a command's inputs: the configuration files its inputs
// are read from, and the rules between its flags. Help, man and markdown show both in the
// user's words, so a user learns them from the page rather than by breaking one.

// filesOf lists the configuration files the command at names (below the root) reads, in the
// order they win: the command's own first, then each ancestor's, as the input reader's
// chainConfigFiles orders them. It returns nil when none of the page's inputs reads a
// configuration file, so a page with nothing to read from a file doesn't list any.
func (gp *program) filesOf(names []string, d templateHelpData) []templateDocFileRow {
	if !readsConfigValues(d) {
		return nil
	}
	scope := strings.Join(append([]string{gp.rootName}, names...), "/")
	chain := gp.scopeChain(scope)
	var rows []templateDocFileRow
	for _, cf := range gp.chainFiles(scope) {
		rows = append(rows, gp.fileRow(cf, chain))
	}
	return rows
}

// chainFiles is the configuration files in scope for the command at scope, nearest command's
// first, each command's in declared order, then the unscoped ones.
func (gp *program) chainFiles(scope string) []scopedConfigFile {
	var out []scopedConfigFile
	segs := strings.Split(scope, "/")
	for n := len(segs); n > 0; n-- {
		at := strings.Join(segs[:n], "/")
		for _, cf := range gp.configFiles {
			if cf.Scope == at {
				out = append(out, cf)
			}
		}
	}
	for _, cf := range gp.configFiles {
		if cf.Scope == "" {
			out = append(out, cf)
		}
	}
	return out
}

// readsConfigValues reports whether any row on the page can be set from a configuration file:
// a configuration input, or a flag or argument showing its config key.
func readsConfigValues(d templateHelpData) bool {
	if len(d.Configuration) > 0 {
		return true
	}
	for _, rows := range [][]templateDocFlagRow{d.Flags, d.Cascading} {
		for _, r := range rows {
			if r.ConfigKey != "" {
				return true
			}
		}
	}
	for _, a := range d.Arguments {
		if a.ConfigKey != "" {
			return true
		}
	}
	return false
}

// fileRow describes one configuration file for a page whose command's chain is chain.
func (gp *program) fileRow(cf scopedConfigFile, chain []*Inputs) templateDocFileRow {
	row := templateDocFileRow{Name: cf.Name, File: fileDisplayName(cf.ConfigurationFile), AsEnv: cf.As == "env"}
	if declared, implied := configFormatOf(cf.ConfigurationFile), configFormatOf(withoutFormat(cf.ConfigurationFile)); declared != implied {
		row.Format = declared
	}
	if d := cf.Discover; d != nil {
		switch d.Strategy {
		case "walk-up":
			row.Found = "in the working directory or the nearest parent holding one"
		case "xdg":
			row.Found = "in $XDG_CONFIG_HOME (default ~/.config)"
		case "native":
			row.Found = "in the platform's configuration directory"
		case "xdg-system":
			row.Found = "in each $XDG_CONFIG_DIRS directory (default /etc/xdg)"
		}
	}
	row.PathFrom = pathFromInputs(chain, cf.Name, gp.envPrefix)
	if p := cf.Profiles; p != nil {
		sel := findProfileSelector(chain, p.Select)
		prof := &templateDocProfiles{Under: p.Under, Default: sel.defaultFor(p)}
		if sel.flag != nil {
			prof.Select = append(prof.Select, flagLabel(*sel.flag))
		}
		prof.Select = append(prof.Select, sel.variables(gp.envPrefix)...)
		row.Profiles = prof
	}
	row.Note = fileNote(row)
	return row
}

// withoutFormat is cf with its declared format cleared, to tell the format its name implies.
func withoutFormat(cf ConfigurationFile) ConfigurationFile {
	cf.Format = ""
	return cf
}

// fileDisplayName is a configuration file as a user names it: the declared path, or the name
// discovery looks for, under its application directory when the strategy has one
// (taskr/config.yaml).
func fileDisplayName(cf ConfigurationFile) string {
	d := cf.Discover
	switch {
	case d == nil:
		return cf.Path
	case d.App != "":
		return d.App + "/" + d.File
	default:
		return d.File
	}
}

// pathFromInputs lists the inputs along chain that name another path for the file called name
// (`config_source`): each flag by its long identifier, then each variable.
func pathFromInputs(chain []*Inputs, name, envPrefix string) []string {
	var flags, vars []string
	for _, in := range chain {
		if in == nil {
			continue
		}
		for _, f := range in.Flags {
			if f.Hidden || f.Schema == nil || f.Schema.ConfigSource != name {
				continue
			}
			flags = appendNew(flags, flagLabel(f))
			if v := flagEnvVar(f.Schema, flagReconKey(f.Name, f.Schema), envPrefix); v != "" {
				vars = appendNew(vars, strings.Split(v, ",")...)
			}
		}
		for _, e := range in.Env {
			if e.Hidden || e.Schema == nil || e.Schema.ConfigSource != name {
				continue
			}
			vars = appendNew(vars, strings.Split(envVarName(e, envPrefix), ",")...)
		}
	}
	return append(flags, vars...)
}

// appendNew appends each of items to list that it doesn't hold yet.
func appendNew(list []string, items ...string) []string {
	for _, it := range items {
		if it = strings.TrimSpace(it); it != "" && !slices.Contains(list, it) {
			list = append(list, it)
		}
	}
	return list
}

// fileNote is a file row's facts in one line, for the default templates.
func fileNote(r templateDocFileRow) string {
	var parts []string
	if r.Format != "" {
		parts = append(parts, r.Format)
	}
	if r.Found != "" {
		parts = append(parts, r.Found)
	}
	if len(r.PathFrom) > 0 {
		parts = append(parts, "or the file "+orList(r.PathFrom)+" names")
	}
	if r.AsEnv {
		parts = append(parts, "read as environment variables")
	}
	if p := r.Profiles; p != nil {
		note := fmt.Sprintf("profiles under %q", p.Under)
		if len(p.Select) > 0 {
			note += ", chosen by " + p.Select[0] // the flag when there is one; its variables are on its row
		}
		if p.Default != "" {
			note += " (default " + p.Default + ")"
		}
		parts = append(parts, note)
	}
	return strings.Join(parts, "; ")
}

// flagLabel names a flag in a sentence: its first long identifier that isn't deprecated
// (--json), else its first identifier that isn't.
func flagLabel(f FlagInput) string {
	ids, _ := undeprecated(flagIdentifiers(f), f.DeprecatedIdentifiers, "")
	for _, id := range ids {
		if strings.HasPrefix(id, "--") {
			return id
		}
	}
	if len(ids) > 0 {
		return ids[0]
	}
	return f.Name
}

// flagRules describes the rules between a command's flags, groups first then dependencies, each
// in declared order. A rule naming a hidden flag is left out, as the flag is.
func flagRules(inputs *Inputs) []templateDocFlagRule {
	if inputs == nil {
		return nil
	}
	label := map[string]string{}
	for _, f := range inputs.Flags {
		if !f.Hidden {
			label[f.Name] = flagLabel(f)
		}
	}
	labels := func(names []string) ([]string, bool) {
		out := make([]string, 0, len(names))
		for _, n := range names {
			l, ok := label[n]
			if !ok {
				return nil, false
			}
			out = append(out, l)
		}
		return out, true
	}
	var rules []templateDocFlagRule
	for _, g := range inputs.FlagGroups {
		flags, ok := labels(g.Flags)
		if !ok {
			continue
		}
		rules = append(rules, templateDocFlagRule{Kind: g.Kind, Flags: flags, Text: groupText(g.Kind, flags)})
	}
	for _, dep := range inputs.FlagDependencies {
		requires, ok1 := labels(dep.Requires)
		forbids, ok2 := labels(dep.Forbids)
		unless, ok3 := labels(dep.Unless)
		when := ""
		ok4 := true
		if dep.When != "" {
			when, ok4 = label[dep.When]
		}
		if !ok1 || !ok2 || !ok3 || !ok4 {
			continue
		}
		cond := dependencyCondition(when, dep.Equals, unless)
		rules = append(rules, templateDocFlagRule{
			Kind: "dependency", Flags: slices.Concat(requires, forbids), Condition: cond,
			Text: cond + ", " + dependencyEffect(requires, forbids),
		})
	}
	return rules
}

// groupText is a flag group's rule as a sentence.
func groupText(kind string, flags []string) string {
	switch kind {
	case "mutually_exclusive":
		return joinAnd(flags) + " can't be used together"
	case "required_together":
		return joinAnd(flags) + " must be used together"
	case "one_of":
		return "exactly one of " + orList(flags) + " is required"
	case "at_least_one":
		return "at least one of " + orList(flags) + " is required"
	}
	return kind + ": " + strings.Join(flags, ", ")
}

// dependencyCondition is when a flag dependency applies: "with --tls", "with --format csv or
// tsv", "unless --config is given", or both joined.
func dependencyCondition(when string, equals []any, unless []string) string {
	var parts []string
	if when != "" {
		c := "with " + when
		if len(equals) > 0 {
			values := make([]string, len(equals))
			for i, v := range equals {
				values[i] = fmt.Sprint(v)
			}
			c += " " + orList(values)
		}
		parts = append(parts, c)
	}
	if len(unless) > 0 {
		parts = append(parts, "unless "+orList(unless)+" is given")
	}
	return strings.Join(parts, " and ")
}

// dependencyEffect is what a triggered flag dependency asks for: "--cert is required and
// --insecure can't be used".
func dependencyEffect(requires, forbids []string) string {
	var parts []string
	switch len(requires) {
	case 0:
	case 1:
		parts = append(parts, requires[0]+" is required")
	default:
		parts = append(parts, joinAnd(requires)+" are required")
	}
	if len(forbids) > 0 {
		parts = append(parts, orList(forbids)+" can't be used")
	}
	return strings.Join(parts, " and ")
}
