package codegen

import (
	"cmp"
	"encoding/json"
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"text/template"
)

// The tools feature: tool definitions for AI agents, one file per target, built from the
// contract. A command becomes a tool when it is offered to agents (see agentProgram), and its
// tool parameters are the inputs a caller may type, leaving out what an agent shouldn't
// supply: short-circuit, hidden, deprecated, secret and `from:` inputs (unless `agent: true`),
// the directory flag, and the machine-output flag, which the caller adds itself. Env and config
// inputs are the server's configuration, never parameters.

// toolsMetaKey is the MCP `_meta` key a tool's invocation facts live under.
const toolsMetaKey = "dev.rotini/invoke"

// Tool name limits per target.
var toolNameLimits = map[string]int{"mcp": 128, "openai-strict": 64, "gemini": 128}

// toolFiles are the file each tools target writes.
var toolFiles = map[string]string{"mcp": "mcp.json", "openai-strict": "openai.json", "gemini": "gemini.json"}

// tool is one command as a target-neutral tool definition. Its schemas are draft-07 style,
// with references to "#/definitions/<name>" resolved by defs.
type tool struct {
	cmd         agentCommand
	name, title string
	description string
	properties  map[string]any
	order       []string // parameter names in declaration order
	required    []string
	defs        map[string]any
	output      map[string]any // the output schema without its own definitions; nil for none
	outputDefs  map[string]any
	invoke      toolInvoke
}

// toolInvoke is what a server needs to turn a tool call back into a command line. It rides in
// the MCP tool's _meta, under toolsMetaKey.
type toolInvoke struct {
	Path        []string                  `json:"path"`
	Arguments   []toolInvokeArgument      `json:"arguments,omitempty"`
	Flags       map[string]toolInvokeFlag `json:"flags,omitempty"`
	Fixed       []string                  `json:"fixed,omitempty"` // added to every call: the machine-output flag
	Stdin       *toolInvokeStdin          `json:"stdin,omitempty"`
	Passthrough bool                      `json:"passthrough,omitempty"` // every word after the command is an argument; no "--"
	Stream      bool                      `json:"stream,omitempty"`      // stdout is a stream of output items, one per line
	Wrapped     bool                      `json:"wrapped,omitempty"`     // structured content is {"result": <stdout>}
}

// toolInvokeArgument is a positional argument, in command-line order.
type toolInvokeArgument struct {
	Name        string `json:"name"`
	Kind        string `json:"kind"`
	Variadic    bool   `json:"variadic,omitempty"`
	Passthrough bool   `json:"passthrough,omitempty"` // it and every later word are taken as typed
}

// toolInvokeFlag is how one flag parameter is written on the command line.
type toolInvokeFlag struct {
	Flag      string `json:"flag"`                // the identifier to write
	Kind      string `json:"kind"`                // scalar, count, list, map or object
	Type      string `json:"type,omitempty"`      // the rotini type, such as bool or []string
	Negated   string `json:"negated,omitempty"`   // a negatable bool's --no- form, for false
	Separator string `json:"separator,omitempty"` // a list's separator, when it has one
	Role      string `json:"role,omitempty"`      // the flag's role (dry-run, confirm, page, force, fields, sort), when it has one
}

// toolInvokeStdin is the parameter that carries what the command reads on stdin.
type toolInvokeStdin struct {
	Param  string `json:"param"`
	Format string `json:"format"`
}

// renderToolExports writes the tools feature's files: one per target.
func renderToolExports(p *program, f *Feature, a *agentProgram, _ *template.Template) (map[string][]byte, []error, error) {
	targets := f.Targets
	if len(targets) == 0 {
		targets = []string{"mcp"}
	}
	tools, notices, err := buildTools(a, f.McpRevision == "2025-11-25")
	if err != nil {
		return nil, nil, err
	}
	for _, t := range tools {
		for _, target := range targets {
			if limit := toolNameLimits[target]; len(t.name) > limit {
				return nil, nil, fmt.Errorf("the tool name %q for %q is longer than %s allows (%d characters)", t.name, t.cmd.invocation, target, limit)
			}
		}
	}
	files := map[string][]byte{}
	for _, target := range targets {
		var doc any
		var more []error
		switch target {
		case "mcp":
			doc = mcpTools(tools)
		case "openai-strict":
			doc, more = openAITools(tools)
		case "gemini":
			doc, more = geminiTools(tools)
		}
		notices = append(notices, more...)
		files[toolFiles[target]] = marshalJSONFile(doc)
	}
	if f.Go {
		p.toolsGo = files["mcp.json"]
	}
	return files, notices, nil
}

