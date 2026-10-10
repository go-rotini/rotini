package codegen

import (
	"cmp"
	"fmt"
	"path"
	"slices"
	"strings"
	"text/template"

	"github.com/go-rotini/yaml"
)

// This file writes the carapace feature: a carapace-spec file (https://carapace.sh) describing
// the program's command tree, for users who complete through carapace. It carries what the
// spec declares statically: commands, flags, enum values and completion hints. Values that a
// Go completer supplies at run time have no carapace form, so they get no candidates there.

// carapaceSchemaHeader points editors at carapace-spec's JSON Schema.
const carapaceSchemaHeader = "# yaml-language-server: $schema=https://carapace.sh/schemas/command.json\n"

// renderCarapace writes <root>.yaml under the feature's `file` directory, or to `file` itself
// when it names a .yaml or .yml file. carapace requires the file name to match the root name.
func renderCarapace(p *program, f *Feature, _ *agentProgram, _ *template.Template) (map[string][]byte, []error, error) {
	doc := p.carapaceCommand(p.rootName, nil, p.rootHelp, p.rootInputs, p.tree, p.rootPlugins, p.rootPassthrough, false)
	var out strings.Builder
	out.WriteString(carapaceSchemaHeader)
	writeYAMLMap(&out, doc, "")
	rel := p.rootName + ".yaml"
	if ext := path.Ext(f.File); ext == ".yaml" || ext == ".yml" {
		rel = ""
	}
	return map[string][]byte{rel: []byte(out.String())}, nil, nil
}

// carapaceCommand builds one command's carapace-spec mapping, its children included.
func (p *program) carapaceCommand(name string, aliases []string, help cmdHelp, in *Inputs, children []rnode, plugins []PluginSpec, passthrough, hidden bool) yaml.MapSlice {
	cmd := yaml.MapSlice{{Key: "name", Value: name}}
	add := func(key string, value any, when bool) {
		if when {
			cmd = append(cmd, yaml.MapItem{Key: key, Value: value})
		}
	}
	add("aliases", aliases, len(aliases) > 0)
	add("description", help.Summary, help.Summary != "")
	add("hidden", true, hidden)
	if in == nil {
		in = &Inputs{}
	}
	switch {
	case passthrough:
		add("parsing", "disabled", true)
	case in.OptionsFirst || (len(in.Arguments) > 0 && in.Arguments[0].Passthrough):
		add("parsing", "non-interspersed", true)
	}
	local, persistent, flagValues, names := p.carapaceFlags(in.Flags)
	add("flags", local, len(local) > 0)
	add("persistentflags", persistent, len(persistent) > 0)
	exclusive := carapaceExclusive(in.FlagGroups, names)
	add("exclusiveflags", exclusive, len(exclusive) > 0)
	completion := yaml.MapSlice{}
	if len(flagValues) > 0 {
		completion = append(completion, yaml.MapItem{Key: "flag", Value: flagValues})
	}
	if !passthrough {
		completion = append(completion, p.carapacePositionals(in.Arguments)...)
	}
	add("completion", completion, len(completion) > 0)
	subs := make([]yaml.MapSlice, 0, len(children)+len(plugins))
	for _, n := range children {
		subs = append(subs, p.carapaceCommand(n.name, visibleAliases(n), n.help, n.inputs, n.children, n.plugins, n.passthrough, n.hidden || n.deprecated != ""))
	}
	for _, pl := range plugins {
		subs = append(subs, carapacePlugin(pl))
	}
	add("commands", subs, len(subs) > 0)
	return cmd
}

// carapaceFlags splits a command's flags into its own and its persistent (cascading) entries,
// and returns each flag's completion values and the name carapace knows it by.
func (p *program) carapaceFlags(flags []FlagInput) (local, persistent, values yaml.MapSlice, names map[string]string) {
	values = yaml.MapSlice{}
	names = map[string]string{}
	for _, fl := range flags {
		entries, cname := carapaceFlag(fl)
		if fl.Cascading {
			persistent = append(persistent, entries...)
		} else {
			local = append(local, entries...)
		}
		names[fl.Name] = cname
		if v := p.carapaceValues(fl.Schema); len(v) > 0 && cname != "" && carapaceTakesValue(fl.Schema) {
			values = append(values, yaml.MapItem{Key: cname, Value: v})
		}
	}
	return local, persistent, values, names
}

