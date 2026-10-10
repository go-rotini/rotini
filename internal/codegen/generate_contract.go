package codegen

import (
	"cmp"
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
)

// This file emits the machine-readable contract: one JSON Schema file per declared output
// (generate.schemas.output), one per configuration file (generate.schemas.config, see
// generate_configschema.go) and the contract document (generate.contract). Each is opt-in.
// The output schema files leave hidden commands out; the contract lists them, marked hidden.

// contractFormat names the contract document's format and version. Breaking changes bump
// the version.
const contractFormat = "rotini-contract/1"

// contractDoc is the contract document. Its format is described by schema-contract.json.
type contractDoc struct {
	Format        string                 `json:"format"`
	Name          string                 `json:"name"`
	Commands      []contractCommand      `json:"commands"`
	Definitions   map[string]any         `json:"definitions,omitempty"`
	Errors        json.RawMessage        `json:"errors"`
	Completion    *contractCompletion    `json:"completion,omitempty"`
	ResponseFiles *contractResponseFiles `json:"response_files,omitempty"`
	Multicall     *contractMulticall     `json:"multicall,omitempty"`
	Topics        []contractTopic        `json:"topics,omitempty"` // the root's help topics
}

// contractResponseFiles is how the program reads response files: a word starting with Prefix
// before the first "--" is replaced by the named file's lines.
type contractResponseFiles struct {
	Prefix string `json:"prefix"`
}

// contractCompletion is what the program's shell completion lets its users switch.
type contractCompletion struct {
	MessagesEnv     string `json:"messages_env,omitempty"`     // set to 0, false or off to hide completion messages
	DescriptionsEnv string `json:"descriptions_env,omitempty"` // set to 0, false or off to hide completion descriptions
}

// contractCommand is one command, or one plugin a command declares.
type contractCommand struct {
	Name                  string                   `json:"name"`
	Path                  []string                 `json:"path"`
	Summary               string                   `json:"summary,omitempty"`
	Description           string                   `json:"description,omitempty"`
	Aliases               []string                 `json:"aliases,omitempty"`
	Hidden                bool                     `json:"hidden,omitempty"` // left out of help; it or an ancestor declares hidden
	Deprecated            string                   `json:"deprecated,omitempty"`
	DeprecatedSince       string                   `json:"deprecated_since,omitempty"`
	RemovedIn             string                   `json:"removed_in,omitempty"`
	DeprecatedIdentifiers []string                 `json:"deprecated_identifiers,omitempty"`            // deprecated aliases
	IdentifiersRemovedIn  map[string]string        `json:"deprecated_identifiers_removed_in,omitempty"` // the release removing each deprecated alias
	Plugin                bool                     `json:"plugin,omitempty"`
	OptionsFirst          bool                     `json:"options_first,omitempty"` // flags stop at the command's first argument
	Passthrough           bool                     `json:"passthrough,omitempty"`   // every word after the command is an argument, as typed
	Arguments             []contractArgument       `json:"arguments,omitempty"`
	Flags                 []contractFlag           `json:"flags,omitempty"`
	FlagGroups            []contractFlagGroup      `json:"flag_groups,omitempty"`
	FlagDependencies      []contractFlagDependency `json:"flag_dependencies,omitempty"`
	Env                   []contractEnv            `json:"env,omitempty"`
	Config                []contractConfig         `json:"config,omitempty"`
	ConfigFiles           []contractConfigFile     `json:"config_files,omitempty"` // declared on this command; its descendants read them too
	Stdin                 *contractStdin           `json:"stdin,omitempty"`
	PluginDiscovery       *contractPluginDiscovery `json:"plugin_discovery,omitempty"`
	Parameters            map[string]any           `json:"parameters,omitempty"`
	Output                any                      `json:"output,omitempty"`
	Stream                bool                     `json:"stream,omitempty"` // output declares one item of a stream
	ExitStatus            []contractExit           `json:"exit_status,omitempty"`

	// Command facts from flag sets, hidden spellings and replacements; see contractParserFields.
	HiddenAliases []string `json:"hidden_aliases,omitempty"` // names that run the command but are never listed
	ReplacedBy    string   `json:"replaced_by,omitempty"`    // the command to use instead, as its path below the root
	Stability     string   `json:"stability,omitempty"`      // as declared: experimental or beta; unset is stable
}

type contractArgument struct {
	Name            string                       `json:"name"`
	Summary         string                       `json:"summary,omitempty"`
	Type            string                       `json:"type,omitempty"`
	Kind            string                       `json:"kind"`
	Required        bool                         `json:"required,omitempty"`
	Variadic        bool                         `json:"variadic,omitempty"`
	Passthrough     bool                         `json:"passthrough,omitempty"` // raw words start here: every later word is taken as typed
	Env             []string                     `json:"env,omitempty"`         // fallback variables, in lookup order
	ConfigKey       string                       `json:"config_key,omitempty"`  // fallback config key, only when the command reads config files
	Separator       string                       `json:"separator,omitempty"`
	Glob            bool                         `json:"glob,omitempty"` // patterns in its words are expanded into paths on Windows
	IgnoreCase      bool                         `json:"ignore_case,omitempty"`
	Layouts         []string                     `json:"layouts,omitempty"`
	Relative        string                       `json:"relative,omitempty"`
	VariableFile    string                       `json:"variable_file,omitempty"` // a variable naming a file that holds the fallback value
	Expand          []string                     `json:"expand,omitempty"`
	RelativeTo      string                       `json:"relative_to,omitempty"`
	Secret          bool                         `json:"secret,omitempty"`
	Hidden          bool                         `json:"hidden,omitempty"`
	EnumValues      map[string]contractEnumValue `json:"enum_values,omitempty"`
	Deprecated      string                       `json:"deprecated,omitempty"`
	DeprecatedSince string                       `json:"deprecated_since,omitempty"`
	RemovedIn       string                       `json:"removed_in,omitempty"`
	Schema          any                          `json:"schema"`
	ValuesFrom      string                       `json:"values_from,omitempty"` // the output path its enum lists the fields of
	Description     string                       `json:"description,omitempty"` // the input's longer text; the parameter description when set
	Stability       string                       `json:"stability,omitempty"`   // as declared: experimental or beta; unset is stable
}