// toolsDecl renders the cmd file's ToolsMCP variable, or "" when the tools feature's `go` is
// off.
func toolsDecl(doc []byte) string {
	if doc == nil {
		return ""
	}
	return "// ToolsMCP is the program's MCP tool definitions (a tools/list result): the tools\n" +
		"// feature's mcp.json.\nvar ToolsMCP = " + goRawString(string(doc)) + "\n"
}

// buildTools turns every command offered to agents into a tool. A command whose required input
// a tool can't supply is left out, with a notice; two commands with one tool name are an error.
func buildTools(a *agentProgram, wrapOutput bool) ([]tool, []error, error) {
	var tools []tool
	var notices []error
	names := map[string]string{}
	for _, c := range a.offered() {
		t, skip := buildTool(a, c, wrapOutput)
		if skip != "" {
			notices = append(notices, fmt.Errorf("tools: %q is left out of the tool definitions: %s", c.invocation, skip))
			continue
		}
		if prev, ok := names[t.name]; ok {
			return nil, nil, fmt.Errorf("%q and %q both make the tool name %q; rename one of them, or keep one from agents with `agent: false`", prev, c.invocation, t.name)
		}
		names[t.name] = c.invocation
		tools = append(tools, t)
	}
	return tools, notices, nil
}

// buildTool builds one command's tool. skip says why the command can't be a tool, or "".
func buildTool(a *agentProgram, c agentCommand, wrapOutput bool) (tool, string) {
	cmd := c.c
	t := tool{
		cmd:        c,
		name:       strings.Join(append([]string{a.doc.Name}, cmd.Path...), "_"),
		title:      c.invocation,
		properties: map[string]any{},
		invoke:     toolInvoke{Path: slices.Clone(cmd.Path), Fixed: c.machine, Stream: cmd.Stream, Passthrough: cmd.Passthrough},
	}
	if t.invoke.Path == nil {
		t.invoke.Path = []string{}
	}
	serverEnv, skip := t.addInputs(cmd)
	if skip != "" {
		return t, skip
	}
	if skip := t.addStdin(cmd.Stdin); skip != "" {
		return t, skip
	}
	for _, e := range cmd.Env {
		if e.Required && !e.Hidden {
			serverEnv = append(serverEnv, e.Variables...)
		}
	}
	t.defs = reachableContractDefs(t.properties, a.doc.Definitions)
	t.setOutput(cmd, wrapOutput)
	t.description = toolDescription(c, serverEnv)
	return t, ""
}

// addParam adds one input as a parameter: its schema, with its description (else summary) and
// its stability.
func (t *tool) addParam(in agentInput) {
	s := maps.Clone(in.Schema)
	if s == nil {
		s = map[string]any{}
	}
	desc := cmp.Or(in.Description, in.Summary)
	if in.Stability != "" {
		desc = strings.TrimSpace(desc + " (" + in.Stability + ")")
	}
	if desc != "" {
		s["description"] = desc
	}
	t.properties[in.Name] = s
	t.order = append(t.order, in.Name)
	if in.Required {
		t.required = append(t.required, in.Name)
	}
}

