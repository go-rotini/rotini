package codegen

import (
	"cmp"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// This file writes a JSON Schema for each configuration file the spec declares
// (generate.schemas.config), describing the keys the program's users may write in it, for
// completion and checking in their editor.

// configSchemaSuffix is the suffix of every config file schema rotini writes. Pruning only
// removes files with this suffix.
const configSchemaSuffix = ".config.json"

// configKeyEntry is one key a configuration file may hold: the input that reads it and the
// commands that bind it.
type configKeyEntry struct {
	key         string   // the dotted key in the file
	schema      any      // the value's JSON Schema
	description string   // the input's description, else its summary
	summary     string   // the input's summary
	deprecated  string   // the deprecation note, "" when not deprecated
	commands    []string // the commands that read it, as typed
	scope       *schemaScope
	input       *InputSchema // the input's declared schema, for its enum values
}

// configCommand is one command, with its ancestor chain, for finding the keys it reads.
type configCommand struct {
	path   string // the command path as config file scopes write it: demo/deploy
	typed  string // the command as typed: demo deploy
	chain  []*Inputs
	scopes []*schemaScope // parallel to chain: the named schemas each frame's $refs resolve against
}

// configCommands lists every visible command with its chain, root first.
func (p *program) configCommands() []configCommand {
	out := []configCommand{{path: p.rootName, typed: p.rootName, chain: []*Inputs{p.rootInputs}, scopes: []*schemaScope{nil}}}
	var walk func(nodes []rnode, parent configCommand)
	walk = func(nodes []rnode, parent configCommand) {
		for _, n := range nodes {
			if n.hidden {
				continue
			}
			c := configCommand{
				path:   parent.path + "/" + n.name,
				typed:  parent.typed + " " + n.name,
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

// readsFile reports whether a command at path reads cf: cf is declared on it or an ancestor, or
// on no command at all.
func readsFile(cf scopedConfigFile, path string) bool {
	return cf.Scope == "" || cf.Scope == path || strings.HasPrefix(path, cf.Scope+"/")
}

// configFileKeys returns the keys cf may hold, sorted: for every visible command that reads it,
// the config inputs of the command's chain that are pinned to cf or to no file, and the config
// keys of the chain's flags and arguments. Hidden inputs and config_source flags (whose value is
// a path, not a key) are left out. The first input seen for a key wins.
func (p *program) configFileKeys(cf scopedConfigFile) []configKeyEntry {
	entries := map[string]*configKeyEntry{}
	add := func(key string, e configKeyEntry, command string) {
		got, ok := entries[key]
		if !ok {
			e.key = key
			entries[key] = &e
			got = &e
		}
		if !slices.Contains(got.commands, command) {
			got.commands = append(got.commands, command)
		}
	}
	for _, c := range p.configCommands() {
		if !readsFile(cf, c.path) {
			continue
		}
		for i, in := range c.chain {
			if in == nil {
				continue
			}
			scope := c.scopes[i]
			for _, cfg := range in.Config {
				if cfg.Hidden || (cfg.Schema != nil && cfg.Schema.File != "" && cfg.Schema.File != cf.Name) {
					continue
				}
				add(configKey(cfg), configKeyEntry{
					schema: inputJSONSchema(cfg.Schema), description: cmp.Or(cfg.Description, cfg.Summary), summary: cfg.Summary,
					deprecated: deprecatedNote(cfg.Deprecated, cfg.DeprecatedSince, cfg.RemovedIn), scope: scope, input: cfg.Schema,
				}, c.typed)
			}
			for _, f := range in.Flags {
				key := flagReconKey(f.Name, f.Schema)
				if f.Hidden || key == "" || f.Schema.ConfigSource != "" {
					continue
				}
				add(key, configKeyEntry{
					schema: inputJSONSchema(f.Schema), description: cmp.Or(f.Description, f.Summary), summary: f.Summary,
					deprecated: deprecatedNote(f.Deprecated, f.DeprecatedSince, f.RemovedIn), scope: scope, input: f.Schema,
				}, c.typed)
			}
			for _, a := range in.Arguments {
				key := flagReconKey(a.Name, a.Schema)
				if a.Hidden || key == "" {
					continue
				}
				add(key, configKeyEntry{
					schema: inputJSONSchema(a.Schema), description: cmp.Or(a.Description, a.Summary), summary: a.Summary,
					deprecated: deprecatedNote(a.Deprecated, a.DeprecatedSince, a.RemovedIn), scope: scope, input: a.Schema,
				}, c.typed)
			}
		}
	}
	out := make([]configKeyEntry, 0, len(entries))
	for _, k := range slices.Sorted(maps.Keys(entries)) {
		out = append(out, *entries[k])
	}
	return out
}

// deprecatedNote is an input's deprecation as one line, or "" when it isn't deprecated.
func deprecatedNote(message, since, removedIn string) string {
	if message == "" {
		return ""
	}
	return deprecationNote(message, since, removedIn)
}

// writeConfigSchemas writes one JSON Schema per non-dotenv configuration file into dir and,
// unless pruning is skipped, removes stale ones.
func (p *program) writeConfigSchemas(dir string) error {
	absDir, err := underModule(p.module.root, "generate.schemas.config.dir", dir)
	if err != nil {
		return err
	}
	files := map[string][]byte{}
	for _, cf := range p.configFiles {
		if dotenvFile(cf.ConfigurationFile) || cf.As == "env" {
			continue
		}
		var path []string
		if cf.Scope != "" {
			path = strings.Split(cf.Scope, "/")[1:]
		}
		name := manPageName(p.rootName, path) + "." + cf.Name + configSchemaSuffix
		if _, dup := files[name]; dup {
			continue
		}
		files[name] = marshalJSONFile(p.configSchemaDoc(cf))
	}
	for _, name := range slices.Sorted(maps.Keys(files)) {
		if err := p.plan.write(filepath.Join(absDir, name), files[name]); err != nil {
			return err
		}
	}
	entries, err := os.ReadDir(absDir)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("read the config schemas directory: %w", err)
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), configSchemaSuffix) || files[e.Name()] != nil || p.skipPrune {
			continue
		}
		if err := p.plan.remove(filepath.Join(absDir, e.Name())); err != nil {
			return fmt.Errorf("remove a stale config schema: %w", err)
		}
		p.pruned = append(p.pruned, filepath.Join(absDir, e.Name()))
	}
	return nil
}

// configSchemaDoc renders cf's JSON Schema: an open object whose properties are the keys it may
// hold, nested by dot, with the file's own declared schema under allOf.
func (p *program) configSchemaDoc(cf scopedConfigFile) map[string]any {
	doc := map[string]any{
		"$schema":     "http://json-schema.org/draft-07/schema#",
		"title":       fmt.Sprintf("%s configuration file %q", p.rootName, cf.Name),
		"description": configFileWhere(cf, p.rootName),
		"type":        "object",
	}
	props := map[string]any{}
	defs := map[string]any{}
	for _, e := range p.configFileKeys(cf) {
		s, _ := e.schema.(map[string]any)
		s = maps.Clone(s)
		if s == nil {
			s = map[string]any{}
		}
		if e.description != "" {
			s["description"] = e.description
		}
		describeEnum(s, e.input)
		if e.deprecated != "" {
			s["deprecated"] = true
			s["deprecationMessage"] = e.deprecated
		}
		if found, ok := reachableDefinitions(s, p.schemasOf(e.scope)); ok {
			maps.Copy(defs, found)
		}
		setNested(props, strings.Split(e.key, "."), s)
	}
	if cf.Profiles != nil {
		props[cf.Profiles.Under] = p.profilesSchema(cf, props)
	}
	if len(props) > 0 {
		doc["properties"] = props
	}
	// A profiled file's declared schema checks the shared keys merged with the selected
	// profile, which a schema of the file itself can't express, so it is left out.
	if cf.Schema != nil && cf.Profiles == nil {
		declared, _ := standardSchema(schemaToDoc(*cf.Schema)).(map[string]any)
		if found, ok := reachableDefinitions(declared, p.schemas); ok && declared != nil {
			doc["allOf"] = []any{declared}
			maps.Copy(defs, found)
		} else {
			p.auditWarnings = append(p.auditWarnings, fmt.Errorf("config file %q: its schema names a schema the spec doesn't declare, so the generated config schema leaves it out", cf.Name))
		}
	}
	if len(defs) > 0 {
		doc["definitions"] = defs
	}
	return doc
}

// profilesSchema describes the key holding cf's profiles: each profile may hold the same keys
// as the top of the file (props).
func (p *program) profilesSchema(cf scopedConfigFile, props map[string]any) map[string]any {
	section := map[string]any{"type": "object"}
	if len(props) > 0 {
		section["properties"] = maps.Clone(props)
	}
	sel := findProfileSelector(p.scopeChain(cf.Scope), cf.Profiles.Select)
	var by []string
	if sel.flag != nil {
		by = append(by, longestIdentifier(sel.flag.Identifiers))
	}
	by = append(by, sel.variables(p.envPrefix)...)
	desc := "Named profiles, one chosen per run"
	if len(by) > 0 {
		desc += " by " + strings.Join(by, " or ")
	}
	desc += ". A profile's keys win over the same keys at the top of the file, which every profile shares."
	if d := sel.defaultFor(cf.Profiles); d != "" {
		desc += fmt.Sprintf(" Without a choice, %q is used.", d)
	}
	return map[string]any{"type": "object", "description": desc, "additionalProperties": section}
}

// longestIdentifier is the flag identifier a description names: its longest spelling.
func longestIdentifier(ids []string) string {
	best := ""
	for _, id := range ids {
		if len(id) > len(best) {
			best = id
		}
	}
	return best
}

// setNested places schema at the dotted path in props, creating the objects between.
func setNested(props map[string]any, path []string, schema map[string]any) {
	if len(path) == 1 {
		if _, taken := props[path[0]]; !taken {
			props[path[0]] = schema
		}
		return
	}
	parent, _ := props[path[0]].(map[string]any)
	if parent == nil {
		parent = map[string]any{"type": "object"}
		props[path[0]] = parent
	}
	inner, _ := parent["properties"].(map[string]any)
	if inner == nil {
		inner = map[string]any{}
		parent["properties"] = inner
	}
	setNested(inner, path[1:], schema)
}

// configFileWhere says where a configuration file is read from and which commands read it.
func configFileWhere(cf scopedConfigFile, root string) string {
	where := cf.Path
	if d := cf.Discover; d != nil {
		switch d.Strategy {
		case "walk-up":
			where = d.File + " in the working directory or the nearest parent holding one"
		case "xdg":
			where = "$XDG_CONFIG_HOME/" + d.App + "/" + d.File + " (default ~/.config/" + d.App + "/" + d.File + ")"
		case "native":
			where = d.App + "/" + d.File + " in the platform's configuration directory"
		case "xdg-system":
			where = d.App + "/" + d.File + " in each $XDG_CONFIG_DIRS directory (default /etc/xdg)"
		}
	}
	by := root + " and its sub-commands"
	if cf.Scope != "" && cf.Scope != root {
		by = strings.ReplaceAll(cf.Scope, "/", " ") + " and its sub-commands"
	}
	return where + ", read by " + by + "."
}

// describeEnum lists an input's enum in s as editors read it, when its values declare more than
// their spelling: every value that is not hidden, then each alias, with `enumDescriptions`
// aligned to them (a value's summary; "same as <value>" for an alias). Validators ignore
// enumDescriptions. The enum sits where inputJSONSchema put it: on s, or on a list's items or a
// map's values.
func describeEnum(s map[string]any, input *InputSchema) {
	if input == nil || !enumDetailed(input.Enum) {
		return
	}
	target := s
	for _, k := range []string{"items", "additionalProperties"} {
		if inner, ok := s[k].(map[string]any); ok && inner["enum"] != nil {
			inner = maps.Clone(inner)
			s[k] = inner
			target = inner
		}
	}
	var values []any
	var descriptions []any
	var aliases []any
	var aliasDescriptions []any
	for _, v := range enumValues(input.Enum) {
		if v.Hidden {
			continue
		}
		values = append(values, v.Value)
		descriptions = append(descriptions, v.Summary)
		for _, a := range slices.Concat(v.Aliases, v.DeprecatedAliases) {
			aliases = append(aliases, a)
			aliasDescriptions = append(aliasDescriptions, "same as "+v.Value)
		}
	}
	target["enum"] = append(values, aliases...)
	target["enumDescriptions"] = append(descriptions, aliasDescriptions...)
}