type contractFlag struct {
	Name                  string                       `json:"name"`
	Identifiers           []string                     `json:"identifiers"`
	Negated               []string                     `json:"negated,omitempty"` // the --no- forms of a negatable flag
	Summary               string                       `json:"summary,omitempty"`
	Type                  string                       `json:"type,omitempty"`
	Kind                  string                       `json:"kind"`
	Required              bool                         `json:"required,omitempty"`
	Cascading             bool                         `json:"cascading,omitempty"`
	ShortCircuit          bool                         `json:"short_circuit,omitempty"` // set on the command line, it waives the command's requirements
	Role                  string                       `json:"role,omitempty"`          // what the flag means to a program driving the CLI (force)
	Inherited             bool                         `json:"inherited,omitempty"`
	Env                   []string                     `json:"env,omitempty"`           // fallback variables, in lookup order
	ConfigKey             string                       `json:"config_key,omitempty"`    // fallback config key, only when the command reads config files
	VariableFile          string                       `json:"variable_file,omitempty"` // a variable naming a file that holds the fallback value
	ConfigSource          string                       `json:"config_source,omitempty"`
	Separator             string                       `json:"separator,omitempty"`
	From                  []string                     `json:"from,omitempty"`
	ImplicitValue         any                          `json:"implicit_value,omitempty"`
	IgnoreCase            bool                         `json:"ignore_case,omitempty"`
	DottedKeys            bool                         `json:"dotted_keys,omitempty"`
	Layouts               []string                     `json:"layouts,omitempty"`
	Relative              string                       `json:"relative,omitempty"`
	Expand                []string                     `json:"expand,omitempty"`
	RelativeTo            string                       `json:"relative_to,omitempty"`
	Secret                bool                         `json:"secret,omitempty"`
	Hidden                bool                         `json:"hidden,omitempty"`
	Deprecated            string                       `json:"deprecated,omitempty"`
	DeprecatedSince       string                       `json:"deprecated_since,omitempty"`
	RemovedIn             string                       `json:"removed_in,omitempty"`
	DeprecatedIdentifiers []string                     `json:"deprecated_identifiers,omitempty"`
	IdentifiersRemovedIn  map[string]string            `json:"deprecated_identifiers_removed_in,omitempty"`
	EnumValues            map[string]contractEnumValue `json:"enum_values,omitempty"`
	Schema                any                          `json:"schema"`
	Repeatable            *bool                        `json:"repeatable,omitempty"`  // set only to false: a repeat is an error
	Description           string                       `json:"description,omitempty"` // the input's longer text; the parameter description when set
	Stability             string                       `json:"stability,omitempty"`   // as declared: experimental or beta; unset is stable
	ValuesFrom            string                       `json:"values_from,omitempty"`

	// Flag facts from flag sets, hidden spellings and replacements; see contractParserFields.
	HiddenIdentifiers []string `json:"hidden_identifiers,omitempty"` // identifiers accepted but never listed
	ReplacedBy        string   `json:"replaced_by,omitempty"`        // the identifier of the flag to use instead
	FlagSet           string   `json:"flag_set,omitempty"`           // the flag set it comes from
}

type contractEnv struct {
	Name            string                       `json:"name"`
	Variables       []string                     `json:"variables"`
	VariableFile    string                       `json:"variable_file,omitempty"` // a variable naming a file that holds the value
	Summary         string                       `json:"summary,omitempty"`
	Type            string                       `json:"type,omitempty"`
	Kind            string                       `json:"kind"`
	Required        bool                         `json:"required,omitempty"`
	Secret          bool                         `json:"secret,omitempty"`
	ConfigSource    string                       `json:"config_source,omitempty"`
	Nesting         string                       `json:"nesting,omitempty"`
	Separator       string                       `json:"separator,omitempty"`
	IgnoreCase      bool                         `json:"ignore_case,omitempty"`
	Layouts         []string                     `json:"layouts,omitempty"`
	Relative        string                       `json:"relative,omitempty"`
	Expand          []string                     `json:"expand,omitempty"`
	RelativeTo      string                       `json:"relative_to,omitempty"`
	Hidden          bool                         `json:"hidden,omitempty"`
	Deprecated      string                       `json:"deprecated,omitempty"`
	DeprecatedSince string                       `json:"deprecated_since,omitempty"`
	RemovedIn       string                       `json:"removed_in,omitempty"`
	EnumValues      map[string]contractEnumValue `json:"enum_values,omitempty"`
	Schema          any                          `json:"schema"`
	Description     string                       `json:"description,omitempty"` // the input's longer text; the parameter description when set
	Stability       string                       `json:"stability,omitempty"`   // as declared: experimental or beta; unset is stable
}