// addInputs adds the command's arguments and flags that are tool parameters, with how to write
// each on the command line. A required input left out stops the tool (skip says why), unless it
// is a secret the server's environment or config can supply: serverEnv lists those variables.
func (t *tool) addInputs(cmd agentContractCmd) (serverEnv []string, skip string) {
	excluded := func(in agentInput) string {
		if !in.Required {
			return ""
		}
		if in.Secret && (len(in.Env) > 0 || in.ConfigKey != "") {
			serverEnv = append(serverEnv, in.Env...)
			return ""
		}
		return fmt.Sprintf("its required %s %q isn't a tool parameter (it is hidden, deprecated, secret or read from a file); give it `agent: true` or a fallback the server can supply", inputChannel(in), in.Name)
	}
	seen := map[string]bool{}
	for _, in := range cmd.Arguments {
		seen[in.Name] = true
		if !inputOffered(in, false) {
			if why := excluded(in); why != "" {
				return nil, why
			}
			continue
		}
		t.addParam(in)
		t.invoke.Arguments = append(t.invoke.Arguments, toolInvokeArgument{Name: in.Name, Kind: in.Kind, Variadic: in.Variadic, Passthrough: in.Passthrough})
		t.invoke.Passthrough = t.invoke.Passthrough || in.Passthrough
	}
	for _, in := range cmd.Flags {
		if seen[in.Name] {
			continue // the nearest declaration wins, as on the command line
		}
		seen[in.Name] = true
		switch {
		case in.Role == "machine-output" || in.Role == "chdir":
			continue
		case !toolFlagOffered(in):
			if why := excluded(in); why != "" {
				return nil, why
			}
			continue
		}
		t.addParam(in)
		if t.invoke.Flags == nil {
			t.invoke.Flags = map[string]toolInvokeFlag{}
		}
		fl := toolInvokeFlag{Flag: longIdentifier(in.Identifiers), Kind: in.Kind, Type: in.Type, Separator: in.Separator, Role: in.Role}
		if len(in.Negated) > 0 {
			fl.Negated = in.Negated[0]
		}
		t.invoke.Flags[in.Name] = fl
	}
	return serverEnv, ""
}

// addStdin adds the `stdin` parameter for a command that reads stdin: the stdin schema for a
// JSON or YAML document, else a string. skip is set when an input already has the name.
func (t *tool) addStdin(st *contractStdin) string {
	if st == nil {
		return ""
	}
	if _, clash := t.properties["stdin"]; clash {
		return `it reads stdin and also has an input named "stdin", which is the tool parameter stdin's content goes in; rename the input`
	}
	schema := map[string]any{"type": "string"}
	if s, ok := st.Schema.(map[string]any); ok && (st.Format == "json" || st.Format == "yaml") {
		schema = maps.Clone(s)
	}
	// Its definitions come from the contract's, as the inputs' do.
	delete(schema, "$schema")
	delete(schema, "definitions")
	desc := "What the command reads on stdin"
	switch st.Format {
	case "json", "yaml":
		desc += ", as a JSON value"
	case "lines", "jsonl":
		desc += ", one item per line"
	}
	schema["description"] = desc + "."
	t.properties["stdin"] = schema
	t.order = append(t.order, "stdin")
	if st.Required {
		t.required = append(t.required, "stdin")
	}
	t.invoke.Stdin = &toolInvokeStdin{Param: "stdin", Format: cmp.Or(st.Format, "text")}
	return ""
}

// setOutput sets the tool's output schema from the command's: an array of items for a stream,
// and for the older MCP revision, which wants an object, a non-object wrapped as
// {"result": …}.
func (t *tool) setOutput(cmd agentContractCmd, wrapOutput bool) {
	if cmd.Output == nil {
		return
	}
	o := maps.Clone(cmd.Output)
	delete(o, "$schema")
	if d, ok := o["definitions"].(map[string]any); ok {
		t.outputDefs = d
		delete(o, "definitions")
	}
	if cmd.Stream {
		o = map[string]any{"type": "array", "items": o}
	}
	if wrapOutput {
		if isObjectSchema(o, t.outputDefs) {
			o["type"] = "object" // the older revision wants it at the root, beside a $ref
		} else {
			o = map[string]any{"type": "object", "properties": map[string]any{"result": o}, "required": []any{"result"}}
			t.invoke.Wrapped = true
		}
	}
	t.output = o
}

// inputChannel names an input's kind for a message.
func inputChannel(in agentInput) string {
	if len(in.Identifiers) > 0 {
		return "flag"
	}
	return "argument"
}

// isObjectSchema reports whether a schema describes a JSON object, following a top-level $ref.
func isObjectSchema(s, defs map[string]any) bool {
	for range 8 {
		if ref, ok := s["$ref"].(string); ok {
			name, _ := strings.CutPrefix(ref, "#/definitions/")
			d, _ := defs[name].(map[string]any)
			if d == nil {
				return false
			}
			s = d
			continue
		}
		break
	}
	return s["type"] == "object"
}

