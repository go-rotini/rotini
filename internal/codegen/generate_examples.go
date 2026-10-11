package codegen

import (
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"text/template"

	"github.com/go-rotini/toml"
	"github.com/go-rotini/yaml"
)

// This file writes the config_example and env_example features: an example of each
// configuration file the spec declares, in the file's own format, and a .env.example listing
// every environment variable the program reads. Every key and variable is commented out, so
// copying an example changes nothing until a line is uncommented: lines starting with "# "
// ("// " in JSONC) are keys, and lines starting with "## " ("/// ") are notes about them.
// JSON can't hold comments, so a JSON file's example holds only the keys with a default.
//
// Both features also put their text in the generated cmd package, behind ConfigExample(name)
// and EnvExample(), so a command can print them.

// exampleAccessors is what the config_example and env_example features put in the cmd file.
type exampleAccessors struct {
	configOn bool
	configs  []exampleText // one per configuration file name, in declaration order
	envOn    bool
	env      exampleText
}

// exampleText is one example: its name (the config_files entry's) and either its text, or the
// embed path of the file holding it.
type exampleText struct {
	name, text, embed string
}

// configFormatOf returns the format a configuration file is read as.
func configFormatOf(cf ConfigurationFile) string {
	if dotenvFile(cf) {
		return "dotenv"
	}
	if cf.Format != "" {
		return cf.Format
	}
	name := cf.Path
	if cf.Discover != nil {
		name = cf.Discover.File
	}
	switch strings.ToLower(path.Ext(name)) {
	case ".toml":
		return "toml"
	case ".json":
		return "json"
	case ".jsonc":
		return "jsonc"
	}
	return "yaml"
}

// exampleExt is the extension of a configuration file's example: the file's own .yml when it
// uses one, else its format's.
func exampleExt(cf ConfigurationFile, format string) string {
	name := cf.Path
	if cf.Discover != nil {
		name = cf.Discover.File
	}
	switch {
	case format == "dotenv":
		return "env"
	case format == "yaml" && strings.EqualFold(path.Ext(name), ".yml"):
		return "yml"
	}
	return format
}

// exampleFiles are the configuration files that get an example: every declared file, once per
// command scope and name, except those read as environment variables (the .env example covers
// them).
func (p *program) exampleFiles() []scopedConfigFile {
	var out []scopedConfigFile
	seen := map[string]bool{}
	for _, cf := range p.configFiles {
		id := cf.Scope + "\x00" + cf.Name
		if cf.As == "env" || seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, cf)
	}
	return out
}

// exampleFileName names a configuration file's example: <name>.example.<ext> for a file the
// root declares, <page-name>.<name>.example.<ext> for one a sub-command declares.
func (p *program) exampleFileName(cf scopedConfigFile) string {
	name := cf.Name + ".example." + exampleExt(cf.ConfigurationFile, configFormatOf(cf.ConfigurationFile))
	if cf.Scope != "" && cf.Scope != p.rootName {
		name = manPageName(p.rootName, strings.Split(cf.Scope, "/")[1:]) + "." + name
	}
	return name
}