// carapaceExclusive lists the mutually_exclusive flag groups by the flags' carapace names. The
// other kinds of group have no carapace form.
func carapaceExclusive(groups []FlagGroup, names map[string]string) [][]string {
	var out [][]string
	for _, g := range groups {
		if g.Kind != "mutually_exclusive" {
			continue
		}
		var group []string
		for _, n := range g.Flags {
			if c := names[n]; c != "" {
				group = append(group, c)
			}
		}
		if len(group) > 1 {
			out = append(out, group)
		}
	}
	return out
}

// carapacePositionals lists each argument's completion values by position, and those of a
// variadic or passthrough last argument as `positionalany`.
func (p *program) carapacePositionals(args []ArgumentInput) yaml.MapSlice {
	var positional [][]string
	var rest []string
	for i, a := range args {
		values := p.carapaceValues(a.Schema)
		if a.Passthrough || (i == len(args)-1 && isListType(definitionType(a.Schema, nil))) {
			rest = values
			break
		}
		if values == nil {
			values = []string{}
		}
		positional = append(positional, values)
	}
	for len(positional) > 0 && len(positional[len(positional)-1]) == 0 {
		positional = positional[:len(positional)-1]
	}
	var out yaml.MapSlice
	if len(positional) > 0 {
		out = append(out, yaml.MapItem{Key: "positional", Value: positional})
	}
	if len(rest) > 0 {
		out = append(out, yaml.MapItem{Key: "positionalany", Value: rest})
	}
	return out
}

// carapacePlugin is a plugin's entry: the plugin parses its own words.
func carapacePlugin(pl PluginSpec) yaml.MapSlice {
	sub := yaml.MapSlice{{Key: "name", Value: pl.Name}}
	if len(pl.Aliases) > 0 {
		sub = append(sub, yaml.MapItem{Key: "aliases", Value: pl.Aliases})
	}
	if pl.Summary != "" {
		sub = append(sub, yaml.MapItem{Key: "description", Value: pl.Summary})
	}
	return append(sub, yaml.MapItem{Key: "parsing", Value: "disabled"})
}

// visibleAliases is a command's aliases without its deprecated ones, which completion leaves out.
func visibleAliases(n rnode) []string {
	var out []string
	for _, a := range n.aliases {
		if !slices.Contains(n.deprecatedIdentifiers, a) {
			out = append(out, a)
		}
	}
	return out
}

// carapaceFlag returns a flag's carapace-spec entries (`-s, --long=` keys with their
// description) and the name its completion values are keyed by. carapace takes one short and
// one long spelling per entry, so each further identifier, each hidden or deprecated one and
// each negated form becomes its own hidden entry.
func carapaceFlag(f FlagInput) (yaml.MapSlice, string) {
	ids := flagIdentifiers(f)
	var short, long string
	var extra []string
	for _, id := range ids {
		switch {
		case slices.Contains(f.DeprecatedIdentifiers, id):
			extra = append(extra, id)
		case !strings.HasPrefix(id, "--") && len([]rune(id)) == 2 && short == "":
			short = id
		case (strings.HasPrefix(id, "--") || len([]rune(id)) > 2) && long == "":
			long = id
		default:
			extra = append(extra, id)
		}
	}
	extra = append(extra, f.HiddenIdentifiers...)
	mods := carapaceModifiers(f.Schema)
	hidden := f.Hidden || f.Deprecated != ""
	required := f.Schema != nil && f.Schema.Required
	main := strings.Join(slices.DeleteFunc([]string{short, long}, func(s string) bool { return s == "" }), ", ")
	key := main + mods
	if required {
		key += "!"
	}
	if hidden {
		key += "&"
	}
	entries := make(yaml.MapSlice, 0, 1+len(extra)+2)
	entries = append(entries, yaml.MapItem{Key: key, Value: f.Summary})
	for _, id := range extra {
		entries = append(entries, yaml.MapItem{Key: id + mods + "&", Value: f.Summary})
	}
	for _, id := range negatedForms(f) {
		entries = append(entries, yaml.MapItem{Key: id + "&", Value: f.Summary})
	}
	return entries, strings.TrimLeft(cmp.Or(long, short), "-")
}