type contractConfig struct {
	Name            string                       `json:"name"`
	Key             string                       `json:"key"`
	File            string                       `json:"file,omitempty"`
	Summary         string                       `json:"summary,omitempty"`
	Type            string                       `json:"type,omitempty"`
	Kind            string                       `json:"kind"`
	Required        bool                         `json:"required,omitempty"`
	IgnoreCase      bool                         `json:"ignore_case,omitempty"`
	Layouts         []string                     `json:"layouts,omitempty"`
	Relative        string                       `json:"relative,omitempty"`
	Expand          []string                     `json:"expand,omitempty"`
	RelativeTo      string                       `json:"relative_to,omitempty"`
	Secret          bool                         `json:"secret,omitempty"`
	Hidden          bool                         `json:"hidden,omitempty"`
	Deprecated      string                       `json:"deprecated,omitempty"`
	DeprecatedSince string                       `json:"deprecated_since,omitempty"`
	RemovedIn       string                       `json:"removed_in,omitempty"`
	EnumValues      map[string]contractEnumValue `json:"enum_values,omitempty"`
	Schema          any                          `json:"schema"`
	Description     string                       `json:"description,omitempty"` // the input's longer text; the parameter description when set
	Stability       string                       `json:"stability,omitempty"`   // as declared: experimental or beta; unset is stable
}

// contractFlagGroup is a flag_groups entry: a rule over a set of the command's flags.
type contractFlagGroup struct {
	Kind  string   `json:"kind"`
	Flags []string `json:"flags"`
}

// contractFlagDependency is a flag_dependencies entry: when When is set (to one of Equals, when
// given) and none of Unless is, every flag in Requires must be set and none in Forbids may be.
type contractFlagDependency struct {
	When     string   `json:"when,omitempty"`
	Requires []string `json:"requires,omitempty"`
	Equals   []any    `json:"equals,omitempty"`
	Unless   []string `json:"unless,omitempty"`
	Forbids  []string `json:"forbids,omitempty"`
}

// contractConfigFile is a configuration file a command declares: a fixed path, or how it is
// discovered.
type contractConfigFile struct {
	Name     string                  `json:"name"`
	Format   string                  `json:"format,omitempty"`
	Path     string                  `json:"path,omitempty"`
	As       string                  `json:"as,omitempty"` // "env": the file supplies environment variables
	Discover *contractConfigDiscover `json:"discover,omitempty"`
}

type contractConfigDiscover struct {
	Strategy string `json:"strategy"`
	App      string `json:"app,omitempty"`
	File     string `json:"file"`
}

// contractPluginDiscovery is how a command finds plugins: any executable named Prefix<word>
// runs as `<command> <word>`.
type contractPluginDiscovery struct {
	Prefix string `json:"prefix"`
	Hidden bool   `json:"hidden,omitempty"`
}

// contractEnumValue is what one enum value of an input declares beyond its spelling.
type contractEnumValue struct {
	Summary           string   `json:"summary,omitempty"`
	Aliases           []string `json:"aliases,omitempty"`
	DeprecatedAliases []string `json:"deprecated_aliases,omitempty"`
	Hidden            bool     `json:"hidden,omitempty"`
	Deprecated        string   `json:"deprecated,omitempty"`
	DeprecatedSince   string   `json:"deprecated_since,omitempty"`
	RemovedIn         string   `json:"removed_in,omitempty"`
	ReplacedBy        string   `json:"replaced_by,omitempty"`
}

// contractEnumValues maps each value of an input's enum to what it declares beyond its
// spelling, or nil when no value declares more.
func contractEnumValues(schema *InputSchema) map[string]contractEnumValue {
	if schema == nil || !enumDetailed(schema.Enum) {
		return nil
	}
	out := map[string]contractEnumValue{}
	for _, v := range enumValues(schema.Enum) {
		out[v.Value] = contractEnumValue{
			Summary: v.Summary, Aliases: v.Aliases, DeprecatedAliases: v.DeprecatedAliases, Hidden: v.Hidden,
			Deprecated: v.Deprecated, DeprecatedSince: v.DeprecatedSince, RemovedIn: v.RemovedIn, ReplacedBy: v.ReplacedBy,
		}
	}
	return out
}

type contractStdin struct {
	Format         string `json:"format,omitempty"`
	Type           string `json:"type,omitempty"`
	Required       bool   `json:"required,omitempty"`
	Schema         any    `json:"schema,omitempty"`
	Stream         bool   `json:"stream,omitempty"`          // read one item at a time (lines or jsonl)
	Separator      string `json:"separator,omitempty"`       // "nul": lines are separated by NUL bytes
	UnlessArgument string `json:"unless_argument,omitempty"` // stdin is read only when this argument gets no file, or "-"
}

type contractExit struct {
	Code      int    `json:"code"`
	Name      string `json:"name,omitempty"`
	Summary   string `json:"summary,omitempty"`
	Retryable bool   `json:"retryable,omitempty"`
	Output    any    `json:"output,omitempty"`
	DocsURL   string `json:"docs_url,omitempty"`
}

