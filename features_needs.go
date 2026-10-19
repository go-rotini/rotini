package rotini

import (
	"path/filepath"
	"reflect"
	"slices"
	"strings"
)

// featureNeeds returns, in canonical order, exactly the features a program needs for the
// command tree def, the settings meta and the inputs types (each a generated inputs struct, or
// a pointer to one; the Inputs types def records are walked too). It mirrors the runtime's
// predicates, so a program wired with featureNeeds runs every command as one with every
// feature does. Tests only: runtime code never calls it.
func featureNeeds(def Definition, meta InputSettings, inputs ...reflect.Type) []Feature {
	var n needs
	n.definition(def)
	n.settings(meta)
	for _, t := range append(slices.Clone(inputs), treeInputs(def)...) {
		n.inputsType(t)
	}
	return n.features()
}

// needs records which features a program needs.
type needs struct {
	plugins, respFiles, chdir, multicall                bool
	stdin                                               []string
	env, config, profiles, envFiles, fallbacks, expand  bool
	homes                                               bool // a path may name another user's home ("~bob")
	objects, schemas                                    bool
	json, yaml, toml, jsonc, dotenv                     bool
	bounds, unique, patterns, groups, deps, streamPaths bool
	url, email, timezone, mac, ip, regexp               bool
	visited                                             map[reflect.Type]bool
}

// features lists the recorded needs in canonical order.
func (n *needs) features() []Feature {
	var out []Feature
	add := func(on bool, f func() Feature) {
		if on {
			out = append(out, f())
		}
	}
	add(n.plugins, Plugins)
	add(n.respFiles, ResponseFiles)
	add(n.chdir, Chdir)
	add(n.multicall, Multicall)
	if len(n.stdin) > 0 {
		formats := slices.Clone(n.stdin)
		slices.SortFunc(formats, func(a, b string) int {
			return slices.Index(stdinFormats, a) - slices.Index(stdinFormats, b)
		})
		out = append(out, Stdin(formats...))
	}
	add(n.env, Env)
	add(n.config, ConfigFiles)
	add(n.profiles, Profiles)
	add(n.envFiles, EnvFiles)
	add(n.fallbacks, Fallbacks)
	add(n.expand, PathExpansion)
	add(n.homes && !n.expand, UserHomes) // PathExpansion includes UserHomes
	add(n.objects, ObjectFlags)
	add(n.schemas, Schemas)
	add(n.json, JSON)
	add(n.yaml, YAML)
	add(n.toml, TOML)
	add(n.jsonc, JSONC)
	add(n.dotenv, Dotenv)
	add(n.bounds, Bounds)
	add(n.unique, UniqueItems)
	add(n.patterns, Patterns)
	add(n.groups, FlagGroups)
	add(n.deps, FlagDependencies)
	add(n.streamPaths, StreamPaths)
	add(n.url, URLValues)
	add(n.email, EmailValues)
	add(n.timezone, TimeZoneValues)
	add(n.mac, MACValues)
	add(n.ip, IPValues)
	add(n.regexp, RegexpValues)
	return out
}

// treeInputs is every Inputs type def records, root first.
func treeInputs(def Definition) []reflect.Type {
	var out []reflect.Type
	if def.Inputs != nil {
		out = append(out, def.Inputs)
	}
	var walk func(cmds []CommandDef)
	walk = func(cmds []CommandDef) {
		for _, c := range cmds {
			if c.Inputs != nil {
				out = append(out, c.Inputs)
			}
			walk(c.Commands)
		}
	}
	walk(def.Commands)
	return out
}

// definition records what the command tree needs.
func (n *needs) definition(def Definition) {
	n.respFiles = def.ResponseFiles != nil
	_, n.chdir = chdirFlag(def)
	n.multicall = def.Multicall != nil
	n.command(def.Flags, def.Arguments, def.FlagGroups, def.FlagDependencies, def.Plugins, def.PluginDiscovery, def.PluginPath)
	var walk func(cmds []CommandDef)
	walk = func(cmds []CommandDef) {
		for _, c := range cmds {
			n.command(c.Flags, c.Arguments, c.FlagGroups, c.FlagDependencies, c.Plugins, c.PluginDiscovery, c.PluginPath)
			walk(c.Commands)
		}
	}
	walk(def.Commands)
}

