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

// This file converts spec schemas to Go type declarations and to self-contained JSON
// Schemas for runtime validation.

// outputRootSentinel is the placeholder root type GenerateGo requires; it is stripped from
// the output.
const outputRootSentinel = "rotiniGeneratedOutputsRoot"

// buildOutputTypes generates Go type declarations for the program's named schemas and its
// own commands' "<Prefix>Output" and "<Prefix>Stdin" types (see collectOutputDefs), or ""
// when there are none. It uses jsonschema.GenerateGo, the engine behind the spec and conf
// types.
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

// initialismIdents applies initialism casing to generated field and nested type names
// ("apiVersion" → APIVersion, not ApiVersion); JSON tags are unchanged.
//
// Renames are applied by AST position, so a field and a type sharing a name are handled
// independently. Names in fixed (top-level types referenced elsewhere as written) are never
// renamed, and a rename that would collide with an existing name is skipped.
func initialismIdents(src string, fixed map[string]bool) (string, error) {
	const pkgClause = "package p\n\n"
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "", pkgClause+src, parser.ParseComments)
	if err != nil {
		return "", fmt.Errorf("parse generated types: %w", err)
	}
	// Index declared type names and each field ident's enclosing struct.
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

// outputTypeNames lists the top-level type names buildOutputTypes declares, sorted. It reads
// the same definitions, so the two cannot disagree.
func outputTypeNames(gp *program) []string {
	defs := collectOutputDefs(gp)
	out := make([]string, 0, len(defs))
	for name := range defs {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// eachOwnNode visits every non-composed node in the resolved tree in pre-order, skipping
// composed subtrees, whose types live in the child's package.
func eachOwnNode(nodes []rnode, visit func(n *rnode)) {
	for i := range nodes {
		if nodes[i].composed {
			continue
		}
		visit(&nodes[i])
		eachOwnNode(nodes[i].children, visit)
	}
}

// collectOutputDefs assembles the JSON Schema `definitions` for buildOutputTypes: each named
// schema, plus "<Prefix>Output" for each own command declaring an output and "<Prefix>Stdin"
// for each declaring a decoded (non-raw) stdin payload. "#/schemas/" refs become
// "#/definitions/".
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
	// Only the stdin schema's BaseSchema is type structure; required, default, etc. are
	// input metadata.
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

// pathFromClaim holds the config_source inputs claiming one configuration_files entry: a
// flag's logical name and/or an env input's variable.
type pathFromClaim struct {
	flag string
	env  string
}

// collectStdinSchemas maps each own command's "<Prefix>Stdin" name to the self-contained JSON
// Schema the input reader validates its stdin payload against, or returns nil when none
// declare a decoded stdin. A raw format (text, lines) binds the payload as is, with no
// <Prefix>Stdin type and nothing to validate, so it has no entry.
func collectStdinSchemas(gp *program) map[string]string {
	out := map[string]string{}
	add := func(prefix string, in *Inputs) {
		if in == nil || in.Stdin == nil || in.Stdin.Schema == nil || rawStdinFormat(in.Stdin.Format) {
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

// validationSchema renders a self-contained JSON Schema for runtime validation of stdin
// payloads, config files, and object flags: the declared shape plus the document's named
// schemas as definitions, so "#/schemas/X" refs resolve. It returns "" on failure.
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

// rewriteSchemaRefs recursively rewrites every {"$ref": "#/schemas/X"} in v to
// "#/definitions/X".
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

// stripGenerated reduces a generated Go file to its type declarations, dropping the
// "// Code generated" banner, the package clause, and the sentinel root type.
func stripGenerated(src, sentinel string) string {
	// Banner, package clause, and body are separated by blank lines.
	if parts := strings.SplitN(src, "\n\n", 3); len(parts) == 3 {
		src = parts[2]
	}
	src = strings.ReplaceAll(src, "type "+sentinel+" map[string]any\n", "")
	return strings.TrimSpace(src)
}