// renderConfigExample writes one example per configuration file under the feature's `file`
// directory. A key any file may hold is listed once, in the first such file's example; a key
// pinned to a file is listed in that file's.
func renderConfigExample(p *program, f *Feature, _ *agentProgram, _ *template.Template) (map[string][]byte, []error, error) {
	files := p.exampleFiles()
	if len(files) == 0 {
		p.examples.configOn = true
		return nil, []error{errors.New("config_example: the spec declares no config_files, so there is nothing to write")}, nil
	}
	base, err := underModule(p.module.root, "generate.features.config_example.file", cmp.Or(f.File, "examples/config/"))
	if err != nil {
		return nil, nil, err
	}
	keys := make([][]configKeyEntry, len(files))
	owner := map[string]int{} // an unpinned key → the file listing it
	for i, cf := range files {
		keys[i] = p.configFileKeys(cf)
		for _, e := range keys[i] {
			if _, ok := owner[e.key]; !ok && !pinnedKey(e) {
				owner[e.key] = i
			}
		}
	}
	out := map[string][]byte{}
	names := map[string]bool{}
	for i, cf := range files {
		var entries []exampleEntry
		elsewhere := ""
		for _, e := range keys[i] {
			if pinnedKey(e) || owner[e.key] == i {
				entries = append(entries, exampleEntryOf(e))
			} else if elsewhere == "" {
				elsewhere = p.exampleFileName(files[owner[e.key]])
			}
		}
		if cf.Profiles != nil {
			desc, _ := p.profilesSchema(cf, nil)["description"].(string)
			entries = append(entries, exampleEntry{
				path: []string{cf.Profiles.Under, cmp.Or(cf.Profiles.Default, "example")},
				doc:  desc, value: map[string]any{},
			})
		}
		name := p.exampleFileName(cf)
		text := p.configExampleText(cf, entries, elsewhere, filepath.Join(base, name))
		out[name] = []byte(text)
		if !names[cf.Name] {
			names[cf.Name] = true
			p.examples.configs = append(p.examples.configs, exampleText{name: cf.Name, text: text})
		}
	}
	p.examples.configOn = true
	if err := p.embedExamples(f, "config_example", p.examples.configs, func(e exampleText) string {
		return "config_example_" + e.name + "." + exampleExt(p.firstConfigFile(e.name), configFormatOf(p.firstConfigFile(e.name)))
	}); err != nil {
		return nil, nil, err
	}
	return out, nil, nil
}

// firstConfigFile returns the first configuration file declared with name.
func (p *program) firstConfigFile(name string) ConfigurationFile {
	for _, cf := range p.configFiles {
		if cf.Name == name {
			return cf.ConfigurationFile
		}
	}
	return ConfigurationFile{}
}

// pinnedKey reports whether a key is read from one named file only.
func pinnedKey(e configKeyEntry) bool { return e.input != nil && e.input.File != "" }

// renderEnvExample writes .env.example: every environment variable the program's visible
// inputs read, commented out.
func renderEnvExample(p *program, f *Feature, _ *agentProgram, _ *template.Template) (map[string][]byte, []error, error) {
	text := p.envExampleText()
	p.examples.envOn = true
	p.examples.env = exampleText{text: text}
	rel := ""
	if strings.HasSuffix(cmp.Or(f.File, ".env.example"), "/") {
		rel = ".env.example"
	}
	envs := []exampleText{p.examples.env}
	if err := p.embedExamples(f, "env_example", envs, func(exampleText) string { return "env_example.env" }); err != nil {
		return nil, nil, err
	}
	p.examples.env = envs[0]
	return map[string][]byte{rel: []byte(text)}, nil, nil
}

// embedExamples writes the examples to the feature's embed_dir when `embed` is on, and records
// each one's embed path in place of its text.
func (p *program) embedExamples(f *Feature, feature string, examples []exampleText, file func(exampleText) string) error {
	if !f.Embed {
		return nil
	}
	dir := filepath.Join(p.module.root, filepath.FromSlash(f.EmbedDir))
	rel, err := filepath.Rel(p.layout.cmdDir, dir)
	if err != nil || strings.HasPrefix(rel, "..") {
		return fmt.Errorf("generate.features.%s.embed_dir %q must resolve under the cmd package so //go:embed can reach it", feature, f.EmbedDir)
	}
	for i, e := range examples {
		name := file(e)
		if err := p.plan.write(filepath.Join(dir, name), []byte(e.text)); err != nil {
			return err
		}
		examples[i].embed = path.Join(filepath.ToSlash(rel), name)
		examples[i].text = ""
	}
	return nil
}

// ─── entries ─────────────────────────────────────────────────────────────────.

// exampleEntry is one key of a configuration file's example.
type exampleEntry struct {
	path       []string // the key, split at its dots
	doc        string   // the note above it: summary, type, values, default
	value      any      // the value written: the default, else a sample of the type
	hasDefault bool     // value is the declared default
}

