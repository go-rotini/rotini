package codegen

import (
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"maps"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"unicode"
)

// This file generates constants for the names a spec declares, so handler code names them
// once and a renamed value stops compiling instead of drifting:
//
//   - each enum value, `const TaskrListStatusOpen = "open"`, with its summary as the doc
//     comment, and a named schema's values typed by the schema's own type (StatusOpen Status);
//   - each configuration file, config key, profile and environment variable the spec declares,
//     the env_prefix applied (TaskrConfigFileUser, TaskrConfigKeyServerPort, TaskrProfileProd,
//     TaskrEnvToken).

// namedConst is one generated constant.
type namedConst struct {
	name   string   // the Go name
	typ    string   // the Go type of a typed constant; "" for an untyped one
	value  string   // the string it holds
	doc    []string // doc comment lines, each starting "//"
	origin string   // the spec entry behind it, for a collision message
	ptr    string   // the entry's spec pointer, for lint; "" when it has none
	loc    string   // the entry's command, for lint
}

// constGroup is one const block: constants of one input, schema or kind of name, under one
// comment.
type constGroup struct {
	comment string
	consts  []namedConst
	names   bool // names (files, keys, profiles, env), declared in the cmd file; else enum values, beside the types
}

// identPart spells s as identifier words: each run of characters a Go identifier can't hold is
// a word boundary, then the words are PascalCased ("dry-run" and "dry.run" give DryRun, "json"
// JSON). "" when s has no letter or digit.
func identPart(s string) string {
	return toPascalCase(strings.Map(func(r rune) rune {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			return r
		}
		return '-'
	}, s))
}

// enumConstType reports whether an input of schema gets enum constants: its value is a string,
// a list of strings or a pointer to either, so an untyped string constant compares with it.
func enumConstType(schema *InputSchema) bool {
	switch strings.TrimPrefix(goFieldType(schema), "*") {
	case "string", "[]string":
		return true
	}
	return false
}

// ownCommand is one command of a spec's own tree: not composed from another spec.
type ownCommand struct {
	cmd    *Command
	chain  []*Command // the root down to cmd
	path   string     // slash-separated names, the root first
	ptr    string
	prefix string // the generated type prefix
}

// ownCommandsOf lists spec's own commands in tree order, root first. A composed child names its
// own constants, in its own package.
func ownCommandsOf(spec *Spec) []ownCommand {
	var out []ownCommand
	rootPascal := toPascalCase(spec.Command.Name)
	var walk func(c *Command, chain []*Command, names []string, ptr string)
	walk = func(c *Command, chain []*Command, names []string, ptr string) {
		chain = append(slices.Clip(chain), c)
		out = append(out, ownCommand{cmd: c, chain: chain, path: strings.Join(names, "/"), ptr: ptr,
			prefix: rootPascal + toPascalCase(strings.Join(names[1:], "-"))})
		for i := range c.Commands {
			child := &c.Commands[i]
			if child.Ref != "" || child.Name == "" {
				continue
			}
			walk(child, chain, append(slices.Clip(names), child.Name), fmt.Sprintf("%s/commands/%d", ptr, i))
		}
	}
	walk(&spec.Command, nil, []string{spec.Command.Name}, rootPointer)
	return out
}

// invocation is a command's path as typed: "taskr list".
func (c ownCommand) invocation() string { return strings.ReplaceAll(c.path, "/", " ") }

// enumGroup is the constants for the enum values of one input (or a flag set's flag) whose
// field is named by prefix+field: one per value that makes an identifier.
func enumGroup(prefix, field, subject, ptr, loc string, members []any) constGroup {
	g := constGroup{comment: "// The values of " + subject + "."}
	for _, v := range enumValues(members) {
		part := identPart(v.Value)
		if part == "" {
			continue
		}
		c := namedConst{
			name: prefix + field + part, value: v.Value, ptr: ptr, loc: loc,
			origin: fmt.Sprintf("the enum value %q of %s", v.Value, subject),
			doc:    fieldDoc(v.Summary, v.Deprecated),
		}
		g.consts = append(g.consts, c)
	}
	return g
}

