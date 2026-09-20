package codegen

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/go-rotini/jsonschema"
)

// Spec-Schema → Go-source codegen: the output-type and stdin/validation schema
// declarations the generator embeds (run after the command tree is resolved).

// outputRootSentinel is the throwaway root type GenerateGo always emits for the
// assembled output-types document; it carries no data and is stripped, leaving
// only the document-level named schemas and the per-command <Prefix>Output types.
const outputRootSentinel = "rotiniGeneratedOutputsRoot"

// buildOutputTypes generates the Go type declarations for a program's output types and named
// schemas as a formatted source fragment ready to inject into the framework file, or "" when
// the program declares neither.
//
// Every document-level schema becomes a named type, and every own command declaring `output`
// gets a "<Prefix>Output" type. Generation reuses jsonschema.GenerateGo, the same engine
// behind the spec and conf types, so refs, nesting, arrays and allOf embedding all work.
func buildOutputTypes(gp *program, pkg string) (string, error) {
	defs := collectOutputDefs(gp)
	if len(defs) == 0 {
		return "", nil
	}
	doc, err := json.Marshal(map[string]any{
		"$schema":     "http://json-schema.org/draft-07/schema#",
		"type":        "object",
		"definitions": defs,
	})
	if err != nil {
		return "", fmt.Errorf("marshal output schema document: %w", err)
	}
	src, err := jsonschema.GenerateGo(doc,
		jsonschema.WithGoPackage(pkg),
		jsonschema.WithGoRootType(outputRootSentinel))
	if err != nil {
		return "", fmt.Errorf("generate output types: %w", err)
	}
	return stripGenerated(string(src), outputRootSentinel), nil
}