// exampleEntryOf describes one key for the example.
func exampleEntryOf(e configKeyEntry) exampleEntry {
	in := e.input
	summary := e.summary
	if summary == "" {
		summary, _, _ = strings.Cut(e.description, "\n")
	}
	out := exampleEntry{path: strings.Split(e.key, "."), value: exampleSample(in)}
	var notes []string
	if in != nil {
		t := flagDisplayType(in)
		if t == "" && getSchemaType(in) == "bool" {
			t = "bool"
		}
		if t != "" {
			notes = append(notes, t)
		}
		if vals := enumVisible(in.Enum); len(vals) > 0 {
			notes = append(notes, "one of "+strings.Join(vals, ", "))
		}
		if d := schemaDefaultString(in); d != "" {
			notes = append(notes, "default "+d)
		}
		if in.Secret {
			notes = append(notes, "secret")
		} else if in.Default != nil {
			out.value, out.hasDefault = in.Default, true
		}
		if in.RelativeTo == "config" {
			notes = append(notes, "relative paths resolve against this file's directory")
		}
	}
	if e.deprecated != "" {
		notes = append(notes, "deprecated: "+e.deprecated)
	}
	out.doc = summary
	if len(notes) > 0 {
		out.doc = strings.TrimSpace(summary + " (" + strings.Join(notes, "; ") + ")")
	}
	return out
}

// exampleSample is a value of an input's type to write when it has no default (or a secret
// one): its first enum value, false, its minimum or 0, an empty list or map, or "".
func exampleSample(in *InputSchema) any {
	if in == nil {
		return ""
	}
	if vals := enumVisible(in.Enum); len(vals) > 0 {
		return vals[0]
	}
	t := strings.TrimPrefix(definitionType(in, nil), "*")
	switch {
	case t == "bool":
		return false
	case isListType(t):
		return []any{}
	case strings.HasPrefix(t, "map[") || in.Ref != "" || in.Type == "object":
		return map[string]any{}
	case strings.HasPrefix(t, "int") || strings.HasPrefix(t, "uint") || t == "count":
		if m := bound(in.Minimum); m != nil {
			return int64(*m)
		}
		return 0
	case strings.HasPrefix(t, "float"):
		if m := bound(in.Minimum); m != nil {
			return *m
		}
		return 0
	}
	return ""
}

// exampleNode is one level of an example's nested keys.
type exampleNode struct {
	name     string
	entry    *exampleEntry
	children []*exampleNode
}

// exampleTree nests entries by their dotted keys, keeping their order.
func exampleTree(entries []exampleEntry) *exampleNode {
	root := &exampleNode{}
	for i := range entries {
		n := root
		for depth, part := range entries[i].path {
			var next *exampleNode
			for _, c := range n.children {
				if c.name == part {
					next = c
				}
			}
			if next == nil {
				next = &exampleNode{name: part}
				n.children = append(n.children, next)
			}
			if depth == len(entries[i].path)-1 && next.entry == nil && len(next.children) == 0 {
				next.entry = &entries[i]
			}
			n = next
		}
	}
	return root
}

// ─── formats ─────────────────────────────────────────────────────────────────.