// inputEnumGroups is the enum constants of every string input of spec's own commands, then of
// each flag set an own command uses, then of each string-valued named schema, typed by the
// schema's type.
func inputEnumGroups(spec *Spec) []constGroup {
	var out []constGroup
	usedSets := map[string]bool{}
	for _, oc := range ownCommandsOf(spec) {
		c := oc.cmd
		add := func(channel, name, ptr string, schema *InputSchema) {
			subject := fmt.Sprintf("the %s %s of %s", name, channel, oc.invocation())
			out = appendGroup(out, inputEnumGroup(oc.prefix, name, subject, ptr+"/schema", "command "+oc.path, schema))
		}
		own := ownFlagCount(c, spec.Command.FlagSets)
		for i, f := range c.Flags[:min(own, len(c.Flags))] {
			add("flag", f.Name, fmt.Sprintf("%s/flags/%d", oc.ptr, i), f.Schema)
		}
		for _, name := range c.Use {
			usedSets[name] = true
		}
		for i, a := range c.Arguments {
			add("argument", a.Name, fmt.Sprintf("%s/arguments/%d", oc.ptr, i), a.Schema)
		}
		for i, e := range c.Env {
			add("env input", e.Name, fmt.Sprintf("%s/env/%d", oc.ptr, i), e.Schema)
		}
		for i, cfg := range c.Config {
			add("config input", cfg.Name, fmt.Sprintf("%s/config/%d", oc.ptr, i), cfg.Schema)
		}
	}
	rootPascal := toPascalCase(spec.Command.Name)
	for _, name := range slices.Sorted(maps.Keys(usedSets)) {
		for i, f := range spec.Command.FlagSets[name].Flags {
			subject := fmt.Sprintf("the %s flag of flag set %s", f.Name, name)
			ptr := fmt.Sprintf("%s/flag_sets/%s/flags/%d/schema", rootPointer, name, i)
			out = appendGroup(out, inputEnumGroup(rootPascal+name, f.Name, subject, ptr, rootLabel(spec), f.Schema))
		}
	}
	return append(out, schemaEnumGroups(spec)...)
}

// inputEnumGroup is the constants of the input name's enum, named prefix+<Name>+<Value>, or
// none when the input isn't a string or a list of strings with an enum of its own. ptr is the
// input's schema.
func inputEnumGroup(prefix, name, subject, ptr, loc string, schema *InputSchema) constGroup {
	if schema == nil || schema.Ref != "" || !enumConstType(schema) {
		return constGroup{}
	}
	if len(schema.Enum) == 0 {
		ptr += "/items"
	}
	return enumGroup(prefix, toPascalCase(name), subject, ptr+"/enum", loc, enumOrItems(schema))
}

// schemaEnumGroups is the constants of each string-valued named schema's enum, typed by the
// schema's own generated type.
func schemaEnumGroups(spec *Spec) []constGroup {
	var out []constGroup
	for _, name := range slices.Sorted(maps.Keys(spec.Command.Schemas)) {
		s := spec.Command.Schemas[name]
		if s.Type != "string" || len(s.Enum) == 0 {
			continue
		}
		g := enumGroup(name, "", "the schema "+name, rootPointer+"/schemas/"+name+"/enum", rootLabel(spec), s.Enum)
		for i := range g.consts {
			g.consts[i].typ = name
		}
		out = appendGroup(out, g)
	}
	return out
}