// toolDescription is the text a model reads about a tool: what the command does, how settled
// it is, its effects, what the server's environment must supply, and its exit codes.
func toolDescription(c agentCommand, serverEnv []string) string {
	cmd := c.c
	var parts []string
	if cmd.Summary != "" {
		parts = append(parts, cmd.Summary)
	}
	if cmd.Description != "" && cmd.Description != cmd.Summary {
		parts = append(parts, cmd.Description)
	}
	if cmd.Deprecated != "" {
		parts = append(parts, "Deprecated: "+cmd.Deprecated)
	}
	switch c.stability {
	case "experimental":
		parts = append(parts, "Experimental: it may change or be removed in any release.")
	case "beta":
		parts = append(parts, "Beta: it may change in a minor release.")
	}
	if c.effects != nil {
		parts = append(parts, "Effects: "+effectsText(c.effects)+".")
	}
	if len(serverEnv) > 0 {
		parts = append(parts, "Requires "+strings.Join(slices.Compact(slices.Sorted(slices.Values(serverEnv))), ", ")+" in the server's environment.")
	}
	if len(cmd.ExitStatus) > 0 {
		codes := make([]string, 0, len(cmd.ExitStatus))
		for _, e := range cmd.ExitStatus {
			s := strconv.Itoa(e.Code)
			var notes []string
			if e.Name != "" {
				notes = append(notes, e.Name)
			}
			if e.Retryable {
				notes = append(notes, "retryable")
			}
			if len(notes) > 0 {
				s += " (" + strings.Join(notes, ", ") + ")"
			}
			if e.Summary != "" {
				s += ": " + e.Summary
			}
			codes = append(codes, s)
		}
		parts = append(parts, "Exit codes: "+strings.Join(codes, "; ")+".")
	}
	return strings.Join(parts, "\n\n")
}