// outputTypeNames lists the top-level type names buildOutputTypes declares, sorted so
// the generated aliases are deterministic. It reads the same definition set the
// generator does, so the two cannot disagree about what exists.
func outputTypeNames(gp *program) []string {
	defs := collectOutputDefs(gp)
	out := make([]string, 0, len(defs))
	for name := range defs {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// eachOwnNode visits every non-composed command node in the resolved tree
// depth-first (pre-order), skipping composed subtrees entirely — their output and
// stdin types live in the child's cmd. Shared by the output- and stdin-type
// collectors.
func eachOwnNode(nodes []rnode, visit func(n *rnode)) {
	for i := range nodes {
		if nodes[i].composed {
			continue
		}
		visit(&nodes[i])
		eachOwnNode(nodes[i].children, visit)
	}
}

// collectOutputDefs assembles the JSON-schema `definitions` for the output-types
// document: each document-level named schema, plus one "<Prefix>Output" per
// command that declares an output. Refs are rewritten from the spec's
// "#/schemas/" space to the document's "#/definitions/" space.
func collectOutputDefs(gp *program) map[string]any {
	defs := map[string]any{}
	for name, sch := range gp.schemas {
		defs[name] = schemaToDoc(sch)
	}
	add := func(prefix string, out *Schema) {
		if out != nil {
			defs[prefix+"Output"] = schemaToDoc(*out)
		}
	}
	// A command's stdin payload type "<Prefix>Stdin" comes from the schema-shape of
	// its stdin InputSchema (only the BaseSchema part — required/default/etc. are
	// input metadata, not JSON-schema type structure).
	addStdin := func(prefix string, in *Inputs) {
		if in != nil && in.Stdin != nil && in.Stdin.Schema != nil && !rawStdinFormat(in.Stdin.Format) {
			defs[prefix+"Stdin"] = schemaToDoc(Schema{BaseSchema: in.Stdin.Schema.BaseSchema})
		}
	}
	add(gp.rootPascal, gp.rootOutput)
	addStdin(gp.rootPascal, gp.rootInputs)
	eachOwnNode(gp.tree, func(n *rnode) {
		add(n.prefix, n.output)
		addStdin(n.prefix, n.inputs)
	})
	return defs
}

// pathFromClaim accumulates the config_source inputs claiming one
// configuration_files entry: a flag's logical name and/or an env input's
// variable.
type pathFromClaim struct {
	flag string
	env  string
}

// collectPathFrom maps each configuration_files name to the inputs supplying its path, across
// the whole tree: a flag claims by logical name, an env input by its variable. Validation
// guarantees single claims per channel and that the named entry exists.
func collectPathFrom(gp *program) map[string]pathFromClaim {
	out := map[string]pathFromClaim{}
	add := func(in *Inputs) {
		if in == nil {
			return
		}
		for _, f := range in.Flags {
			if f.Schema != nil && f.Schema.ConfigSource != "" {
				c := out[f.Schema.ConfigSource]
				c.flag = f.Name
				out[f.Schema.ConfigSource] = c
			}
		}
		for _, e := range in.Env {
			if e.Schema != nil && e.Schema.ConfigSource != "" {
				c := out[e.Schema.ConfigSource]
				c.env = envVarName(e, gp.envPrefix)
				out[e.Schema.ConfigSource] = c
			}
		}
	}
	add(gp.rootInputs)
	eachOwnNode(gp.tree, func(n *rnode) { add(n.inputs) })
	return out
}

// contractComment is the TextUnmarshaler nudge emitted on flag and argument fields whose type
// comes from an explicit spec `import:`, putting the contract where the user reads their own
// generated code. Builtin aliases parse themselves, and env and config fields decode through
// recon, so neither gets one.
func contractComment(schema *InputSchema) string {
	if schema == nil || strings.TrimSpace(schema.Import) == "" {
		return ""
	}
	return "// parsed via its encoding.TextUnmarshaler (see the spec schema's `type` docs)"
}

// envVarName is an env input's environment variable: the explicit `variable:`
// when declared, else the SNAKE_UPPER projection of its name (recon's default).
func envVarName(e EnvInput, envPrefix string) string {
	if v := envVarOf(e.Schema); v != "" {
		return v // explicit variable: exempt from env_prefix — already exact
	}
	derived := strings.ToUpper(strings.NewReplacer("-", "_", ".", "_").Replace(e.Name))
	if envPrefix != "" {
		return envPrefix + "_" + derived
	}
	return derived
}

// collectStdinSchemas builds the per-command stdin validation schemas for BindMeta: each own
// command declaring a stdin payload maps its "<Prefix>Stdin" type name to a self-contained
// JSON Schema the binder validates the decoded payload against.
func collectStdinSchemas(gp *program) map[string]string {
	out := map[string]string{}
	add := func(prefix string, in *Inputs) {
		if in == nil || in.Stdin == nil || in.Stdin.Schema == nil {
			return
		}
		if js := stdinValidationSchema(in.Stdin.Schema, gp.schemas); js != "" {
			out[prefix+"Stdin"] = js
		}
	}
	add(gp.rootPascal, gp.rootInputs)
	eachOwnNode(gp.tree, func(n *rnode) { add(n.prefix, n.inputs) })
	if len(out) == 0 {
		return nil
	}
	return out
}

// stdinValidationSchema renders the stdin payload's load-time validation
// schema. It uses only the schema-shape of the InputSchema (the BaseSchema),
// matching the generated type.
func stdinValidationSchema(stdin *InputSchema, docSchemas map[string]Schema) string {
	return validationSchema(Schema{BaseSchema: stdin.BaseSchema}, docSchemas)
}

// validationSchema renders a self-contained JSON Schema for load-time document validation,
// shared by the stdin payload and configuration_files entries: the declared shape plus the
// document's named schemas as definitions, so any "#/schemas/X" refs resolve.
func validationSchema(schema Schema, docSchemas map[string]Schema) string {
	body, ok := schemaToDoc(schema).(map[string]any)
	if !ok {
		return ""
	}
	body["$schema"] = "http://json-schema.org/draft-07/schema#"
	if len(docSchemas) > 0 {
		defs := map[string]any{}
		for name, s := range docSchemas {
			defs[name] = schemaToDoc(s)
		}
		body["definitions"] = defs
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return ""
	}
	return string(raw)
}

// schemaToDoc marshals a spec Schema to a generic JSON-schema value and rewrites
// its "#/schemas/" refs to "#/definitions/".
func schemaToDoc(s Schema) any {
	raw, err := json.Marshal(s)
	if err != nil {
		return map[string]any{}
	}
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return map[string]any{}
	}
	rewriteSchemaRefs(v)
	return v
}

// rewriteSchemaRefs deep-walks v, rewriting every {"$ref": "#/schemas/X"} to
// "#/definitions/X" so the assembled document (which uses `definitions`) resolves.
func rewriteSchemaRefs(v any) {
	switch t := v.(type) {
	case map[string]any:
		for k, val := range t {
			if k == "$ref" {
				if s, ok := val.(string); ok {
					t[k] = strings.Replace(s, "#/schemas/", "#/definitions/", 1)
				}
				continue
			}
			rewriteSchemaRefs(val)
		}
	case []any:
		for _, e := range t {
			rewriteSchemaRefs(e)
		}
	}
}

// stripGenerated reduces a generated Go file to its type declarations: it drops
// the leading "// Code generated …" banner and the "package …" clause, then
// removes the throwaway sentinel root type. The result is gofmt-clean type decls
// the framework template injects and the whole file is re-formatted.
func stripGenerated(src, sentinel string) string {
	// Header banner, package clause, then the body are blank-line separated.
	if parts := strings.SplitN(src, "\n\n", 3); len(parts) == 3 {
		src = parts[2]
	}
	src = strings.ReplaceAll(src, "type "+sentinel+" map[string]any\n", "")
	return strings.TrimSpace(src)
}