// outputEnumGroups is the enum constants of the generated output and stdin types' string
// fields, found by walking src (the generated types) against defs (their schemas) as
// [sizedNumberTypes] does: <Type><Field><Value>. A field typed by a named schema is left to the
// schema's own constants.
func outputEnumGroups(src string, defs map[string]any) ([]constGroup, error) {
	const pkgClause = "package p\n\n"
	file, err := parser.ParseFile(token.NewFileSet(), "", pkgClause+src, parser.SkipObjectResolution)
	if err != nil {
		return nil, fmt.Errorf("parse generated types: %w", err)
	}
	specs := map[string]*ast.TypeSpec{}
	ast.Inspect(file, func(n ast.Node) bool {
		if ts, ok := n.(*ast.TypeSpec); ok {
			specs[ts.Name.Name] = ts
		}
		return true
	})
	var out []constGroup
	visited := map[string]bool{}
	var visitType func(name string, sch map[string]any)
	var visitField func(typ, field string, expr ast.Expr, sch map[string]any)
	visitField = func(typ, field string, expr ast.Expr, sch map[string]any) {
		if sch == nil {
			return
		}
		if _, ref := sch["$ref"]; ref {
			return
		}
		switch e := expr.(type) {
		case *ast.StarExpr:
			visitField(typ, field, e.X, sch)
		case *ast.ArrayType:
			items, _ := sch["items"].(map[string]any)
			if id, ok := e.Elt.(*ast.Ident); ok && id.Name == "string" {
				if members, ok := items["enum"].([]any); ok {
					out = appendGroup(out, enumGroup(typ, field, "the "+field+" field of "+typ, "", "", members))
				}
				return
			}
			visitField(typ, field, e.Elt, items)
		case *ast.MapType:
			values, _ := sch["additionalProperties"].(map[string]any)
			visitField(typ, field, e.Value, values)
		case *ast.Ident:
			if e.Name == "string" {
				if members, ok := sch["enum"].([]any); ok {
					out = appendGroup(out, enumGroup(typ, field, "the "+field+" field of "+typ, "", "", members))
				}
				return
			}
			visitType(e.Name, sch)
		}
	}
	visitType = func(name string, sch map[string]any) {
		ts, ok := specs[name]
		if !ok || visited[name] {
			return
		}
		visited[name] = true
		st, ok := ts.Type.(*ast.StructType)
		if !ok {
			return
		}
		props := schemaProperties(sch)
		for _, f := range st.Fields.List {
			if f.Tag == nil || len(f.Names) == 0 {
				continue
			}
			tag, err := strconv.Unquote(f.Tag.Value)
			if err != nil {
				continue
			}
			key, _, _ := strings.Cut(reflect.StructTag(tag).Get("json"), ",")
			visitField(name, f.Names[0].Name, f.Type, props[key])
		}
	}
	for _, name := range slices.Sorted(maps.Keys(defs)) {
		sch, _ := defs[name].(map[string]any)
		visitType(name, sch)
	}
	return out, nil
}

// appendGroup appends g to groups when it has constants.
func appendGroup(groups []constGroup, g constGroup) []constGroup {
	if len(g.consts) == 0 {
		return groups
	}
	return append(groups, g)
}

// nameGroups is the constants for the configuration files, config keys, profiles and
// environment variables spec's own commands declare. envPrefix is the env_prefix generate
// applies, which may come from a composed child; a constant's name depends only on the
// declared one.
func nameGroups(spec *Spec, envPrefix string) []constGroup {
	n := nameCollector{
		root:           toPascalCase(spec.Command.Name),
		envPrefix:      envPrefix,
		declaredPrefix: spec.Command.EnvPrefix,
		files:          constGroup{names: true, comment: "// The configuration files, by the name rotini.SetConfigValue, rotini.UnsetConfigValue,\n// rotini.ConfigFilePath and rotini.ConfigProfiles take."},
		keys:           constGroup{names: true, comment: "// The keys the configuration files hold, as rotini.SetConfigValue and\n// rotini.UnsetConfigValue take them."},
		profiles:       constGroup{names: true, comment: "// The profiles the spec names, for rotini.ToProfile."},
		env:            constGroup{names: true, comment: "// The environment variables the program reads, env_prefix applied."},
	}
	for _, oc := range ownCommandsOf(spec) {
		n.command(oc)
	}
	var out []constGroup
	for _, g := range []constGroup{n.files, n.keys, n.profiles, n.env} {
		out = appendGroup(out, g)
	}
	return out
}

// nameCollector gathers [nameGroups]'s constants, one command at a time.
type nameCollector struct {
	root                      string
	envPrefix, declaredPrefix string
	files, keys, profiles     constGroup
	env                       constGroup
	loc                       string // the command being read
}

// add appends a constant <root><kind><value's words> to g, unless value makes no words.
func (n *nameCollector) add(g *constGroup, kind, value, ident, ptr, origin string) {
	if part := identPart(ident); value != "" && part != "" {
		g.consts = append(g.consts, namedConst{name: n.root + kind + part, value: value, ptr: ptr, loc: n.loc, origin: origin})
	}
}