// reachableContractDefs returns the contract definitions the schemas reference, directly or
// through another definition.
func reachableContractDefs(schemas, pool map[string]any) map[string]any {
	out := map[string]any{}
	queue := schemaRefs(schemas)
	for len(queue) > 0 {
		name := queue[0]
		queue = queue[1:]
		if _, done := out[name]; done {
			continue
		}
		d, ok := pool[name]
		if !ok {
			continue
		}
		out[name] = d
		queue = append(queue, schemaRefs(d)...)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// jsonCopy copies a decoded JSON value, so a target's rewrites never touch the shared tool.
func jsonCopy(v any) any {
	switch t := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, val := range t {
			out[k] = jsonCopy(val)
		}
		return out
	case []any:
		out := make([]any, len(t))
		for i, val := range t {
			out[i] = jsonCopy(val)
		}
		return out
	case []string:
		out := make([]any, len(t))
		for i, s := range t {
			out[i] = s
		}
		return out
	default:
		return v
	}
}

// toDefs rewrites every "#/definitions/X" reference in v to "#/$defs/X", walking the decoded
// JSON so that a string value that only looks like a reference is left alone.
func toDefs(v any) any {
	switch t := v.(type) {
	case map[string]any:
		for k, val := range t {
			if ref, ok := val.(string); ok && k == "$ref" {
				if name, ok := strings.CutPrefix(ref, "#/definitions/"); ok {
					t[k] = "#/$defs/" + name
				}
				continue
			}
			if k == "properties" || k == "$defs" || k == "definitions" {
				if m, ok := val.(map[string]any); ok {
					for name, sub := range m {
						m[name] = toDefs(sub)
					}
				}
				continue
			}
			t[k] = toDefs(val)
		}
		return t
	case []any:
		for i := range t {
			t[i] = toDefs(t[i])
		}
		return t
	default:
		return v
	}
}

// inputSchema returns the tool's parameters as one object schema, with its definitions under
// defsKey ("definitions" left as is, or "$defs" with references rewritten).
func (t tool) inputSchema(defsKey string) map[string]any {
	props := map[string]any{}
	for k, v := range t.properties {
		props[k] = jsonCopy(v)
	}
	s := map[string]any{"type": "object", "properties": props, "additionalProperties": false}
	if len(t.required) > 0 {
		s["required"] = jsonCopy(t.required)
	}
	if len(t.defs) > 0 {
		s[defsKey] = jsonCopy(t.defs)
	}
	if defsKey == "$defs" {
		toDefs(s)
	}
	return s
}

// ─── mcp ────────────────────────────────────────────────────────────────────────.

// mcpTools renders the tools as an MCP tools/list result.
func mcpTools(tools []tool) map[string]any {
	list := make([]any, 0, len(tools))
	for _, t := range tools {
		m := map[string]any{
			"name":        t.name,
			"title":       t.title,
			"description": t.description,
			"inputSchema": t.inputSchema("$defs"),
			"_meta":       map[string]any{toolsMetaKey: t.invoke},
		}
		if t.output != nil {
			o, _ := jsonCopy(t.output).(map[string]any)
			if len(t.outputDefs) > 0 {
				o["$defs"] = jsonCopy(t.outputDefs)
			}
			m["outputSchema"] = toDefs(o)
		}
		if ann := mcpAnnotations(t.cmd.effects); ann != nil {
			m["annotations"] = ann
		}
		list = append(list, m)
	}
	return map[string]any{"tools": list}
}

// mcpAnnotations maps effects onto MCP tool annotations; nil when the command declares none,
// which MCP clients read as the worst case.
func mcpAnnotations(e *Effects) map[string]any {
	if e == nil {
		return nil
	}
	ann := map[string]any{}
	switch e.Kind {
	case "read":
		ann["readOnlyHint"] = true
	case "write":
		ann["readOnlyHint"] = false
		ann["destructiveHint"] = false
	case "destructive":
		ann["readOnlyHint"] = false
		ann["destructiveHint"] = true
	}
	if e.Idempotent != nil {
		ann["idempotentHint"] = *e.Idempotent
	}
	if e.OpenWorld != nil {
		ann["openWorldHint"] = *e.OpenWorld
	}
	return ann
}

// ─── openai-strict ──────────────────────────────────────────────────────────────.

// openAIStrictKeys are the JSON Schema keywords OpenAI's strict mode accepts. Others move into
// the description.
var openAIStrictKeys = []string{
	"type", "properties", "required", "additionalProperties", "items", "enum", "const", "anyOf",
	"$ref", "$defs", "description", "title", "pattern", "minimum", "maximum", "exclusiveMinimum",
	"exclusiveMaximum", "multipleOf", "minItems", "maxItems", "format",
}

// openAIFormats are the string formats strict mode accepts.
var openAIFormats = []string{"date-time", "time", "date", "duration", "email", "hostname", "ipv4", "ipv6", "uuid"}

// openAITools renders the tools as OpenAI Responses API function tools in strict mode. A tool
// strict mode can't express (a map parameter, too deep or too many properties) is left out,
// with a notice.
func openAITools(tools []tool) (any, []error) {
	list := make([]any, 0, len(tools))
	var notices []error
	for _, t := range tools {
		params := t.inputSchema("$defs")
		why := ""
		strictify(params, &why)
		if why == "" && schemaDepth(params) > 10 {
			why = "its parameters nest more than 10 levels"
		}
		if why == "" && countProperties(params) > 5000 {
			why = "its parameters have more than 5000 properties"
		}
		if why != "" {
			notices = append(notices, fmt.Errorf("tools: %q is left out of openai.json: %s, which OpenAI's strict mode can't express", t.cmd.invocation, why))
			continue
		}
		list = append(list, map[string]any{
			"type":        "function",
			"name":        t.name,
			"description": t.description,
			"parameters":  params,
			"strict":      true,
		})
	}
	return list, notices
}

// strictify rewrites a schema in place for strict mode: every object closed with every property
// required (optional ones made nullable), and unsupported keywords moved into the description.
// why is set when the schema holds something strict mode can't express.
func strictify(s map[string]any, why *string) {
	moveKeywords(s, openAIStrictKeys, func(f string) bool { return slices.Contains(openAIFormats, f) })
	if defs, ok := s["$defs"].(map[string]any); ok {
		for _, d := range defs {
			if m, ok := d.(map[string]any); ok {
				strictify(m, why)
			}
		}
	}
	if items, ok := s["items"].(map[string]any); ok {
		strictify(items, why)
	}
	if list, ok := s["anyOf"].([]any); ok {
		for _, e := range list {
			if m, ok := e.(map[string]any); ok {
				strictify(m, why)
			}
		}
	}
	if !typeIs(s, "object") {
		return
	}
	props, _ := s["properties"].(map[string]any)
	if ap, ok := s["additionalProperties"]; (ok && ap != false) || props == nil {
		if *why == "" {
			*why = "it has a map or free-form object parameter"
		}
		return
	}
	s["additionalProperties"] = false
	required := map[string]bool{}
	if list, ok := s["required"].([]any); ok {
		for _, r := range list {
			if name, ok := r.(string); ok {
				required[name] = true
			}
		}
	}
	all := make([]any, 0, len(props))
	for _, name := range slices.Sorted(maps.Keys(props)) {
		all = append(all, name)
		m, ok := props[name].(map[string]any)
		if !ok {
			continue
		}
		strictify(m, why)
		if !required[name] {
			props[name] = nullable(m)
		}
	}
	s["required"] = all
}

// typeIs reports whether a schema's type is, or includes, t.
func typeIs(s map[string]any, t string) bool {
	switch v := s["type"].(type) {
	case string:
		return v == t
	case []any:
		return slices.Contains(v, any(t))
	}
	return false
}

// nullable lets a schema also take null, for an optional property in strict mode.
func nullable(s map[string]any) map[string]any {
	switch t := s["type"].(type) {
	case string:
		s["type"] = []any{t, "null"}
	case []any:
		if !slices.Contains(t, any("null")) {
			s["type"] = append(t, "null")
		}
	default:
		desc, _ := s["description"].(string)
		delete(s, "description")
		out := map[string]any{"anyOf": []any{s, map[string]any{"type": "null"}}}
		if desc != "" {
			out["description"] = desc
		}
		return out
	}
	if enum, ok := s["enum"].([]any); ok && !slices.Contains(enum, nil) {
		s["enum"] = append(enum, nil)
	}
	return s
}

// moveKeywords keeps the keywords a target accepts and turns the rest into sentences appended
// to the schema's description, so no constraint is dropped silently. A format keep rejects
// moves too. Extension keys (x-…) are dropped.
func moveKeywords(s map[string]any, keep []string, keepFormat func(string) bool) {
	var notes []string
	for _, k := range slices.Sorted(maps.Keys(s)) {
		v := s[k]
		switch {
		case strings.HasPrefix(k, "x-"), k == "$schema":
			delete(s, k)
		case k == "format":
			if f, _ := v.(string); !keepFormat(f) {
				notes = append(notes, "format: "+f)
				delete(s, k)
			}
		case !slices.Contains(keep, k):
			notes = append(notes, keywordNote(k, v))
			delete(s, k)
		}
	}
	if len(notes) == 0 {
		return
	}
	desc, _ := s["description"].(string)
	s["description"] = strings.TrimSpace(strings.TrimSuffix(desc, ".") + ". " + strings.Join(notes, "; ") + ".")
	if desc == "" {
		s["description"] = strings.Join(notes, "; ") + "."
	}
}

// keywordNote says what a JSON Schema keyword asks, in words.
func keywordNote(k string, v any) string {
	val := func() string {
		b, err := json.Marshal(v)
		if err != nil {
			return fmt.Sprint(v)
		}
		return string(b)
	}
	switch k {
	case "default":
		return "default: " + val()
	case "minLength":
		return "at least " + val() + " characters"
	case "maxLength":
		return "at most " + val() + " characters"
	case "uniqueItems":
		if v == true {
			return "no repeated items"
		}
		return "repeated items allowed"
	case "pattern":
		return "matches the pattern " + val()
	case "minItems":
		return "at least " + val() + " items"
	case "maxItems":
		return "at most " + val() + " items"
	case "exclusiveMinimum":
		return "greater than " + val()
	case "exclusiveMaximum":
		return "less than " + val()
	case "multipleOf":
		return "a multiple of " + val()
	default:
		return k + ": " + val()
	}
}

// schemaDepth returns how many object and array levels a schema nests, its definitions
// included.
func schemaDepth(v any) int {
	m, ok := v.(map[string]any)
	if !ok {
		return 0
	}
	deepest := 0
	visit := func(c any) {
		if d := schemaDepth(c); d > deepest {
			deepest = d
		}
	}
	for _, key := range []string{"properties", "$defs"} {
		if sub, ok := m[key].(map[string]any); ok {
			for _, c := range sub {
				visit(c)
			}
		}
	}
	visit(m["items"])
	if list, ok := m["anyOf"].([]any); ok {
		for _, c := range list {
			visit(c)
		}
	}
	if typeIs(m, "object") || typeIs(m, "array") {
		return deepest + 1
	}
	return deepest
}

// countProperties counts every property a schema declares, its definitions included.
func countProperties(v any) int {
	n := 0
	switch t := v.(type) {
	case map[string]any:
		if props, ok := t["properties"].(map[string]any); ok {
			n += len(props)
		}
		for _, c := range t {
			n += countProperties(c)
		}
	case []any:
		for _, c := range t {
			n += countProperties(c)
		}
	}
	return n
}

// ─── gemini ─────────────────────────────────────────────────────────────────────.

// geminiKeys are the JSON Schema keywords Gemini documents for function parameters. Others move
// into the description.
var geminiKeys = []string{
	"type", "title", "description", "properties", "required", "additionalProperties", "enum",
	"format", "minimum", "maximum", "items", "prefixItems", "minItems", "maxItems", "anyOf", "$ref", "$defs",
}

// geminiName is the rule Gemini (Vertex AI) has for parameter names.
var geminiName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,63}$`)

// geminiTools renders the tools as Gemini function declarations, with JSON Schema parameters
// (parametersJsonSchema). References are inlined unless a schema is recursive, and parameter
// names have '-' written '_' (dry-run is dry_run).
func geminiTools(tools []tool) (any, []error) {
	list := make([]any, 0, len(tools))
	var notices []error
	for _, t := range tools {
		params := t.inputSchema("$defs")
		defs, _ := params["$defs"].(map[string]any)
		delete(params, "$defs")
		recursive := false
		inlined := inlineRefs(params, defs, nil, &recursive)
		params, _ = inlined.(map[string]any)
		if recursive {
			params["$defs"] = defs
			notices = append(notices, fmt.Errorf("tools: %q has a recursive parameter schema, so gemini.json keeps its $ref and $defs", t.cmd.invocation))
		}
		geminiKeywords(params)
		props, _ := params["properties"].(map[string]any)
		renamed := map[string]any{}
		for name, s := range props {
			g := strings.ReplaceAll(name, "-", "_")
			if !geminiName.MatchString(g) {
				notices = append(notices, fmt.Errorf("tools: %q's parameter %q breaks Gemini's parameter-name rule (letters, digits and _, at most 64)", t.cmd.invocation, name))
			}
			renamed[g] = s
		}
		if len(renamed) != len(props) {
			notices = append(notices, fmt.Errorf("tools: %q is left out of gemini.json: two of its parameter names differ only in - and _", t.cmd.invocation))
			continue
		}
		params["properties"] = renamed
		if req, ok := params["required"].([]any); ok {
			for i, r := range req {
				if name, ok := r.(string); ok {
					req[i] = strings.ReplaceAll(name, "-", "_")
				}
			}
		}
		list = append(list, map[string]any{"name": t.name, "description": t.description, "parametersJsonSchema": params})
	}
	return map[string]any{"functionDeclarations": list}, notices
}

// inlineRefs replaces each "#/$defs/X" reference with a copy of X. A reference back into a
// definition already being inlined is left in place and sets recursive.
func inlineRefs(v any, defs map[string]any, stack []string, recursive *bool) any {
	switch t := v.(type) {
	case map[string]any:
		if ref, ok := t["$ref"].(string); ok {
			name, _ := strings.CutPrefix(ref, "#/$defs/")
			if slices.Contains(stack, name) {
				*recursive = true
				return t
			}
			if d, ok := defs[name]; ok {
				out, _ := inlineRefs(jsonCopy(d), defs, append(stack, name), recursive).(map[string]any)
				if desc, ok := t["description"].(string); ok && out != nil {
					out["description"] = desc
				}
				return out
			}
			return t
		}
		for k, val := range t {
			t[k] = inlineRefs(val, defs, stack, recursive)
		}
		return t
	case []any:
		for i := range t {
			t[i] = inlineRefs(t[i], defs, stack, recursive)
		}
		return t
	default:
		return v
	}
}

// geminiKeywords applies Gemini's keyword list to a schema and everything inside it.
func geminiKeywords(v any) {
	m, ok := v.(map[string]any)
	if !ok {
		return
	}
	moveKeywords(m, geminiKeys, func(f string) bool { return f == "date-time" || f == "date" || f == "time" })
	for _, key := range []string{"properties", "$defs"} {
		if sub, ok := m[key].(map[string]any); ok {
			for _, c := range sub {
				geminiKeywords(c)
			}
		}
	}
	geminiKeywords(m["items"])
	if ap, ok := m["additionalProperties"].(map[string]any); ok {
		geminiKeywords(ap)
	}
	for _, key := range []string{"anyOf", "prefixItems"} {
		if list, ok := m[key].([]any); ok {
			for _, c := range list {
				geminiKeywords(c)
			}
		}
	}
}
