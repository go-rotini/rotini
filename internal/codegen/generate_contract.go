package codegen

import (
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
// (generate.schemas.output) and the contract document (generate.contract). Both are opt-in
// and, like help, omit hidden commands and inputs.

// contractFormat names the contract document's format and version. Breaking changes bump
// the version.
const contractFormat = "rotini-contract/1"

// contractDoc is the contract document. Its format is described by schema-contract.json.
type contractDoc struct {
	Format      string              `json:"format"`
	Name        string              `json:"name"`
	Commands    []contractCommand   `json:"commands"`
	Definitions map[string]any      `json:"definitions,omitempty"`
	Errors      json.RawMessage     `json:"errors"`
	Completion  *contractCompletion `json:"completion,omitempty"`
}

// contractCompletion is what the program's shell completion lets its users switch.
type contractCompletion struct {
	MessagesEnv string `json:"messages_env,omitempty"` // set to 0, false or off to hide completion messages
}

// contractCommand is one visible command, or one plugin a command declares.
type contractCommand struct {
	Name        string             `json:"name"`
	Path        []string           `json:"path"`
	Summary     string             `json:"summary,omitempty"`
	Description string             `json:"description,omitempty"`
	Aliases     []string           `json:"aliases,omitempty"`
	Deprecated  string             `json:"deprecated,omitempty"`
	Plugin      bool               `json:"plugin,omitempty"`
	Arguments   []contractArgument `json:"arguments,omitempty"`
	Flags       []contractFlag     `json:"flags,omitempty"`
	Env         []contractEnv      `json:"env,omitempty"`
	Config      []contractConfig   `json:"config,omitempty"`
	Stdin       *contractStdin     `json:"stdin,omitempty"`
	Parameters  map[string]any     `json:"parameters,omitempty"`
	Output      any                `json:"output,omitempty"`
	ExitStatus  []contractExit     `json:"exit_status,omitempty"`
}

type contractArgument struct {
	Name       string `json:"name"`
	Summary    string `json:"summary,omitempty"`
	Required   bool   `json:"required,omitempty"`
	Variadic   bool   `json:"variadic,omitempty"`
	Deprecated string `json:"deprecated,omitempty"`
	Schema     any    `json:"schema"`
}

type contractFlag struct {
	Name        string   `json:"name"`
	Identifiers []string `json:"identifiers"`
	Summary     string   `json:"summary,omitempty"`
	Required    bool     `json:"required,omitempty"`
	Cascading   bool     `json:"cascading,omitempty"`
	Inherited   bool     `json:"inherited,omitempty"`
	Env         []string `json:"env,omitempty"`        // fallback variables, in lookup order
	ConfigKey   string   `json:"config_key,omitempty"` // fallback config key, only when the command reads config files
	Deprecated  string   `json:"deprecated,omitempty"`
	Schema      any      `json:"schema"`
}

type contractEnv struct {
	Name       string   `json:"name"`
	Variables  []string `json:"variables"`
	Summary    string   `json:"summary,omitempty"`
	Required   bool     `json:"required,omitempty"`
	Secret     bool     `json:"secret,omitempty"`
	Deprecated string   `json:"deprecated,omitempty"`
	Schema     any      `json:"schema"`
}

type contractConfig struct {
	Name       string `json:"name"`
	Key        string `json:"key"`
	File       string `json:"file,omitempty"`
	Summary    string `json:"summary,omitempty"`
	Required   bool   `json:"required,omitempty"`
	Deprecated string `json:"deprecated,omitempty"`
	Schema     any    `json:"schema"`
}

type contractStdin struct {
	Format string `json:"format,omitempty"`
	Schema any    `json:"schema,omitempty"`
}

type contractExit struct {
	Code    int    `json:"code"`
	Summary string `json:"summary,omitempty"`
	Output  any    `json:"output,omitempty"`
}

// contractNode is one visible command as described by the contract and output schema files.
type contractNode struct {
	path      []string // names below the root; empty for the root
	help      cmdHelp
	inputs    *Inputs
	output    *Schema
	aliases   []string
	plugins   []PluginSpec
	inherited []FlagInput // visible cascading flags declared by ancestors, nearest first
	deprecate string
}

// contractNodes lists the visible commands depth-first, root first. A hidden command and its
// subtree are omitted.
func (p *program) contractNodes() []contractNode {
	visible := func(in *Inputs) []FlagInput {
		var out []FlagInput
		if in != nil {
			for _, f := range in.Flags {
				if f.Cascading && !f.Hidden {
					out = append(out, f)
				}
			}
		}
		return out
	}
	nodes := []contractNode{{path: []string{}, help: p.rootHelp, inputs: p.rootInputs, output: p.rootOutput, plugins: p.rootPlugins}}
	var walk func(rs []rnode, path []string, inherited []FlagInput)
	walk = func(rs []rnode, path []string, inherited []FlagInput) {
		for _, n := range rs {
			if n.hidden {
				continue
			}
			names := append(slices.Clone(path), n.name)
			nodes = append(nodes, contractNode{
				path: names, help: n.help, inputs: n.inputs, output: n.output,
				aliases: n.aliases, plugins: n.plugins, inherited: inherited, deprecate: n.deprecated,
			})
			walk(n.children, names, append(visible(n.inputs), inherited...))
		}
	}
	walk(p.tree, nil, visible(p.rootInputs))
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
	if g.Contract != nil && g.Contract.File != "" {
		abs, err := underModule(p.module.root, "generate.contract.file", g.Contract.File)
		if err != nil {
			return err
		}
		doc, err := p.contract(nodes(p))
		if err != nil {
			return err
		}
		return p.plan.write(abs, doc)
	}
	return nil
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
		page := manPageName(p.rootName, n.path)
		invocation := strings.Join(append([]string{p.rootName}, n.path...), " ")
		if n.output != nil {
			if b, ok := outputSchemaFile(invocation+" output", "", n.output, p.schemas); ok {
				files[page+outputSchemaSuffix] = b
			}
		}
		for _, e := range n.help.ExitStatus {
			if e.Output == nil {
				continue
			}
			title := invocation + " output on exit " + strconv.Itoa(e.Code)
			if b, ok := outputSchemaFile(title, e.Summary, e.Output, p.schemas); ok {
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

// outputDefLiteral renders a command's `Output: &rotini.OutputDef{Type, Schema}` field, or ""
// when it declares no output. typeName is its generated <Prefix>Output type; Schema is omitted
// when the shape cannot be rendered self-contained.
func outputDefLiteral(typeName string, shape *Schema, schemas map[string]Schema) string {
	if shape == nil {
		return ""
	}
	var b strings.Builder
	b.WriteString("Output: &" + rotiniPkgName + ".OutputDef{Type: reflect.TypeFor[" + typeName + "]()")
	if doc, ok := outputSchemaDoc(shape, schemas); ok {
		if raw, err := json.Marshal(doc); err == nil {
			b.WriteString(", Schema: " + goRawString(string(raw)))
		}
	}
	b.WriteString("},\n")
	return b.String()
}

// declaresOutput reports whether the generated Definition records any output type, which
// requires importing reflect.
func declaresOutput(gp *program) bool {
	if gp.rootOutput != nil {
		return true
	}
	found := false
	eachOwnNode(gp.tree, func(n *rnode) { found = found || n.output != nil })
	return found
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
	if mode, env := completionMessages(p.conf); mode != "" && env != "" {
		doc.Completion = &contractCompletion{MessagesEnv: env}
	}
	if len(p.schemas) > 0 {
		doc.Definitions = map[string]any{}
		for name, s := range p.schemas {
			doc.Definitions[name] = standardSchema(schemaToDoc(s))
		}
	}
	for _, n := range nodes {
		doc.Commands = append(doc.Commands, p.contractCommand(n))
		for _, pl := range n.plugins {
			doc.Commands = append(doc.Commands, contractCommand{
				Name:    strings.Join(append(append([]string{p.rootName}, n.path...), pl.Name), " "),
				Path:    append(slices.Clone(n.path), pl.Name),
				Summary: pl.Summary,
				Aliases: pl.Aliases,
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

// contractCommand describes one visible command.
func (p *program) contractCommand(n contractNode) contractCommand {
	c := contractCommand{
		Name:        strings.Join(append([]string{p.rootName}, n.path...), " "),
		Path:        n.path,
		Summary:     n.help.Summary,
		Description: n.help.Description,
		Aliases:     n.aliases,
		Deprecated:  n.deprecate,
	}
	params := map[string]any{}
	var required []string
	param := func(name, summary string, schema any, req bool) {
		if _, taken := params[name]; taken {
			return // the nearest declaration wins, as on the command line
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
	in := n.inputs
	if in == nil {
		in = &Inputs{}
	}
	readsConfig := p.readsConfig(n.path)
	for _, a := range in.Arguments {
		if a.Hidden {
			continue
		}
		req := a.Schema != nil && a.Schema.Required
		arg := contractArgument{Name: a.Name, Summary: a.Summary, Required: req, Variadic: isVariadicSchema(a.Schema), Deprecated: a.Deprecated, Schema: inputJSONSchema(a.Schema)}
		c.Arguments = append(c.Arguments, arg)
		param(a.Name, a.Summary, arg.Schema, req)
	}
	flag := func(f FlagInput, inherited bool) {
		req := f.Schema != nil && f.Schema.Required
		cf := contractFlag{
			Name: f.Name, Identifiers: flagIdentifiers(f), Summary: f.Summary, Required: req,
			Cascading: f.Cascading && !inherited, Inherited: inherited, Deprecated: f.Deprecated,
			Schema: inputJSONSchema(f.Schema),
		}
		// The same names help shows, from the same functions as the generated env tags.
		row := withConfigKeys([]templateDocFlagRow{flagRow(f, p.envPrefix)}, readsConfig)[0]
		cf.Env, cf.ConfigKey = row.Env, row.ConfigKey
		c.Flags = append(c.Flags, cf)
		param(f.Name, f.Summary, cf.Schema, req)
	}
	for _, f := range in.Flags {
		if !f.Hidden {
			flag(f, false)
		}
	}
	for _, f := range n.inherited {
		flag(f, true)
	}
	p.contractSources(&c, in)
	c.Parameters = map[string]any{"type": "object", "properties": params, "additionalProperties": false}
	if len(required) > 0 {
		c.Parameters["required"] = required
	}
	if n.output != nil {
		c.Output = standardSchema(schemaToDoc(*n.output))
	}
	for _, e := range n.help.ExitStatus {
		x := contractExit{Code: e.Code, Summary: e.Summary}
		if e.Output != nil {
			x.Output = standardSchema(schemaToDoc(*e.Output))
		}
		c.ExitStatus = append(c.ExitStatus, x)
	}
	return c
}

// contractSources describes a command's other input sources: environment variables,
// configuration keys and stdin.
func (p *program) contractSources(c *contractCommand, in *Inputs) {
	for _, e := range in.Env {
		if e.Hidden {
			continue
		}
		secret := e.Schema != nil && e.Schema.Secret
		c.Env = append(c.Env, contractEnv{
			Name: e.Name, Variables: strings.Split(envVarName(e, p.envPrefix), ","), Summary: e.Summary,
			Required: e.Schema != nil && e.Schema.Required, Secret: secret, Deprecated: e.Deprecated,
			Schema: inputJSONSchema(e.Schema),
		})
	}
	for _, cfg := range in.Config {
		if cfg.Hidden {
			continue
		}
		key, file := cfg.Name, ""
		if cfg.Schema != nil {
			if cfg.Schema.Key != "" {
				key = cfg.Schema.Key
			}
			file = cfg.Schema.File
		}
		c.Config = append(c.Config, contractConfig{
			Name: cfg.Name, Key: key, File: file, Summary: cfg.Summary,
			Required: cfg.Schema != nil && cfg.Schema.Required, Deprecated: cfg.Deprecated,
			Schema: inputJSONSchema(cfg.Schema),
		})
	}
	if in.Stdin != nil {
		st := &contractStdin{Format: in.Stdin.Format}
		if in.Stdin.Schema != nil {
			st.Schema = standardSchema(schemaToDoc(Schema{BaseSchema: in.Stdin.Schema.BaseSchema}))
		}
		c.Stdin = st
	}
}

// inputJSONSchema describes an input's value as standard JSON Schema: its type, constraints,
// enum, and default. A `secret` input's default is omitted.
func inputJSONSchema(s *InputSchema) any {
	base := BaseSchema{}
	if s != nil {
		base = s.BaseSchema
	}
	base.Type = getSchemaType(s)
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
	return standardSchema(doc)
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
		"bytesize", "hexbytes", "base64bytes":
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
