package codegen

import (
	"path/filepath"
	"slices"
	"strconv"
	"strings"
)

// This file decides which runtime features the generated NewProgram passes to
// rotini.NewWiredProgram. A program links only the features it is given, and a run that needs
// one it lacks fails, so the decision is conservative: whatever the spec declares, and
// whatever a declaration might need at run time, is wired.

// wiring records the features a program needs, one flag per feature.
type wiring struct {
	plugins, respFiles, chdir, multicall               bool
	stdin                                              map[string]bool
	env, config, profiles, envFiles, fallbacks, expand bool
	objects, schemas                                   bool
	json, yaml, toml, jsonc, dotenv                    bool
	bounds, patterns, groups, deps, streamPaths        bool
	url, email, timezone, mac, ip, regexp              bool
}

// stdinFormatOrder is the order stdin formats are passed to rotini.Stdin.
var stdinFormatOrder = []string{"text", "lines", "bytes", "json", "jsonl", "yaml", "jsonc", "toml"}

// wiredFeatures returns the feature constructors the generated NewProgram passes, as Go
// expressions in the runtime's canonical order. It reads the merged command tree, composed
// subtrees included, and the program's configuration files.
func wiredFeatures(gp *program) []string {
	multicall, _, _ := gp.multicall()
	w := wiring{stdin: map[string]bool{}, respFiles: gp.responseFiles() != nil, multicall: multicall}
	w.command(gp, gp.rootInputs, gp.rootPlugins, gp.rootDiscovery, gp.rootPluginPath, gp.schemas)
	var walk func(nodes []rnode)
	walk = func(nodes []rnode) {
		for i := range nodes {
			n := &nodes[i]
			w.command(gp, n.inputs, n.plugins, n.discovery, n.pluginPath, gp.schemasOf(n.scope))
			walk(n.children)
		}
	}
	walk(gp.tree)
	pathFrom := collectPathFrom(gp)
	for _, f := range gp.configFiles {
		_, hasPathFrom := pathFrom[f.Name]
		w.configFile(f.ConfigurationFile, hasPathFrom)
	}
	if len(collectStdinSchemas(gp)) > 0 {
		w.schemas = true
	}
	return w.expressions()
}

// command records what one command's declarations need. schemas resolves its $refs.
func (w *wiring) command(gp *program, in *Inputs, plugins []PluginSpec, discovery *PluginDiscovery, pluginPath string, schemas map[string]Schema) {
	if len(plugins) > 0 || discovery != nil {
		w.plugins = true
	}
	if namesOtherHome(pluginPath) {
		w.expand = true
	}
	if in == nil {
		return
	}
	fields := gp.inputFieldsOf(in)
	for _, fd := range slices.Concat(fields.flags, fields.args) {
		if fd.Recon != "" {
			w.fallbacks = true
		}
	}
	if len(fields.env) > 0 {
		w.env = true
	}
	if len(fields.config) > 0 {
		w.config = true
	}
	for _, fs := range [][]fieldDef{fields.flags, fields.args, fields.env, fields.config} {
		for _, fd := range fs {
			if fd.EnvFile != "" {
				w.envFiles = true
			}
		}
	}
	w.groups = w.groups || flagGroupsLiteral(in) != ""
	w.deps = w.deps || flagDependenciesLiteral(in) != ""
	for _, f := range in.Flags {
		if f.Role == "chdir" {
			w.chdir = true
		}
		if objectSchemaFor(f.Schema, schemas) != "" {
			w.objects, w.schemas, w.json, w.yaml = true, true, true, true
		}
		w.input(f.Schema, schemas)
	}
	for _, a := range in.Arguments {
		w.input(a.Schema, schemas)
	}
	for _, e := range in.Env {
		w.input(e.Schema, schemas)
	}
	for _, c := range in.Config {
		w.input(c.Schema, schemas)
	}
	if format := stdinFormatExpr(in); format != "" {
		format, _, _ = strings.Cut(format, ",")
		w.stdin[format] = true
		w.codec(format)
	}
	for _, f := range in.ConfigFiles {
		w.configFile(f, false)
	}
}

// input records what one input's schema needs: its bounds, pattern, expansion, variable
// file, and the value types of the input and of everything its schema names.
func (w *wiring) input(schema *InputSchema, schemas map[string]Schema) {
	if schema == nil {
		return
	}
	eachConstraint(schema, func(tag, _, _, _ string) {
		switch tag {
		case "pattern", "patternmsg":
			w.patterns = true
		default:
			w.bounds = true
		}
	})
	if len(schema.Expand) > 0 || schema.RelativeTo != "" {
		w.expand = true
	}
	if schema.VariableFile != "" {
		w.envFiles = true
	}
	w.typeName(definitionType(schema, schemas))
	w.typeName(goFieldType(schema))
	w.schemaTypes(schema.BaseSchema, schemas, map[string]bool{})
}

// schemaTypes records the value types s and its items and properties name, following $refs
// through schemas; seen guards against cycles.
func (w *wiring) schemaTypes(s BaseSchema, schemas map[string]Schema, seen map[string]bool) {
	w.typeName(s.Type)
	if name := refTypeName(s.Ref); name != "" && !seen[name] {
		seen[name] = true
		if ref, ok := schemas[name]; ok {
			w.schemaTypes(ref.BaseSchema, schemas, seen)
		}
	}
	if s.Items != nil {
		w.schemaTypes(s.Items.BaseSchema, schemas, seen)
	}
	for _, p := range s.Properties {
		w.schemaTypes(p.BaseSchema, schemas, seen)
	}
}