// configExampleText renders one configuration file's example in its format. target is where
// the example is written, for the relative path to the file's config schema.
func (p *program) configExampleText(cf scopedConfigFile, entries []exampleEntry, elsewhere, target string) string {
	format := configFormatOf(cf.ConfigurationFile)
	if format == "json" {
		return jsonExample(entries)
	}
	note, key := "## ", "# "
	if format == "jsonc" {
		note, key = "/// ", "// "
	}
	var b strings.Builder
	if schema := p.exampleSchemaPath(cf, target); schema != "" {
		switch format {
		case "yaml":
			fmt.Fprintf(&b, "# yaml-language-server: $schema=%s\n", schema)
		case "toml":
			fmt.Fprintf(&b, "#:schema %s\n", schema)
		}
	}
	fmt.Fprintf(&b, "%s%s: %s\n", note, cf.Name, configFileWhere(cf, p.rootName))
	fmt.Fprintf(&b, "%sEach line starting with %q is a key: remove the %q to set it.\n", note, key, key)
	if elsewhere != "" {
		fmt.Fprintf(&b, "%sKeys any configuration file may hold are listed once, in %s.\n", note, elsewhere)
	}
	if len(entries) == 0 {
		if format == "jsonc" {
			b.WriteString("{}\n")
		}
		return b.String()
	}
	b.WriteString("\n")
	tree := exampleTree(entries)
	switch format {
	case "toml":
		tomlExample(&b, tree, nil)
	case "jsonc":
		b.WriteString("{\n")
		jsoncExample(&b, tree, 1)
		b.WriteString("}\n")
	case "dotenv":
		for _, e := range entries {
			fmt.Fprintf(&b, "%s%s\n", note, e.doc)
			fmt.Fprintf(&b, "%s%s=%s\n", key, strings.Join(e.path, "."), dotenvValue(e.value))
		}
	default:
		yamlExample(&b, tree, 0)
	}
	return b.String()
}

// exampleSchemaPath is the path from the example to the file's generated config schema
// (generate.schemas.config), or "" when none is written for it.
func (p *program) exampleSchemaPath(cf scopedConfigFile, target string) string {
	g := p.conf.Generate
	if g == nil || g.Schemas == nil || g.Schemas.Config == nil || dotenvFile(cf.ConfigurationFile) {
		return ""
	}
	dir, err := underModule(p.module.root, "generate.schemas.config.dir", g.Schemas.Config.Dir)
	if err != nil {
		return ""
	}
	var scope []string
	if cf.Scope != "" {
		scope = strings.Split(cf.Scope, "/")[1:]
	}
	schema := filepath.Join(dir, manPageName(p.rootName, scope)+"."+cf.Name+configSchemaSuffix)
	rel, err := filepath.Rel(filepath.Dir(target), schema)
	if err != nil {
		return ""
	}
	return filepath.ToSlash(rel)
}

// yamlExample writes a YAML example's keys, nested by indentation.
func yamlExample(b *strings.Builder, n *exampleNode, depth int) {
	indent := strings.Repeat("  ", depth)
	for i, c := range n.children {
		if depth == 0 && i > 0 {
			b.WriteString("\n")
		}
		if c.entry != nil {
			fmt.Fprintf(b, "## %s%s\n", indent, c.entry.doc)
			fmt.Fprintf(b, "# %s%s: %s\n", indent, yamlKey(c.name), yamlValue(c.entry.value))
			continue
		}
		fmt.Fprintf(b, "# %s%s:\n", indent, yamlKey(c.name))
		yamlExample(b, c, depth+1)
	}
}

// yamlKey writes a key as YAML needs it.
func yamlKey(k string) string {
	if bareKey.MatchString(k) && !strings.HasPrefix(k, "-") {
		return k
	}
	return strconv.Quote(k)
}

// yamlValue writes a value inline: scalars as YAML, lists and maps in flow (JSON) form.
func yamlValue(v any) string {
	switch v.(type) {
	case []any, map[string]any:
		return jsonInline(v)
	}
	out, err := yaml.Marshal(v)
	if err != nil {
		return `""`
	}
	return strings.TrimSpace(string(out))
}

// jsonInline writes a value as one line of JSON. The values are decoded spec values, which
// always encode; "null" stands in should one not.
func jsonInline(v any) string {
	out, err := json.Marshal(v)
	if err != nil {
		return "null"
	}
	return string(out)
}

// bareKey matches a key YAML and TOML take unquoted.
var bareKey = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

