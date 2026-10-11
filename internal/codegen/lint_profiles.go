package codegen

import (
	"fmt"
	"slices"
	"strings"
)

// lintProfiles checks each config_files entry's `profiles`: the selector names a string flag or
// env input in scope that is read before any configuration file, the profiles key collides with
// no key the file is read for, the default is declared once and is one of the selector's
// values, and the file is a configuration file, not a dotenv file. Two files in one chain
// sharing a selector but not the key holding their profiles is a warning.
func lintProfiles(spec *Spec) []error {
	var problems []error
	walkChainsAt(spec, func(chain []*Command, path, ptr string) {
		cmd := chain[len(chain)-1]
		if cmd.inputs() == nil {
			return
		}
		inputs := make([]*Inputs, len(chain))
		for i, c := range chain {
			inputs[i] = c.inputs()
		}
		for i, cf := range cmd.inputs().ConfigFiles {
			p := cf.Profiles
			if p == nil {
				continue
			}
			at := func(key string, sev severity, msg string) {
				problems = append(problems, &problem{
					kind: "spec", ptr: fmt.Sprintf("%s/config_files/%d/profiles/%s", ptr, i, key), loc: "command " + path, sev: sev,
					msg: fmt.Sprintf("config_files %q: %s", cf.Name, msg),
				})
			}
			if dotenvFile(cf) || cf.As == "env" {
				at("under", severityError, "sets `profiles` on a dotenv file, whose KEY=value lines have no sections to choose from; remove `profiles`")
				continue
			}
			sel := findProfileSelector(inputs, p.Select)
			selectorProblems(sel, p, at, len(cmd.Commands) > 0)
			if key, owner := profileKeyCollision(chain, cf, p.Under); key != "" {
				at("under", severityError, fmt.Sprintf("profiles.under is %q, the first segment of key %q (%s); profiles need a key of their own", p.Under, key, owner))
			}
			if other := sharedSelector(chain, cf); other != nil {
				at("select", severityWarning, fmt.Sprintf("profiles.select %q is also the selector of config_files %q, which keeps its profiles under %q, not %q; one selector usually reads one key", p.Select, other.Name, other.Profiles.Under, p.Under))
			}
		}
	})
	return problems
}

// selectorProblems checks the input(s) a profiles.select names. hasChildren says whether the
// file's command has sub-commands, under which a non-cascading flag isn't listed in help.
func selectorProblems(sel profileSelector, p *ConfigurationFileProfiles, at func(key string, sev severity, msg string), hasChildren bool) {
	if sel.flag == nil && sel.env == nil {
		at("select", severityError, fmt.Sprintf("profiles.select names %q, but no flag or env input of that name is declared on this command or an ancestor", p.Select))
		return
	}
	var defaults []string
	if f := sel.flag; f != nil {
		name := fmt.Sprintf("flag %q", f.Name)
		defaults = append(defaults, selectorInputProblems(name, f.Schema, p, at)...)
		if s := f.Schema; s != nil && s.ConfigSource != "" {
			at("select", severityError, fmt.Sprintf("profiles.select names %s, which sets `config_source`; a selector chooses a profile, not a file", name))
		}
		if s := f.Schema; s != nil && s.Key != "" {
			at("select", severityError, fmt.Sprintf("profiles.select names %s, which sets `key`; the selector is read before any configuration file, so it can't come from one", name))
		}
		if hasChildren && !f.Cascading {
			at("select", severityWarning, fmt.Sprintf("profiles.select names %s, which isn't cascading; it still parses after a sub-command, but the sub-commands' help won't list it; set `cascading: true`", name))
		}
	}
	if e := sel.env; e != nil {
		name := fmt.Sprintf("env %q", e.Name)
		defaults = append(defaults, selectorInputProblems(name, e.Schema, p, at)...)
		if s := e.Schema; s != nil && s.Nesting != "" {
			at("select", severityError, fmt.Sprintf("profiles.select names %s, which sets `nesting`; a selector is one variable", name))
		}
	}
	if p.Default != "" {
		defaults = append(defaults, fmt.Sprintf("profiles.default %q", p.Default))
	}
	if len(defaults) > 1 && !sameDefaults(sel, p) {
		at("default", severityError, fmt.Sprintf("the profile's default is declared more than once (%s); declare it in one place", strings.Join(defaults, ", ")))
	}
}