// command reads one own command's config files, keys and variables.
func (n *nameCollector) command(oc ownCommand) {
	c := oc.cmd
	n.loc = "command " + oc.path
	for i, f := range c.ConfigFiles {
		ptr := fmt.Sprintf("%s/config_files/%d", oc.ptr, i)
		n.add(&n.files, "ConfigFile", f.Name, f.Name, ptr+"/name", fmt.Sprintf("the config_files name %q of %s", f.Name, oc.invocation()))
		if f.Profiles != nil {
			for _, name := range profileNames(oc.chain, f.Profiles) {
				n.add(&n.profiles, "Profile", name, name, ptr+"/profiles", fmt.Sprintf("the profile %q of config file %q", name, f.Name))
			}
		}
	}
	argv := func(name, kind string, i int, schema *InputSchema) {
		ptr, subject := fmt.Sprintf("%s/%s/%d", oc.ptr, kind, i), fmt.Sprintf("the %s %s of %s", name, strings.TrimSuffix(kind, "s"), oc.invocation())
		if key := flagReconKey(name, schema); key != "" {
			n.variables(variables(schema), key, ptr, subject)
			key := flagConfigKey(name, schema)
			n.add(&n.keys, "ConfigKey", key, key, ptr, fmt.Sprintf("the config key %q of %s", key, subject))
		}
	}
	for i, f := range c.Flags {
		argv(f.Name, "flags", i, f.Schema)
	}
	for i, a := range c.Arguments {
		argv(a.Name, "arguments", i, a.Schema)
	}
	for i, e := range c.Env {
		n.variables(variables(e.Schema), e.Name, fmt.Sprintf("%s/env/%d", oc.ptr, i), fmt.Sprintf("the %s env input of %s", e.Name, oc.invocation()))
	}
	for i, cfg := range c.Config {
		key := configKey(cfg)
		n.add(&n.keys, "ConfigKey", key, key, fmt.Sprintf("%s/config/%d", oc.ptr, i), fmt.Sprintf("the config key %q of the %s config input of %s", key, cfg.Name, oc.invocation()))
	}
}

// variables adds an input's environment variables: those it names, else the one derived from
// key under the env_prefix. A constant is named for the variable without the declared prefix.
func (n *nameCollector) variables(vars []string, key, ptr, subject string) {
	if len(vars) == 0 {
		v := envVarFor(key, n.envPrefix)
		n.add(&n.env, "Env", v, strings.ToLower(envVarFor(key, "")), ptr, fmt.Sprintf("the environment variable %s of %s", v, subject))
		return
	}
	for _, v := range vars {
		n.add(&n.env, "Env", v, strings.ToLower(withoutPrefix(v, n.declaredPrefix)), ptr, fmt.Sprintf("the environment variable %s of %s", v, subject))
	}
}

// withoutPrefix is an explicit variable's name without the declared env_prefix, which every
// derived name shares, so TASKR_TOKEN names a constant as the derived token does.
func withoutPrefix(v, prefix string) string {
	if prefix == "" {
		return v
	}
	return strings.TrimPrefix(v, prefix+"_")
}

// profileNames are the profile names a config file's profiles declare: its default, and the
// values its selector (a flag or env input of that name on the command or an ancestor) allows.
func profileNames(chain []*Command, p *ConfigurationFileProfiles) []string {
	var out []string
	if p.Default != "" {
		out = append(out, p.Default)
	}
	for _, c := range slices.Backward(chain) {
		for _, f := range c.Flags {
			if f.Name == p.Select && f.Schema != nil {
				out = append(out, enumStrings(f.Schema.Enum)...)
			}
		}
		for _, e := range c.Env {
			if e.Name == p.Select && e.Schema != nil {
				out = append(out, enumStrings(e.Schema.Enum)...)
			}
		}
	}
	return out
}

// mergeConstGroups drops each constant an earlier one already declares with the same value
// (an input read from two channels alike, a file read by two commands), and groups left
// empty. Constants that share a name but not a value stay, for the collision checks to report.
func mergeConstGroups(groups []constGroup) []constGroup {
	seen := map[string]bool{} // name and value
	var out []constGroup
	for _, g := range groups {
		kept := g.consts[:0:0]
		for _, c := range g.consts {
			key := c.name + "\x00" + c.value
			if seen[key] {
				continue
			}
			seen[key] = true
			kept = append(kept, c)
		}
		g.consts = kept
		out = appendGroup(out, g)
	}
	return out
}