// contractNode is one command as described by the contract and output schema files.
type contractNode struct {
	path          []string // names below the root; empty for the root
	help          cmdHelp
	inputs        *Inputs
	output        *Schema
	stream        bool
	aliases       []string
	plugins       []PluginSpec
	inherited     []FlagInput // cascading flags declared by ancestors, nearest first
	deprecate     string
	deprecatedIDs []string
	lifecycle     lifecycle
	hidden        bool // it or an ancestor is hidden
	passthrough   bool
	discovery     *PluginDiscovery
	pluginHost    string       // the program its discovered plugins are named after; "" = this one
	scope         *schemaScope // the named schemas its $refs name; nil = the root spec's

	hiddenAliases []string    // names that run it but are never listed
	replacedBy    replacement // the command to use instead of a deprecated one
	flagSets      []string    // the flag set each of inputs.Flags came from; nil without sets
}

// contractNodes lists every command depth-first, root first. A hidden command and its
// subtree are marked hidden.
func (p *program) contractNodes() []contractNode {
	cascading := func(in *Inputs) []FlagInput {
		var out []FlagInput
		if in != nil {
			for _, f := range in.Flags {
				if f.Cascading {
					out = append(out, f)
				}
			}
		}
		return out
	}
	nodes := []contractNode{{
		path: []string{}, help: p.rootHelp, inputs: p.rootInputs, output: p.rootOutput, stream: p.rootStream,
		plugins: p.rootPlugins, passthrough: p.rootPassthrough, discovery: p.rootDiscovery,
		flagSets: p.rootFlagSets,
	}}
	var walk func(rs []rnode, path []string, inherited []FlagInput, hidden bool)
	walk = func(rs []rnode, path []string, inherited []FlagInput, hidden bool) {
		for _, n := range rs {
			names := append(slices.Clone(path), n.name)
			nodes = append(nodes, contractNode{
				path: names, help: n.help, inputs: n.inputs, output: n.output, stream: n.stream,
				aliases: n.aliases, plugins: n.plugins, inherited: inherited, deprecate: n.deprecated,
				deprecatedIDs: n.deprecatedIdentifiers, lifecycle: n.lifecycle, hidden: hidden || n.hidden,
				passthrough: n.passthrough, discovery: n.discovery, pluginHost: n.pluginHost, scope: n.scope,
				hiddenAliases: n.hiddenAliases, replacedBy: n.replacedBy, flagSets: n.flagSets,
			})
			walk(n.children, names, append(cascading(n.inputs), inherited...), hidden || n.hidden)
		}
	}
	walk(p.tree, nil, cascading(p.rootInputs), false)
	return nodes
}

// emitContract writes the output schema files and the contract document, each when the conf
// asks for it.
func (p *program) emitContract() error {
	g := p.conf.Generate
	if g == nil {
		return nil
	}
	nodes := (*program).contractNodes
	if g.Schemas != nil && g.Schemas.Output != nil {
		if err := p.writeOutputSchemas(g.Schemas.Output.Dir, nodes(p)); err != nil {
			return err
		}
	}
	if g.Schemas != nil && g.Schemas.Config != nil {
		if err := p.writeConfigSchemas(g.Schemas.Config.Dir); err != nil {
			return err
		}
	}
	c := g.Contract
	if c == nil || (c.File == "" && !c.Go) {
		return nil
	}
	doc, err := p.contract(nodes(p))
	if err != nil {
		return err
	}
	if c.Go {
		p.contractGo = doc
	}
	if c.File == "" {
		return nil
	}
	abs, err := underModule(p.module.root, "generate.contract.file", c.File)
	if err != nil {
		return err
	}
	return p.plan.write(abs, doc)
}

// underModule resolves a module-root-relative path from the conf, refusing one that is absolute
// or escapes the module.
func underModule(root, key, rel string) (string, error) {
	p := filepath.FromSlash(rel)
	if filepath.IsAbs(p) {
		return "", fmt.Errorf("%s %q must be module-root-relative, not absolute", key, rel)
	}
	abs := filepath.Join(root, p)
	if r, err := filepath.Rel(root, abs); err != nil || r == ".." || strings.HasPrefix(r, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("%s %q must resolve under the module root", key, rel)
	}
	return abs, nil
}

// outputSchemaSuffix is the suffix of every output schema file rotini writes. Pruning only
// removes files with this suffix.
const outputSchemaSuffix = ".output.json"

// writeOutputSchemas writes one JSON Schema per declared output (command and per-exit-status)
// into dir and, unless pruning is skipped, removes stale ones.
func (p *program) writeOutputSchemas(dir string, nodes []contractNode) error {
	absDir, err := underModule(p.module.root, "generate.schemas.output.dir", dir)
	if err != nil {
		return err
	}
	files := map[string][]byte{}
	for _, n := range nodes {
		if n.hidden {
			continue
		}
		page := manPageName(p.rootName, n.path)
		invocation := strings.Join(append([]string{p.rootName}, n.path...), " ")
		schemas := p.schemasOf(n.scope)
		if n.output != nil {
			title := invocation + " output"
			if n.stream {
				title += ", one item of a stream"
			}
			if b, ok := outputSchemaFile(title, "", n.output, schemas); ok {
				files[page+outputSchemaSuffix] = b
			}
		}
		for _, e := range n.help.ExitStatus {
			if e.Output == nil {
				continue
			}
			title := invocation + " output on exit " + strconv.Itoa(e.Code)
			if b, ok := outputSchemaFile(title, e.Summary, e.Output, schemas); ok {
				files[page+".exit-"+strconv.Itoa(e.Code)+outputSchemaSuffix] = b
			}
		}
	}
	for _, name := range slices.Sorted(maps.Keys(files)) {
		if err := p.plan.write(filepath.Join(absDir, name), files[name]); err != nil {
			return err
		}
	}
	entries, err := os.ReadDir(absDir)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("read the output schemas directory: %w", err)
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), outputSchemaSuffix) || files[e.Name()] != nil || p.skipPrune {
			continue
		}
		if err := p.plan.remove(filepath.Join(absDir, e.Name())); err != nil {
			return fmt.Errorf("remove a stale output schema: %w", err)
		}
		p.pruned = append(p.pruned, filepath.Join(absDir, e.Name()))
	}
	return nil
}