// tomlExample writes a TOML example: the keys of each table, then its sub-tables.
func tomlExample(b *strings.Builder, n *exampleNode, at []string) {
	for _, c := range n.children {
		if c.entry != nil {
			fmt.Fprintf(b, "## %s\n", c.entry.doc)
			fmt.Fprintf(b, "# %s = %s\n", tomlKey(c.name), tomlValue(c.entry.value))
		}
	}
	for _, c := range n.children {
		if c.entry != nil {
			continue
		}
		table := append(slices.Clone(at), c.name)
		keys := make([]string, len(table))
		for i, k := range table {
			keys[i] = tomlKey(k)
		}
		if !strings.HasSuffix(b.String(), "\n\n") {
			b.WriteString("\n")
		}
		fmt.Fprintf(b, "# [%s]\n", strings.Join(keys, "."))
		tomlExample(b, c, table)
	}
}

// tomlKey writes a key as TOML needs it.
func tomlKey(k string) string {
	if bareKey.MatchString(k) {
		return k
	}
	return strconv.Quote(k)
}

// tomlValue writes a value inline as TOML.
func tomlValue(v any) string {
	if m, ok := v.(map[string]any); ok {
		if len(m) == 0 {
			return "{}"
		}
		var parts []string
		for _, k := range slices.Sorted(maps.Keys(m)) {
			parts = append(parts, tomlKey(k)+" = "+tomlValue(m[k]))
		}
		return "{ " + strings.Join(parts, ", ") + " }"
	}
	out, err := toml.Marshal(map[string]any{"v": v})
	if err != nil {
		return `""`
	}
	_, val, _ := strings.Cut(strings.TrimSpace(string(out)), "=")
	return strings.TrimSpace(val)
}

// jsoncExample writes a JSONC example's members, each commented out, with the commas a
// valid object needs once uncommented.
func jsoncExample(b *strings.Builder, n *exampleNode, depth int) {
	indent := strings.Repeat("  ", depth)
	for i, c := range n.children {
		comma := ","
		if i == len(n.children)-1 {
			comma = ""
		}
		if c.entry != nil {
			fmt.Fprintf(b, "///%s%s\n", indent, c.entry.doc)
			fmt.Fprintf(b, "// %s%q: %s%s\n", indent, c.name, jsonInline(c.entry.value), comma)
			continue
		}
		fmt.Fprintf(b, "// %s%q: {\n", indent, c.name)
		jsoncExample(b, c, depth+1)
		fmt.Fprintf(b, "// %s}%s\n", indent, comma)
	}
}

// jsonExample writes a JSON example: JSON has no comments, so it holds only the keys with a
// default, set, and never a secret.
func jsonExample(entries []exampleEntry) string {
	doc := map[string]any{}
	for _, e := range entries {
		if !e.hasDefault {
			continue
		}
		m := doc
		for _, part := range e.path[:len(e.path)-1] {
			next, ok := m[part].(map[string]any)
			if !ok {
				next = map[string]any{}
				m[part] = next
			}
			m = next
		}
		m[e.path[len(e.path)-1]] = e.value
	}
	return string(marshalJSONFile(doc))
}

// dotenvValue writes a value for a dotenv line: as is when it needs no quoting, else in
// double quotes.
func dotenvValue(v any) string {
	var s string
	switch x := v.(type) {
	case []any, map[string]any:
		s = jsonInline(x)
	default:
		s = defaultString(v)
	}
	if dotenvBare.MatchString(s) {
		return s
	}
	return strconv.Quote(s)
}

// dotenvBare matches a dotenv value that needs no quotes.
var dotenvBare = regexp.MustCompile(`^[A-Za-z0-9_./:@%+,-]*$`)

// ─── the .env example ─────────────────────────────────────────────────────────.