// command records what one command's declarations need.
func (n *needs) command(flags []FlagDef, args []ArgDef, groups []FlagGroup, deps []FlagDependency, plugins []PluginDef, discovery *PluginDiscoveryDef, pluginPath string) {
	if len(plugins) > 0 || discovery != nil {
		n.plugins = true
	}
	if namesOtherHome(pluginPath) {
		n.homes = true
	}
	n.groups = n.groups || len(groups) > 0
	n.deps = n.deps || len(deps) > 0
	for _, fd := range flags {
		if isObjectFlag(&fd) {
			n.objects, n.json, n.yaml, n.schemas = true, true, true, true
		}
		n.declared(fd.Type, fd.Constraints)
	}
	for _, ad := range args {
		n.declared(ad.Type, ad.Constraints)
	}
}

// declared records what an input of declared type typ with constraints c needs.
func (n *needs) declared(typ string, c Constraints) {
	n.constraints(c)
	elem := strings.TrimPrefix(typ, "[]")
	if _, v, ok := strings.Cut(elem, "]"); ok && strings.HasPrefix(elem, "map[") {
		elem = v
	}
	if isStreamPathType(elem) {
		n.streamPaths = true
	}
	switch strings.TrimPrefix(elem, "*") {
	case "url.URL":
		n.url = true
	case "mail.Address":
		n.email = true
	case "time.Location":
		n.timezone = true
	case "net.HardwareAddr":
		n.mac = true
	case "netip.Addr", "netip.Prefix", "netip.AddrPort":
		n.ip = true
	case "regexp.Regexp":
		n.regexp = true
	}
}

// constraints records the validation features c needs.
func (n *needs) constraints(c Constraints) {
	if c.Minimum != nil || c.Maximum != nil || c.ExclusiveMinimum != nil || c.ExclusiveMaximum != nil ||
		c.MultipleOf != nil || c.MinLength > 0 || c.MaxLength != nil || c.MinItems > 0 || c.MaxItems != nil {
		n.bounds = true
	}
	if c.UniqueItems {
		n.unique = true
	}
	if c.Pattern != "" {
		n.patterns = true
	}
}

// settings records what the input settings need.
func (n *needs) settings(meta InputSettings) {
	if len(meta.Sources) > 0 {
		n.config = true
	}
	if len(meta.StdinSchemas) > 0 {
		n.schemas = true
	}
	for _, f := range meta.ConfigFiles {
		if f.As == "env" {
			n.envFiles, n.dotenv = true, true
		} else {
			n.config = true
			n.fileFormat(f)
		}
		if f.Profiles != nil {
			n.profiles = true
		}
		if f.Schema != "" {
			n.schemas = true
		}
		if namesOtherHome(f.Path) || (f.Discover != nil && namesOtherHome(f.Discover.File)) {
			n.homes = true
		}
		// A path chosen at run time may name another user's home ("~bob/app.yaml").
		if f.PathFrom != nil {
			n.homes = true
		}
		for _, k := range f.Keys {
			n.declared(k.Type, k.Constraints) // a config write checks a key as the flag it is
		}
	}
}

// fileFormat records the codec a configuration file decodes with: its format, else its
// extension; every document codec when its path is chosen at run time.
func (n *needs) fileFormat(f ConfigFile) {
	format := f.Format
	if format == "" {
		name := f.Path
		if name == "" && f.Discover != nil {
			name = f.Discover.File
		}
		switch strings.ToLower(filepath.Ext(name)) {
		case ".json":
			format = "json"
		case ".yaml", ".yml":
			format = "yaml"
		case ".toml":
			format = "toml"
		case ".jsonc", ".json5":
			format = "jsonc"
		case ".env":
			format = "dotenv"
		}
	}
	if f.PathFrom != nil && f.Format == "" {
		n.json, n.yaml, n.toml, n.jsonc, n.dotenv = true, true, true, true, true
	}
	n.codec(format)
}