// outputSchemaFile renders one output's JSON Schema file (see outputSchemaDoc) with the given
// title and description. ok is false when the shape references an undeclared schema.
func outputSchemaFile(title, description string, shape *Schema, schemas map[string]Schema) ([]byte, bool) {
	body, ok := outputSchemaDoc(shape, schemas)
	if !ok {
		return nil, false
	}
	body["title"] = title
	if description != "" {
		body["description"] = description
	}
	return marshalJSONFile(body), true
}

// outputSchemaDoc renders an output shape as a self-contained standard JSON Schema, with the
// named schemas it reaches as definitions. ok is false when it references a schema this
// document does not declare.
func outputSchemaDoc(shape *Schema, schemas map[string]Schema) (map[string]any, bool) {
	body, ok := standardSchema(schemaToDoc(*shape)).(map[string]any)
	if !ok {
		return nil, false
	}
	defs, ok := reachableDefinitions(body, schemas)
	if !ok {
		return nil, false
	}
	body["$schema"] = "http://json-schema.org/draft-07/schema#"
	if len(defs) > 0 {
		body["definitions"] = defs
	}
	return body, true
}

// outputDefLiteral renders a command's `Output: &rotini.OutputDef{Type, Schema, Stream}` field,
// or "" when it declares no output. typeName is its generated <Prefix>Output type; Schema is
// omitted when the shape cannot be rendered self-contained.
func outputDefLiteral(typeName string, shape *Schema, schemas map[string]Schema, stream bool) string {
	if shape == nil {
		return ""
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Output: &%s.OutputDef{Type: reflect.TypeFor[%s]()", rotiniPkgName, typeName)
	if doc, ok := outputSchemaDoc(shape, schemas); ok {
		if raw, err := json.Marshal(doc); err == nil {
			b.WriteString(", Schema: ")
			b.WriteString(goRawString(string(raw)))
		}
	}
	if stream {
		b.WriteString(", Stream: true")
	}
	b.WriteString("},\n")
	return b.String()
}

// reachableDefinitions returns, as standard JSON Schema, every named schema doc references,
// directly or through another. ok is false when a reference names no declared schema.
func reachableDefinitions(doc any, schemas map[string]Schema) (map[string]any, bool) {
	defs := map[string]any{}
	queue := schemaRefs(doc)
	for len(queue) > 0 {
		name := queue[0]
		queue = queue[1:]
		if _, done := defs[name]; done {
			continue
		}
		s, ok := schemas[name]
		if !ok {
			return nil, false
		}
		def := standardSchema(schemaToDoc(s))
		defs[name] = def
		queue = append(queue, schemaRefs(def)...)
	}
	return defs, true
}

// schemaRefs lists the names of the definitions a schema document references.
func schemaRefs(v any) []string {
	var out []string
	switch t := v.(type) {
	case map[string]any:
		if ref, ok := t["$ref"].(string); ok {
			if name, ok := strings.CutPrefix(ref, "#/definitions/"); ok {
				out = append(out, name)
			}
		}
		for _, k := range slices.Sorted(maps.Keys(t)) {
			out = append(out, schemaRefs(t[k])...)
		}
	case []any:
		for _, e := range t {
			out = append(out, schemaRefs(e)...)
		}
	}
	return out
}

// contract renders the contract document.
func (p *program) contract(nodes []contractNode) ([]byte, error) {
	doc := contractDoc{Format: contractFormat, Name: p.rootName, Errors: errorSchemaBytes}
	if envs := completionScriptEnvs(p.conf); envs != (completionEnvs{}) {
		doc.Completion = &contractCompletion{MessagesEnv: envs.messages, DescriptionsEnv: envs.descriptions}
	}
	if rf := p.responseFiles(); rf != nil {
		doc.ResponseFiles = &contractResponseFiles{Prefix: rf.Prefix}
	}
	if on, prefix, complete := p.multicall(); on {
		doc.Multicall = &contractMulticall{Prefix: prefix, Complete: complete}
	}
	doc.Topics = contractTopics(p.rootHelp.Topics)
	defs := p.contractDefinitions(nodes)
	if len(defs.pool) > 0 {
		doc.Definitions = defs.pool
	}
	for _, n := range nodes {
		doc.Commands = append(doc.Commands, p.contractCommand(n, defs))
		for _, pl := range n.plugins {
			doc.Commands = append(doc.Commands, contractCommand{
				Name:    strings.Join(append(append([]string{p.rootName}, n.path...), pl.Name), " "),
				Path:    append(slices.Clone(n.path), pl.Name),
				Summary: pl.Summary,
				Aliases: pl.Aliases,
				Hidden:  n.hidden,
				Plugin:  true,
			})
		}
	}
	b, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("marshal the contract document: %w", err)
	}
	return append(b, '\n'), nil
}

// contractCommand describes one command. Hidden inputs are listed, marked hidden, but left
// out of parameters, which describes what a caller may pass.
func (p *program) contractCommand(n contractNode, defs *contractDefs) contractCommand {
	c := contractCommand{
		Name:                  strings.Join(append([]string{p.rootName}, n.path...), " "),
		Path:                  n.path,
		Summary:               n.help.Summary,
		Description:           n.help.Description,
		Aliases:               n.aliases,
		Hidden:                n.hidden,
		Deprecated:            n.deprecate,
		DeprecatedSince:       n.lifecycle.since,
		RemovedIn:             n.lifecycle.removedIn,
		DeprecatedIdentifiers: n.deprecatedIDs,
		IdentifiersRemovedIn:  n.lifecycle.removedIDs,
		Passthrough:           n.passthrough,
		Stream:                n.stream,
		Stability:             n.help.Stability,
	}
	in := n.inputs
	if in == nil {
		in = &Inputs{}
	}
	p.contractCommandRules(&c, n, in)
	schemas := p.schemasOf(n.scope)
	schemaOf := func(s *InputSchema) any { return defs.rename(n.scope, inputJSONSchema(s)) }
	params := map[string]any{}
	var required []string
	seen := map[string]bool{}
	// A parameter's description is the input's description, else its summary, as a command's prose is.
	param := func(name, summary string, schema any, req, hidden bool) {
		if seen[name] {
			return // the nearest declaration wins, as on the command line
		}
		seen[name] = true
		if hidden {
			return
		}
		s, _ := schema.(map[string]any)
		s = maps.Clone(s)
		if summary != "" {
			s["description"] = summary
		}
		params[name] = s
		if req {
			required = append(required, name)
		}
	}
	readsConfig := p.readsConfig(n.path)
	for _, a := range in.Arguments {
		req := a.Schema != nil && a.Schema.Required
		f := contractFactsOf(a.Schema, schemas)
		arg := contractArgument{
			Name: a.Name, Summary: a.Summary, Type: f.typ, Kind: f.kind, Required: req, Variadic: isVariadicSchema(a.Schema),
			Passthrough: a.Passthrough, Separator: f.separator, Glob: a.Schema != nil && a.Schema.Glob,
			IgnoreCase: f.ignoreCase, Layouts: f.layouts, Relative: f.relative,
			VariableFile: f.variableFile, Expand: f.expand, RelativeTo: f.relativeTo,
			Secret: f.secret, Hidden: a.Hidden, EnumValues: contractEnumValues(a.Schema),
			Deprecated: a.Deprecated, DeprecatedSince: a.DeprecatedSince, RemovedIn: a.RemovedIn,
			Schema: schemaOf(a.Schema), Description: a.Description, Stability: a.Stability,
		}
		arg.Env, arg.ConfigKey = argumentFallback(a, p.envPrefix, readsConfig)
		arg.ValuesFrom = valuesFrom(a.Schema)
		c.Arguments = append(c.Arguments, arg)
		param(a.Name, cmp.Or(a.Description, a.Summary), arg.Schema, req, a.Hidden)
	}
	declared := map[string]bool{}
	for _, f := range slices.Concat(in.Flags, n.inherited) {
		for _, id := range flagIdentifiers(f) {
			declared[id] = true
		}
	}
	flag := func(f FlagInput, inherited bool) {
		req := f.Schema != nil && f.Schema.Required
		facts := contractFactsOf(f.Schema, schemas)
		cf := contractFlag{
			Name: f.Name, Identifiers: flagIdentifiers(f), Negated: negatedFlagIdentifiers(f, declared), Summary: f.Summary,
			Type: facts.typ, Kind: facts.kind, Required: req,
			Cascading: f.Cascading && !inherited, Inherited: inherited, ShortCircuit: f.ShortCircuit, Role: f.Role,
			ConfigSource: facts.configSource, Separator: facts.separator, From: facts.from, ImplicitValue: facts.implicitValue,
			IgnoreCase: facts.ignoreCase, DottedKeys: facts.dottedKeys, Layouts: facts.layouts, Relative: facts.relative,
			Secret: facts.secret, VariableFile: facts.variableFile, Expand: facts.expand, RelativeTo: facts.relativeTo,
			Hidden: f.Hidden, Deprecated: f.Deprecated, DeprecatedSince: f.DeprecatedSince, RemovedIn: f.RemovedIn,
			DeprecatedIdentifiers: f.DeprecatedIdentifiers, IdentifiersRemovedIn: f.DeprecatedIdentifiersRemovedIn,
			EnumValues: contractEnumValues(f.Schema), Schema: schemaOf(f.Schema),
			ValuesFrom:  valuesFrom(f.Schema),
			Description: f.Description, Stability: f.Stability,
		}
		if f.Schema != nil && f.Schema.Repeatable != nil && !*f.Schema.Repeatable {
			cf.Repeatable = f.Schema.Repeatable
		}
		// The same names help shows, from the same functions as the generated env tags.
		row := withConfigKeys([]templateDocFlagRow{flagRow(f, p.envPrefix)}, readsConfig)[0]
		cf.Env, cf.ConfigKey = row.Env, row.ConfigKey
		c.Flags = append(c.Flags, cf)
		param(f.Name, cmp.Or(f.Description, f.Summary), cf.Schema, req, f.Hidden)
	}
	for _, f := range in.Flags {
		flag(f, false)
	}
	for _, f := range n.inherited {
		flag(f, true)
	}
	p.contractSources(&c, in, n.scope, defs)
	c.Parameters = map[string]any{"type": "object", "properties": params, "additionalProperties": false}
	if len(required) > 0 {
		c.Parameters["required"] = required
	}
	c.Parameters = defs.selfContained(c.Parameters)
	if n.output != nil {
		c.Output = defs.schemaDoc(n.scope, *n.output)
	}
	for _, e := range n.help.ExitStatus {
		x := contractExit{Code: e.Code, Name: e.Name, Summary: e.Summary, Retryable: e.Retryable, DocsURL: e.DocsUrl}
		if e.Output != nil {
			x.Output = defs.schemaDoc(n.scope, *e.Output)
		}
		c.ExitStatus = append(c.ExitStatus, x)
	}
	p.contractParserFields(&c, n, in)
	return c
}

// contractCommandRules describes how a command reads its command line and where its values
// come from, beyond its inputs: options_first, flag groups and dependencies, the config files
// it declares, and plugin discovery.
func (p *program) contractCommandRules(c *contractCommand, n contractNode, in *Inputs) {
	c.OptionsFirst = in.OptionsFirst
	for _, g := range in.FlagGroups {
		c.FlagGroups = append(c.FlagGroups, contractFlagGroup{Kind: g.Kind, Flags: g.Flags})
	}
	for _, d := range in.FlagDependencies {
		c.FlagDependencies = append(c.FlagDependencies, contractFlagDependency{When: d.When, Requires: d.Requires, Equals: d.Equals, Unless: d.Unless, Forbids: d.Forbids})
	}
	for _, cf := range in.ConfigFiles {
		file := contractConfigFile{Name: cf.Name, Format: cf.Format, Path: cf.Path, As: cf.As}
		if d := cf.Discover; d != nil {
			file.Discover = &contractConfigDiscover{Strategy: d.Strategy, App: d.App, File: d.File}
		}
		c.ConfigFiles = append(c.ConfigFiles, file)
	}
	if n.discovery != nil {
		c.PluginDiscovery = &contractPluginDiscovery{Prefix: n.discovery.Prefix, Hidden: n.discovery.Hidden}
		if c.PluginDiscovery.Prefix == "" {
			c.PluginDiscovery.Prefix = cmp.Or(n.pluginHost, p.rootName) + "-"
		}
	}
}

// contractSources describes a command's other input sources: environment variables,
// configuration keys and stdin.
func (p *program) contractSources(c *contractCommand, in *Inputs, scope *schemaScope, defs *contractDefs) {
	schemas := p.schemasOf(scope)
	for _, e := range in.Env {
		f := contractFactsOf(e.Schema, schemas)
		c.Env = append(c.Env, contractEnv{
			Name: e.Name, Variables: strings.Split(envVarName(e, p.envPrefix), ","), Summary: e.Summary,
			Type: f.typ, Kind: f.kind, Required: e.Schema != nil && e.Schema.Required, Secret: f.secret,
			VariableFile: f.variableFile, ConfigSource: f.configSource, Nesting: f.nesting, Separator: f.separator,
			IgnoreCase: f.ignoreCase, Layouts: f.layouts, Relative: f.relative, Expand: f.expand, RelativeTo: f.relativeTo,
			Hidden: e.Hidden, Deprecated: e.Deprecated, DeprecatedSince: e.DeprecatedSince, RemovedIn: e.RemovedIn,
			EnumValues: contractEnumValues(e.Schema), Schema: defs.rename(scope, inputJSONSchema(e.Schema)),
			Description: e.Description, Stability: e.Stability,
		})
	}
	for _, cfg := range in.Config {
		key, file := cfg.Name, ""
		if cfg.Schema != nil {
			if cfg.Schema.Key != "" {
				key = cfg.Schema.Key
			}
			file = cfg.Schema.File
		}
		f := contractFactsOf(cfg.Schema, schemas)
		c.Config = append(c.Config, contractConfig{
			Name: cfg.Name, Key: key, File: file, Summary: cfg.Summary, Type: f.typ, Kind: f.kind,
			Required: cfg.Schema != nil && cfg.Schema.Required, IgnoreCase: f.ignoreCase, Layouts: f.layouts, Relative: f.relative,
			Expand: f.expand, RelativeTo: f.relativeTo,
			Secret: f.secret, Hidden: cfg.Hidden, Deprecated: cfg.Deprecated, DeprecatedSince: cfg.DeprecatedSince,
			RemovedIn: cfg.RemovedIn, EnumValues: contractEnumValues(cfg.Schema),
			Schema:      defs.rename(scope, inputJSONSchema(cfg.Schema)),
			Description: cfg.Description, Stability: cfg.Stability,
		})
	}
	if in.Stdin != nil {
		st := &contractStdin{
			Format: in.Stdin.Format, Stream: in.Stdin.Stream, Separator: in.Stdin.Separator, UnlessArgument: in.Stdin.UnlessArgument,
		}
		if s := in.Stdin.Schema; s != nil {
			st.Required = s.Required
			st.Type = contractFactsOf(s, schemas).typ
			st.Schema = defs.schemaDoc(scope, Schema{BaseSchema: s.BaseSchema})
		}
		c.Stdin = st
	}
}

// inputJSONSchema describes an input's value as standard JSON Schema: its type, constraints,
// enum, and default. A `secret` input's default is omitted. A list's or map's per-value
// constraints sit on its items, where the runtime applies them; only the item counts stay on
// the list. A time input claims a `format` only when its layouts are the one that format
// names.
func inputJSONSchema(s *InputSchema) any {
	base := BaseSchema{}
	if s != nil {
		base = s.BaseSchema
	}
	// The spec's own type name, which standardType reads; a $ref or an untyped input falls
	// back to its Go type.
	if base.Type == "" {
		base.Type = getSchemaType(s)
	}
	doc, ok := schemaToDoc(Schema{BaseSchema: base}).(map[string]any)
	if !ok {
		return map[string]any{}
	}
	if s != nil && s.Type == "count" {
		doc["minimum"] = 0
	}
	if s != nil && s.Default != nil && !s.Secret {
		doc["default"] = s.Default
	}
	doc, _ = standardSchema(doc).(map[string]any)
	if layouts := timeLayouts(s); layouts != nil {
		format := timeFormat(layouts)
		if relative(s) != "" {
			format = "" // a relative value ("2h", "yesterday") matches no JSON Schema format
		}
		setTimeFormat(doc, format)
	}
	moveValueConstraints(doc, base.Type)
	return doc
}

// valueConstraints are the keys that constrain each value, not the list or map holding them.
var valueConstraints = []string{
	"enum", "pattern", "minLength", "maxLength", "minimum", "maximum",
	"exclusiveMinimum", "exclusiveMaximum", "multipleOf", "format",
}

// moveValueConstraints moves a list's or map's per-value constraints from doc onto its
// `items` or `additionalProperties`.
func moveValueConstraints(doc map[string]any, typ string) {
	key := ""
	switch {
	case strings.HasPrefix(typ, "[]") || typ == "array":
		key = "items"
	default:
		if _, _, ok := splitMapType(typ); ok || typ == "map" {
			key = "additionalProperties"
		}
	}
	if key == "" {
		return
	}
	elem, _ := doc[key].(map[string]any)
	if elem == nil {
		elem = map[string]any{}
	}
	moved := false
	for _, k := range valueConstraints {
		if v, ok := doc[k]; ok {
			elem[k] = v
			delete(doc, k)
			moved = true
		}
	}
	if moved {
		doc[key] = elem
	}
}

// standardSchema rewrites a spec-vocabulary schema document into standard JSON Schema
// (draft-07) in place and returns it. Go type names map to their JSON shape; value types
// (duration, ip, bytesize, …) become strings, with a `format` where JSON Schema has one; an
// unknown type (an imported Go type) constrains nothing. `nullable` becomes a "null"
// alternative, and rotini-only keys (import, pattern_message) are dropped.
func standardSchema(v any) any {
	switch t := v.(type) {
	case map[string]any:
		for k, val := range t {
			switch k {
			case "properties", "definitions":
				if m, ok := val.(map[string]any); ok {
					for name, sub := range m {
						m[name] = standardSchema(sub)
					}
				}
			case "items", "additionalProperties":
				t[k] = standardSchema(val)
			case "allOf", "anyOf", "oneOf":
				if list, ok := val.([]any); ok {
					for i := range list {
						list[i] = standardSchema(list[i])
					}
				}
			}
		}
		delete(t, "import")
		delete(t, "pattern_message")
		if typ, ok := t["type"].(string); ok {
			delete(t, "type")
			maps.Copy(t, standardType(typ, t))
		}
		if nullable, _ := t["nullable"].(bool); nullable {
			if typ, ok := t["type"].(string); ok {
				t["type"] = []any{typ, "null"}
			}
		}
		delete(t, "nullable")
		return t
	default:
		return v
	}
}

// standardType returns the JSON Schema for a rotini type name. existing holds the schema's
// declared keys, so a declared `items`, `additionalProperties`, or `format` is kept.
func standardType(typ string, existing map[string]any) map[string]any {
	if elem, ok := strings.CutPrefix(typ, "[]"); ok {
		out := map[string]any{"type": "array"}
		if _, has := existing["items"]; !has {
			out["items"] = standardType(elem, map[string]any{})
		}
		return out
	}
	if _, val, ok := splitMapType(typ); ok {
		out := map[string]any{"type": "object"}
		if _, has := existing["additionalProperties"]; !has {
			out["additionalProperties"] = standardType(val, map[string]any{})
		}
		return out
	}
	str := func(format string) map[string]any {
		out := map[string]any{"type": "string"}
		if _, has := existing["format"]; format != "" && !has {
			out["format"] = format
		}
		return out
	}
	switch typ {
	case "boolean", "bool":
		return map[string]any{"type": "boolean"}
	case "integer", "int", "int8", "int16", "int32", "int64", "uint", "uint8", "uint16", "uint32", "uint64", "count":
		return map[string]any{"type": "integer"}
	case "number", "float32", "float64":
		return map[string]any{"type": "number"}
	case "array":
		return map[string]any{"type": "array"}
	case "object", "map":
		return map[string]any{"type": "object"}
	case "string", "existingfile", "existingdir", "duration", "email", "timezone", "mac", "ip", "cidr", "hostport",
		"bytesize", "hexbytes", "base64bytes", "glob":
		return str("")
	case "regexp":
		return str("regex")
	case "inputfile", "outputfile":
		return str("")
	case "time", "datetime":
		return str("date-time")
	case "date":
		return str("date")
	case "url":
		return str("uri")
	case "null":
		return map[string]any{"type": "null"}
	default:
		return map[string]any{}
	}
}

// marshalJSONFile renders v as indented JSON with a trailing newline. Output is deterministic
// because encoding/json sorts map keys.
func marshalJSONFile(v any) []byte {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		panic(err) // unreachable: the values are built from decoded JSON
	}
	return append(b, '\n')
}