// withoutAuthorNames drops each constant whose name a file of the author's in the package
// already declares, as a project that wrote its own constants before rotini generated them
// does: the author's declaration stays, and the build with it. It returns a notice per name.
func withoutAuthorNames(groups []constGroup, author map[string]string) ([]constGroup, []error) {
	if len(author) == 0 {
		return groups, nil
	}
	var notices []error
	var out []constGroup
	for _, g := range groups {
		kept := g.consts[:0:0]
		for _, c := range g.consts {
			if file, ok := author[c.name]; ok {
				notices = append(notices, fmt.Errorf("%s declares %s, the name of the generated constant for %s; generate leaves the constant out", file, c.name, c.origin))
				continue
			}
			kept = append(kept, c)
		}
		g.consts = kept
		out = appendGroup(out, g)
	}
	return out, notices
}

// constGroupsDecl renders groups as const blocks, or, with reexport set, as one block of
// re-exports of the models package's constants (`X = models.X`). "" when there are none.
func constGroupsDecl(groups []constGroup, reexport bool) string {
	var b strings.Builder
	if reexport {
		var names []string
		for _, g := range groups {
			for _, c := range g.consts {
				names = append(names, c.name)
			}
		}
		if len(names) == 0 {
			return ""
		}
		b.WriteString("// The models package's enum value constants, so handler code here uses them unqualified.\nconst (\n")
		for _, n := range names {
			fmt.Fprintf(&b, "\t%s = models.%s\n", n, n)
		}
		b.WriteString(")\n")
		return b.String()
	}
	for i, g := range groups {
		if i > 0 {
			b.WriteString("\n")
		}
		b.WriteString(g.comment)
		b.WriteString("\nconst (\n")
		for _, c := range g.consts {
			for _, d := range c.doc {
				b.WriteByte('\t')
				b.WriteString(d)
				b.WriteByte('\n')
			}
			if c.typ != "" {
				fmt.Fprintf(&b, "\t%s %s = %s\n", c.name, c.typ, strconv.Quote(c.value))
			} else {
				fmt.Fprintf(&b, "\t%s = %s\n", c.name, strconv.Quote(c.value))
			}
		}
		b.WriteString(")\n")
	}
	return b.String()
}

// constOrigins says what spec entry each constant comes from, for duplicateDecls.
func constOrigins(into map[string]string, groups ...[]constGroup) map[string]string {
	for _, gs := range groups {
		for _, g := range gs {
			for _, c := range g.consts {
				if _, ok := into[c.name]; !ok {
					into[c.name] = c.origin
				}
			}
		}
	}
	return into
}

// constants is the program's generated constants, merged: the enum values of its inputs and of
// its output, stdin and named schema types, then its names, less those [withoutClashes]
// leaves out, each with a notice. Computed once.
func (p *program) constants() ([]constGroup, error) {
	if !p.constsDone {
		p.constsDone = true
		_, out, err := buildOutputTypesConsts(p, "p")
		if err != nil {
			p.constsErr = err
			return nil, err
		}
		groups := mergeConstGroups(append(append(inputEnumGroups(p.spec), out...), nameGroups(p.spec, p.envPrefix)...))
		var clashes []constClash
		p.consts, clashes = withoutClashes(groups, generatedGoNames(p.spec, p.conf, func(*problem) {}))
		for _, c := range clashes {
			p.auditWarnings = append(p.auditWarnings, errors.New(c.msg))
		}
	}
	return p.consts, p.constsErr
}