// selectorInputProblems checks what a flag and an env selector share: a string type, and a
// profiles.default among its enum values. It returns the input's default, described, when it
// declares one.
func selectorInputProblems(name string, s *InputSchema, p *ConfigurationFileProfiles, at func(key string, sev severity, msg string)) []string {
	if t := getSchemaType(s); t != "string" {
		at("select", severityError, fmt.Sprintf("profiles.select names %s, whose type is %s; a profile name is a string", name, t))
	}
	if s == nil {
		return nil
	}
	if p.Default != "" && len(s.Enum) > 0 && !enumHas(s.Enum, p.Default) {
		at("default", severityError, fmt.Sprintf("profiles.default %q is not one of %s's values", p.Default, name))
	}
	if d := defaultString(s.Default); d != "" {
		return []string{fmt.Sprintf("%s default %q", name, d)}
	}
	return nil
}

// sameDefaults reports whether every default the selector and the profiles block declare is the
// same value.
func sameDefaults(sel profileSelector, p *ConfigurationFileProfiles) bool {
	var seen []string
	if sel.flag != nil && sel.flag.Schema != nil {
		if d := defaultString(sel.flag.Schema.Default); d != "" {
			seen = append(seen, d)
		}
	}
	if sel.env != nil && sel.env.Schema != nil {
		if d := defaultString(sel.env.Schema.Default); d != "" {
			seen = append(seen, d)
		}
	}
	if p.Default != "" {
		seen = append(seen, p.Default)
	}
	return len(slices.Compact(seen)) <= 1
}

// enumHas reports whether value is one of an enum's values or aliases.
func enumHas(members []any, value string) bool {
	for _, v := range enumValues(members) {
		if v.Value == value || slices.Contains(v.Aliases, value) || slices.Contains(v.DeprecatedAliases, value) {
			return true
		}
	}
	return false
}

// profileKeyCollision finds a key read from cf whose first segment is under: a config input's
// key (pinned to cf or to no file), or a flag's or argument's configuration key, on cf's
// command, its ancestors or its descendants. It returns the key and the input that reads it.
func profileKeyCollision(chain []*Command, cf ConfigurationFile, under string) (key, owner string) {
	check := func(in *Inputs) {
		if in == nil || key != "" {
			return
		}
		for _, c := range in.Config {
			if c.Schema != nil && c.Schema.File != "" && c.Schema.File != cf.Name {
				continue
			}
			if k := configKey(c); firstSegment(k) == under {
				key, owner = k, fmt.Sprintf("config %q", c.Name)
				return
			}
		}
		for _, f := range in.Flags {
			if k := flagReconKey(f.Name, f.Schema); k != "" && firstSegment(k) == under {
				key, owner = k, fmt.Sprintf("flag %q", f.Name)
				return
			}
		}
		for _, a := range in.Arguments {
			if k := flagReconKey(a.Name, a.Schema); k != "" && firstSegment(k) == under {
				key, owner = k, fmt.Sprintf("argument %q", a.Name)
				return
			}
		}
	}
	for _, c := range chain {
		check(c.inputs())
	}
	var walk func(cmds []Command)
	walk = func(cmds []Command) {
		for i := range cmds {
			check(cmds[i].inputs())
			walk(cmds[i].Commands)
		}
	}
	walk(chain[len(chain)-1].Commands)
	return key, owner
}

// firstSegment is a dotted key's first segment.
func firstSegment(key string) string {
	first, _, _ := strings.Cut(key, ".")
	return first
}

// sharedSelector returns another profiled file in the chain, declared earlier, with the same
// selector but a different profiles key, or nil.
func sharedSelector(chain []*Command, cf ConfigurationFile) *ConfigurationFile {
	for _, c := range chain {
		in := c.inputs()
		if in == nil {
			continue
		}
		for i := range in.ConfigFiles {
			other := &in.ConfigFiles[i]
			if other.Name == cf.Name {
				return nil // only files declared before cf are compared, so each pair is reported once
			}
			if other.Profiles != nil && other.Profiles.Select == cf.Profiles.Select && other.Profiles.Under != cf.Profiles.Under {
				return other
			}
		}
	}
	return nil
}
