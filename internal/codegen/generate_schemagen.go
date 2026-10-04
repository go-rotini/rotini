package codegen

import (
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
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
	fixed := map[string]bool{}
	for name := range defs {
		fixed[name] = true
	}
	return initialismIdents(stripGenerated(string(src), outputRootSentinel), fixed)
}

// initialismIdents gives the generated types' Go names the initialism casing the rest of the
// generated file has: a property "apiVersion" becomes the field APIVersion, not ApiVersion.
// The JSON tags keep the property's own spelling, so nothing about the wire format changes.
//
// It renames by position rather than by text, so a field and a type that share a name are each
// renamed as what they are. fixed holds the top-level type names: those are the spec's schema
// names, or rotini's own "<Prefix>Output", and are referenced from elsewhere in the program
// exactly as written. A rename that would collide with a name already declared is skipped.
func initialismIdents(src string, fixed map[string]bool) (string, error) {
	const pkgClause = "package p\n\n"
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "", pkgClause+src, parser.ParseComments)
	if err != nil {
		return "", fmt.Errorf("parse generated types: %w", err)
	}
	// Which names are types, and which idents are field names (and in which struct).
	types := map[string]bool{}
	fieldOf := map[*ast.Ident]*ast.StructType{}
	ast.Inspect(file, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.TypeSpec:
			types[x.Name.Name] = true
		case *ast.StructType:
			for _, f := range x.Fields.List {
				for _, id := range f.Names {
					fieldOf[id] = x
				}
			}
		}
		return true
	})
	declared := func(st *ast.StructType, name string) bool {
		for _, f := range st.Fields.List {
			for _, id := range f.Names {
				if id.Name == name {
					return true
				}
			}
		}
		return false
	}
	type edit struct {
		off, end int
		name     string
	}
	var edits []edit
	ast.Inspect(file, func(n ast.Node) bool {
		id, ok := n.(*ast.Ident)
		if !ok {
			return true
		}
		renamed := initialismCase(id.Name)
		if renamed == id.Name {
			return true
		}
		if st, isField := fieldOf[id]; isField {
			if declared(st, renamed) {
				return true
			}
		} else if !types[id.Name] || fixed[id.Name] || types[renamed] {
			return true
		}
		off := fset.Position(id.Pos()).Offset
		edits = append(edits, edit{off, off + len(id.Name), renamed})
		return true
	})
	out := pkgClause + src
	sort.Slice(edits, func(i, j int) bool { return edits[i].off > edits[j].off })
	for _, e := range edits {
		out = out[:e.off] + e.name + out[e.end:]
	}
	return strings.TrimPrefix(out, pkgClause), nil
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

// collectStdinSchemas builds the per-command stdin validation schemas for InputSettings: each own
// command declaring a stdin payload maps its "<Prefix>Stdin" type name to a self-contained
// JSON Schema the input reader validates the decoded payload against.
func collectStdinSchemas(gp *program) map[string]string {
	out := map[string]string{}
	add := func(prefix string, in *Inputs) {
		if in == nil || in.Stdin == nil || in.Stdin.Schema == nil {
			return
		}
		// Only the schema's shape (its BaseSchema) validates, matching the generated type.
		if js := validationSchema(Schema{BaseSchema: in.Stdin.Schema.BaseSchema}, gp.schemas); js != "" {
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