// constDecls renders the program's constants for one generated file: the enum values when
// enums is set (as re-exports with reexport), and the names when names is set. In the cmd
// package (cmd), a constant an author file there already declares is left out, with a notice.
func (p *program) constDecls(enums, reexport, names, cmd bool) (string, error) {
	groups, err := p.constants()
	if err != nil {
		return "", err
	}
	var enumGroups, nameGroups []constGroup
	for _, g := range groups {
		if g.names {
			nameGroups = append(nameGroups, g)
		} else {
			enumGroups = append(enumGroups, g)
		}
	}
	if !enums {
		enumGroups = nil
	}
	if !names {
		nameGroups = nil
	}
	if cmd {
		var notices []error
		enumGroups, notices = withoutAuthorNames(enumGroups, p.authorNames)
		p.auditWarnings = append(p.auditWarnings, notices...)
		nameGroups, notices = withoutAuthorNames(nameGroups, p.authorNames)
		p.auditWarnings = append(p.auditWarnings, notices...)
	}
	decl := constGroupsDecl(enumGroups, reexport)
	if n := constGroupsDecl(nameGroups, false); n != "" {
		if decl != "" {
			decl += "\n"
		}
		decl += n
	}
	return decl, nil
}

// recordAuthorFile notes what a file of the author's in the cmd package declares: its
// top-level names, and whether it carries the package doc comment (a test file's doesn't count).
func (p *program) recordAuthorFile(name string, f *ast.File) {
	if f.Doc != nil && !strings.HasSuffix(name, "_test.go") {
		p.authorPackageDoc = true
	}
	rel := filepath.Join(p.layout.cmdDir, name)
	if r, err := filepath.Rel(p.module.root, rel); err == nil {
		rel = filepath.ToSlash(r)
	}
	for _, n := range topLevelNames(f) {
		if p.authorNames == nil {
			p.authorNames = map[string]string{}
		}
		if _, ok := p.authorNames[n]; !ok {
			p.authorNames[n] = rel
		}
	}
}

// generatedOrigins says what spec entry each generated top-level name comes from, for
// duplicateDecls: [generatedNames] plus each constant this file declares.
func (p *program) generatedOrigins(contract, tools bool) map[string]string {
	out := generatedNames(p.exitConstants(), contract, tools)
	if groups, err := p.constants(); err == nil {
		constOrigins(out, groups)
	}
	return out
}

// constClash is a constant left out: its name is another generated name, or two values make it.
type constClash struct {
	name, ptr, loc, msg string
}

// withoutClashes drops each constant whose name taken (the file's other generated names)
// holds, and every constant of a name two different values make (dry-run and dry_run), since
// none of them could be the one the name means. A spec that generated before keeps generating:
// the clash is a warning, and only the constant is lost.
func withoutClashes(groups []constGroup, taken map[string]string) ([]constGroup, []constClash) {
	values := map[string][]namedConst{}
	for _, g := range groups {
		for _, c := range g.consts {
			values[c.name] = append(values[c.name], c)
		}
	}
	var clashes []constClash
	var out []constGroup
	for _, g := range groups {
		kept := g.consts[:0:0]
		for _, c := range g.consts {
			switch other := values[c.name]; {
			case taken[c.name] != "":
				clashes = append(clashes, constClash{c.name, c.ptr, c.loc, fmt.Sprintf(
					"%s would get the constant %s, which %s already is; it is left out", c.origin, c.name, taken[c.name])})
			case fixedConstNames()[c.name] != "":
				clashes = append(clashes, constClash{c.name, c.ptr, c.loc, fmt.Sprintf(
					"%s would get the constant %s, which %s already is; it is left out", c.origin, c.name, fixedConstNames()[c.name])})
			case len(other) > 1:
				if other[0].value != c.value { // one warning per clash, at each value after the first
					clashes = append(clashes, constClash{c.name, c.ptr, c.loc, fmt.Sprintf(
						"%s and %s would both get the constant %s, so neither gets one; spell them alike or rename one to get them", c.origin, other[0].origin, c.name)})
				}
			default:
				kept = append(kept, c)
			}
		}
		g.consts = kept
		out = appendGroup(out, g)
	}
	return out, clashes
}

// fixedConstNames are the cmd file's own exported names that [generatedGoNames] doesn't list.
func fixedConstNames() map[string]string {
	return fixedConstNamesOnce.value(func() map[string]string {
		return map[string]string{
			"Usage": "the generated Usage function", "Contract": "the generated Contract variable",
			"ToolsMCP": "the generated ToolsMCP variable",
		}
	})
}

var fixedConstNamesOnce lazy[map[string]string]
