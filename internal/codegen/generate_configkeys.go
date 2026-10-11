package codegen

import (
	"cmp"
	"fmt"
	"slices"
	"strings"
)

// This file builds each configuration file's key table (ConfigFile.Keys): every key an input
// reads from the file, with what the runtime checks a written value against.

// keyDef is one key a configuration file may hold, as one input declares it.
type keyDef struct {
	key    string
	schema *InputSchema
	scope  *schemaScope
}

// keyCommand is one command, hidden ones included, with its ancestor chain.
type keyCommand struct {
	path   string // the command path as config file scopes write it: demo/deploy
	chain  []*Inputs
	scopes []*schemaScope
}

// keyCommands lists every command, hidden ones included, with its chain, root first.
func (p *program) keyCommands() []keyCommand {
	out := []keyCommand{{path: p.rootName, chain: []*Inputs{p.rootInputs}, scopes: []*schemaScope{nil}}}
	var walk func(nodes []rnode, parent keyCommand)
	walk = func(nodes []rnode, parent keyCommand) {
		for _, n := range nodes {
			c := keyCommand{
				path:   parent.path + "/" + n.name,
				chain:  append(slices.Clone(parent.chain), n.inputs),
				scopes: append(slices.Clone(parent.scopes), n.scope),
			}
			out = append(out, c)
			walk(n.children, c)
		}
	}
	walk(p.tree, out[0])
	return out
}

// fileKeys returns every key an input reads from cf, sorted by key, one entry per declaring
// input: for each command that reads cf, hidden ones included, the config inputs of its chain
// pinned to cf or to no file, and the config keys of the chain's flags and arguments. A
// config_source flag, whose value is a path, and a profile selector, which never reads a file,
// are not keys. An `as: env` file's keys are the variables of the chain's env inputs and
// flag and argument fallbacks instead.
func (p *program) fileKeys(cf scopedConfigFile) []keyDef {
	var out []keyDef
	seen := map[string]bool{}
	add := func(key string, schema *InputSchema, scope *schemaScope) {
		if key == "" {
			return
		}
		id := fmt.Sprintf("%s\x00%p", key, schema)
		if seen[id] {
			return
		}
		seen[id] = true
		out = append(out, keyDef{key: key, schema: schema, scope: scope})
	}
	for _, c := range p.keyCommands() {
		if !readsFile(cf, c.path) {
			continue
		}
		selectors := p.selectorsFor(c.path)
		for i, in := range c.chain {
			if in == nil {
				continue
			}
			scope := c.scopes[i]
			if cf.As == "env" {
				p.envFileKeys(in, scope, add)
				continue
			}
			for _, cfg := range in.Config {
				if cfg.Schema != nil && cfg.Schema.File != "" && cfg.Schema.File != cf.Name {
					continue
				}
				add(configKey(cfg), cfg.Schema, scope)
			}
			for _, f := range in.Flags {
				if f.Schema != nil && f.Schema.ConfigSource != "" || slices.Contains(selectors, f.Name) {
					continue
				}
				add(flagReconKey(f.Name, f.Schema), f.Schema, scope)
			}
			for _, a := range in.Arguments {
				add(flagReconKey(a.Name, a.Schema), a.Schema, scope)
			}
		}
	}
	slices.SortStableFunc(out, func(a, b keyDef) int { return cmp.Compare(a.key, b.key) })
	return out
}

// envFileKeys adds the variables an `as: env` file may set for one command of a chain: each
// env input's first variable, and each flag's and argument's first fallback variable.
func (p *program) envFileKeys(in *Inputs, scope *schemaScope, add func(string, *InputSchema, *schemaScope)) {
	first := func(vars string) string {
		v, _, _ := strings.Cut(vars, ",")
		return v
	}
	for _, e := range in.Env {
		if e.Schema != nil && e.Schema.Nesting != "" {
			continue // a family of variables, not one key
		}
		add(first(envVarName(e, p.envPrefix)), e.Schema, scope)
	}
	for _, f := range in.Flags {
		add(first(flagEnvVar(f.Schema, flagReconKey(f.Name, f.Schema), p.envPrefix)), f.Schema, scope)
	}
	for _, a := range in.Arguments {
		add(first(flagEnvVar(a.Schema, flagReconKey(a.Name, a.Schema), p.envPrefix)), a.Schema, scope)
	}
}

// selectorsFor is the profile selector of every profiled config file a command at path reads.
func (p *program) selectorsFor(path string) []string {
	var out []string
	for _, f := range p.configFiles {
		if f.Profiles != nil && readsFile(f, path) {
			out = append(out, f.Profiles.Select)
		}
	}
	return out
}

// renderConfigKeys renders a config file's Keys field, or nothing when no input reads it.
func renderConfigKeys(b *strings.Builder, gp *program, f scopedConfigFile) {
	keys := gp.fileKeys(f)
	if len(keys) == 0 {
		return
	}
	fmt.Fprintf(b, ", Keys: []%s.ConfigKey{", rotiniPkgName)
	var items []string
	for _, k := range keys {
		if item := configKeyLiteral(gp, k); !slices.Contains(items, item) { // inputs declaring a key alike
			items = append(items, item)
		}
	}
	b.WriteString(strings.Join(items, ", "))
	b.WriteString("}")
}

// configKeyLiteral renders one ConfigKey literal.
func configKeyLiteral(gp *program, k keyDef) string {
	var b strings.Builder
	schemas := gp.schemasOf(k.scope)
	schema := k.schema
	fmt.Fprintf(&b, "{Key: %q, Type: %q", k.key, definitionType(schema, schemas))
	writeKeyRules(&b, schema)
	if objectSchemaFor(schema, schemas) != "" || strings.HasSuffix(goFieldType(schema), "map[string]any") {
		b.WriteString(", Object: true")
	}
	if c := constraintFields(schema); c != "" {
		b.WriteString(", ")
		b.WriteString(c)
	}
	b.WriteString("}")
	return b.String()
}

// writeKeyRules appends a ConfigKey's enum, layouts, separator and secrecy, omitting zero values.
func writeKeyRules(b *strings.Builder, schema *InputSchema) {
	if schema == nil {
		return
	}
	if len(schema.Enum) > 0 {
		fmt.Fprintf(b, ", Enum: %s", goStringSlice(enumStrings(schema.Enum)))
		if enumDetailed(schema.Enum) {
			fmt.Fprintf(b, ", EnumValues: %s", enumValuesLiteral(schema.Enum))
		}
		if schema.IgnoreCase {
			b.WriteString(", IgnoreCase: true")
		}
	}
	if l := layoutsFor(schema); len(l) > 0 {
		fmt.Fprintf(b, ", Layout: %q", l[0])
		if len(l) > 1 {
			fmt.Fprintf(b, ", Layouts: %s", goStringSlice(l))
		}
	}
	if schema.Separator != "" {
		fmt.Fprintf(b, ", Separator: %q", runtimeSeparator(schema.Separator))
	}
	if schema.Secret {
		b.WriteString(", Secret: true")
	}
}