// carapaceModifiers is the modifier suffix of a flag's key: `=` takes a value, `*` repeats
// (a list or map, or a count with no value), `?` takes an optional attached value.
func carapaceModifiers(s *InputSchema) string {
	switch t := definitionType(s, nil); {
	case t == "bool":
		return ""
	case t == "count":
		return "*"
	case s != nil && s.ImplicitValue != nil:
		return "?"
	case isListType(t) || strings.HasPrefix(t, "map["):
		return "=*"
	default:
		return "="
	}
}

// carapaceTakesValue reports whether a flag takes a value whose candidates carapace can offer.
func carapaceTakesValue(s *InputSchema) bool {
	t := definitionType(s, nil)
	return t != "bool" && t != "count"
}

// isListType reports whether a definition type is a list.
func isListType(t string) bool { return strings.HasPrefix(t, "[]") }

// carapaceValues lists an input's static candidates: its enum values (with their summaries,
// leaving out hidden and deprecated ones) and the macro for its completion hint, or else its
// completion message, which the completion feature keeps only when its messages are on.
func (p *program) carapaceValues(s *InputSchema) []string {
	if s == nil {
		return nil
	}
	var out []string
	for _, v := range enumValues(enumOrItems(s)) {
		if v.Hidden || v.Deprecated != "" {
			continue
		}
		if v.Summary != "" {
			out = append(out, v.Value+"\t"+v.Summary)
		} else {
			out = append(out, v.Value)
		}
	}
	if c := s.Complete; c != nil {
		switch c.Kind {
		case "file":
			if len(c.Extensions) > 0 {
				exts := make([]string, len(c.Extensions))
				for i, e := range c.Extensions {
					exts[i] = "." + strings.TrimPrefix(e, ".")
				}
				out = append(out, "$files(["+strings.Join(exts, ", ")+"])")
			} else {
				out = append(out, "$files")
			}
		case "directory":
			out = append(out, "$directories")
		case "executable":
			out = append(out, "$executables")
		case "user":
			out = append(out, "$carapace.os.Users")
		case "group":
			out = append(out, "$carapace.os.Groups")
		case "host":
			out = append(out, "$carapace.net.Hosts")
		case completeKindCommandName:
			for _, n := range p.tree {
				if !n.hidden && n.deprecated == "" {
					out = append(out, n.name)
				}
			}
		}
		if c.Message != "" && len(out) == 0 {
			out = append(out, "$message("+c.Message+")")
		}
	}
	return out
}

// completeKindCommandName is the completion hint that offers command paths.
const completeKindCommandName = "command"

// enumOrItems returns an input's enum, or its list items' enum.
func enumOrItems(s *InputSchema) []any {
	if len(s.Enum) > 0 {
		return s.Enum
	}
	if s.Items != nil {
		return s.Items.Enum
	}
	return nil
}

// writeYAMLMap writes an ordered mapping as block YAML at indent: nested mappings and lists of
// mappings in block form, lists of scalars one per line, and lists of lists in flow form.
func writeYAMLMap(b *strings.Builder, m yaml.MapSlice, indent string) {
	for _, item := range m {
		key := yamlKey(fmt.Sprint(item.Key))
		switch v := item.Value.(type) {
		case yaml.MapSlice:
			fmt.Fprintf(b, "%s%s:\n", indent, key)
			writeYAMLMap(b, v, indent+"  ")
		case []yaml.MapSlice:
			fmt.Fprintf(b, "%s%s:\n", indent, key)
			for _, sub := range v {
				var inner strings.Builder
				writeYAMLMap(&inner, sub, indent+"    ")
				fmt.Fprintf(b, "%s  - %s", indent, strings.TrimPrefix(inner.String(), indent+"    "))
			}
		case []string:
			fmt.Fprintf(b, "%s%s:\n", indent, key)
			for _, s := range v {
				fmt.Fprintf(b, "%s  - %s\n", indent, yamlValue(s))
			}
		case [][]string:
			fmt.Fprintf(b, "%s%s:\n", indent, key)
			for _, list := range v {
				items := make([]string, len(list))
				for i, s := range list {
					items[i] = yamlValue(s)
				}
				fmt.Fprintf(b, "%s  - [%s]\n", indent, strings.Join(items, ", "))
			}
		default:
			fmt.Fprintf(b, "%s%s: %s\n", indent, key, yamlValue(v))
		}
	}
}