// codec records the codec of format.
func (n *needs) codec(format string) {
	switch format {
	case "json", "jsonl":
		n.json = true
	case "yaml":
		n.yaml = true
	case "toml":
		n.toml = true
	case "jsonc":
		n.jsonc = true
	case "dotenv":
		n.dotenv = true
	}
}

// namesOtherHome reports whether path starts with "~user".
func namesOtherHome(path string) bool {
	return len(path) > 1 && path[0] == '~' && path[1] != '/' && path[1] != '\\'
}

// inputsType records what one generated inputs struct needs.
func (n *needs) inputsType(t reflect.Type) {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if t.Kind() != reflect.Struct {
		return
	}
	v := reflect.New(t).Elem()
	n.env = n.env || hasEnvChannel(v)
	n.config = n.config || hasConfigChannel(v)
	n.fallbacks = n.fallbacks || hasReconFlags(v)
	n.expand = n.expand || hasExpansion(v) || hasPathRules(v)
	n.envFiles = n.envFiles || hasFileVars(v)
	for _, ci := range v.Fields() {
		if ci.Kind() != reflect.Struct {
			continue
		}
		for _, name := range []string{"Flags", "Arguments", "Env", "Config"} {
			if s := ci.FieldByName(name); s.IsValid() && s.Kind() == reflect.Struct {
				n.fields(s.Type(), name == "Env" || name == "Config")
			}
		}
	}
	if t.NumField() > 0 {
		n.stdinField(t.Field(t.NumField() - 1).Type)
	}
}

// fields records the value kinds of struct type t's fields, nested ones included, and for a
// channel struct the bounds and patterns its tags declare.
func (n *needs) fields(t reflect.Type, channel bool) {
	if n.visited == nil {
		n.visited = map[reflect.Type]bool{}
	}
	if n.visited[t] {
		return
	}
	n.visited[t] = true
	for f := range t.Fields() {
		if channel {
			if c, ok := channelConstraints(f.Tag); ok {
				n.constraints(c)
			}
		}
		ft := f.Type
		for {
			n.kind(ft)
			switch ft.Kind() {
			case reflect.Pointer, reflect.Slice, reflect.Array, reflect.Map:
				ft = ft.Elem()
				continue
			}
			break
		}
		if ft.Kind() == reflect.Struct && ft.PkgPath() != "time" && ft.PkgPath() != "net/url" && ft.PkgPath() != "net/mail" && ft.PkgPath() != "net/netip" && ft.PkgPath() != "regexp" {
			n.fields(ft, channel)
		}
	}
}

// kind records the value kind of type t, if it is one.
func (n *needs) kind(t reflect.Type) {
	id, _, ok := extraKindOf(t)
	if !ok {
		return
	}
	switch id {
	case "url":
		n.url = true
	case "email":
		n.email = true
	case "timezone":
		n.timezone = true
	case "mac":
		n.mac = true
	case "ip":
		n.ip = true
	case "regexp":
		n.regexp = true
	}
}

// stdinField records the stdin format the leaf frame type t reads, if any.
func (n *needs) stdinField(t reflect.Type) {
	if t.Kind() != reflect.Struct {
		return
	}
	f, ok := t.FieldByName("Stdin")
	if !ok || (f.Type.Kind() != reflect.Pointer && f.Type.Kind() != reflect.Func) {
		return
	}
	raw := f.Tag.Get("stdin")
	if raw == "" {
		return
	}
	format := parseStdinTag(raw).format
	if !slices.Contains(n.stdin, format) {
		n.stdin = append(n.stdin, format)
	}
	n.codec(format)
}