// typeName records the value type t names, as a schema type, a Go type, or a list, map or
// pointer of either.
func (w *wiring) typeName(t string) {
	t = strings.TrimSpace(t)
	if elem, ok := strings.CutPrefix(t, "[]"); ok {
		w.typeName(elem)
		return
	}
	if elem, ok := strings.CutPrefix(t, "*"); ok {
		w.typeName(elem)
		return
	}
	if key, val, ok := splitMapType(t); ok {
		w.typeName(key)
		w.typeName(val)
		return
	}
	switch t {
	case "inputfile", "outputfile":
		w.streamPaths = true
	case "url", "url.URL":
		w.url = true
	case "email", "mail.Address":
		w.email = true
	case "timezone", "time.Location":
		w.timezone = true
	case "mac", "net.HardwareAddr":
		w.mac = true
	case "ip", "cidr", "hostport", "netip.Addr", "netip.Prefix", "netip.AddrPort":
		w.ip = true
	case "regexp", "regexp.Regexp":
		w.regexp = true
	}
}

// configFile records what one configuration file needs. Any file wires the configuration
// file machinery, which also locates files for write-back; an .env file adds the .env
// layer and its codec. A file whose path the user can supply at run time (pathFrom) may name
// another user's home.
func (w *wiring) configFile(f ConfigurationFile, pathFrom bool) {
	w.config = true
	if f.As == "env" {
		w.envFiles, w.dotenv = true, true
	}
	if f.Profiles != nil {
		w.profiles = true
	}
	if f.Schema != nil {
		w.schemas = true
	}
	discover := ""
	if f.Discover != nil {
		discover = f.Discover.File
	}
	if pathFrom || namesOtherHome(f.Path) || namesOtherHome(discover) {
		w.expand = true
	}
	format := f.Format
	if format == "" {
		name := f.Path
		if name == "" {
			name = discover
		}
		format = formatOfExtension(name)
	}
	if pathFrom && f.Format == "" {
		// The user names the file at run time, so any format may arrive, an .env file included.
		w.json, w.yaml, w.toml, w.jsonc, w.dotenv = true, true, true, true, true
	}
	if format == "" && f.As != "env" {
		// No format and no known extension: wire every document codec, so whichever the
		// file turns out to be decodes as it would with every feature.
		w.json, w.yaml, w.toml, w.jsonc = true, true, true, true
	}
	w.codec(format)
}

// formatOfExtension is the configuration format a file name's extension implies, "" for none.
func formatOfExtension(name string) string {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".json":
		return "json"
	case ".yaml", ".yml":
		return "yaml"
	case ".toml":
		return "toml"
	case ".jsonc", ".json5":
		return "jsonc"
	case ".env":
		return "dotenv"
	}
	return ""
}

// codec records the codec a stdin or file format decodes with.
func (w *wiring) codec(format string) {
	switch format {
	case "json", "jsonl":
		w.json = true
	case "yaml", "yml":
		w.yaml = true
	case "toml":
		w.toml = true
	case "jsonc", "json5":
		w.jsonc = true
	case "dotenv", "env":
		w.dotenv = true
	}
}

// namesOtherHome reports whether path starts with "~user".
func namesOtherHome(path string) bool {
	return len(path) > 1 && path[0] == '~' && path[1] != '/' && path[1] != '\\'
}

// expressions lists the recorded features as constructor calls in canonical order.
func (w *wiring) expressions() []string {
	var out []string
	add := func(on bool, ctor string) {
		if on {
			out = append(out, rotiniPkgName+"."+ctor+"()")
		}
	}
	add(w.plugins, "Plugins")
	add(w.respFiles, "ResponseFiles")
	add(w.chdir, "Chdir")
	add(w.multicall, "Multicall")
	if len(w.stdin) > 0 {
		var formats []string
		for _, f := range stdinFormatOrder {
			if w.stdin[f] {
				formats = append(formats, strconv.Quote(f))
			}
		}
		for f := range w.stdin {
			if !slices.Contains(stdinFormatOrder, f) {
				// An unknown format: wire every format, as a program with every feature reads.
				formats = nil
				break
			}
		}
		out = append(out, rotiniPkgName+".Stdin("+strings.Join(formats, ", ")+")")
	}
	add(w.env, "Env")
	add(w.config, "ConfigFiles")
	add(w.profiles, "Profiles")
	add(w.envFiles, "EnvFiles")
	add(w.fallbacks, "Fallbacks")
	add(w.expand, "PathExpansion")
	add(w.objects, "ObjectFlags")
	add(w.schemas, "Schemas")
	add(w.json, "JSON")
	add(w.yaml, "YAML")
	add(w.toml, "TOML")
	add(w.jsonc, "JSONC")
	add(w.dotenv, "Dotenv")
	add(w.bounds, "Bounds")
	add(w.patterns, "Patterns")
	add(w.groups, "FlagGroups")
	add(w.deps, "FlagDependencies")
	add(w.streamPaths, "StreamPaths")
	add(w.url, "URLValues")
	add(w.email, "EmailValues")
	add(w.timezone, "TimeZoneValues")
	add(w.mac, "MACValues")
	add(w.ip, "IPValues")
	add(w.regexp, "RegexpValues")
	return out
}