// envExampleText renders .env.example from the variables the environment help topic lists,
// leaving out the completion switches and XDG directories, which aren't the program's settings.
// A variable an input reads under several names is listed under its first, naming the others.
func (p *program) envExampleText() string {
	skip := map[string]bool{"XDG_CONFIG_HOME": true, "XDG_CONFIG_DIRS": true}
	for _, r := range completionEnvRows(p.conf) {
		skip[r.Var] = true
	}
	rows := environmentTopicRows(p)
	others := map[string][]string{}
	for _, r := range rows {
		if r.AliasOf != "" {
			others[r.AliasOf] = append(others[r.AliasOf], r.Var)
		}
	}
	var b strings.Builder
	fmt.Fprintf(&b, "## The environment variables %s reads.\n", p.rootDisplay)
	b.WriteString("## Each line starting with \"# \" is a variable: remove the \"# \" to set it.\n")
	for _, r := range rows {
		if skip[r.Var] || r.AliasOf != "" {
			continue
		}
		var notes []string
		if r.Type != "" {
			notes = append(notes, r.Type)
		}
		if len(r.Enum) > 0 {
			notes = append(notes, "one of "+strings.Join(r.Enum, ", "))
		}
		if r.Default != "" {
			notes = append(notes, "default "+r.Default)
		}
		if r.Secret {
			notes = append(notes, "secret")
		}
		if r.Deprecated != "" {
			notes = append(notes, "deprecated: "+deprecationNote(r.Deprecated, r.DeprecatedSince, r.RemovedIn))
		}
		if o := others[r.Var]; len(o) > 0 {
			notes = append(notes, "also read as "+strings.Join(o, ", "))
		}
		if len(r.Commands) > 1 {
			notes = append(notes, "read by "+strings.Join(r.Commands, ", "))
		}
		doc := r.Summary
		if len(notes) > 0 {
			doc = strings.TrimSpace(doc + " (" + strings.Join(notes, "; ") + ")")
		}
		name, value := r.Var, r.Default
		if r.Nesting != "" {
			name += r.Nesting + "<KEY>"
			value = ""
			doc += "; one variable per key"
		}
		if r.Secret {
			value = ""
		}
		fmt.Fprintf(&b, "\n## %s\n", doc)
		fmt.Fprintf(&b, "# %s=%s\n", name, dotenvValue(value))
	}
	return b.String()
}

// ─── the generated accessors ─────────────────────────────────────────────────.

// examplesDecl renders the cmd file's ConfigExample and EnvExample, each only when its feature
// is on.
func examplesDecl(e exampleAccessors) string {
	var b strings.Builder
	if e.configOn {
		b.WriteString("// ConfigExample returns the example of the configuration file the spec declares as name\n")
		b.WriteString("// (its config_files entry), with every key commented out, or an error when no file has\n// that name.\n")
		b.WriteString("func ConfigExample(name string) (string, error) {\n\tswitch name {\n")
		for _, c := range e.configs {
			fmt.Fprintf(&b, "\tcase %q:\n\t\treturn ConfigExample%s, nil\n", c.name, toPascalCase(c.name))
		}
		b.WriteString("\tdefault:\n\t\treturn \"\", fmt.Errorf(\"no config example for %q\", name)\n\t}\n}\n")
		for _, c := range e.configs {
			b.WriteString("\n")
			b.WriteString(exampleVar("ConfigExample"+toPascalCase(c.name), c))
		}
	}
	if e.envOn {
		if b.Len() > 0 {
			b.WriteString("\n")
		}
		b.WriteString("// EnvExample returns the program's .env example: every environment variable it reads,\n// commented out.\n")
		b.WriteString("func EnvExample() string { return EnvExampleText }\n\n")
		b.WriteString(exampleVar("EnvExampleText", e.env))
	}
	return b.String()
}

// exampleVar declares one example's variable: embedded from its file, or as a literal.
func exampleVar(name string, e exampleText) string {
	if e.embed != "" {
		return "//go:embed " + e.embed + "\nvar " + name + " string\n"
	}
	return "var " + name + " = " + goRawString(e.text) + "\n"
}

// examplesEmbed reports whether an example is embedded from a file.
func examplesEmbed(e exampleAccessors) bool {
	if e.envOn && e.env.embed != "" {
		return true
	}
	return slices.ContainsFunc(e.configs, func(c exampleText) bool { return c.embed != "" })
}
