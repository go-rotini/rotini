package internal

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/go-rotini/jsonschema"
)

// outputRootSentinel is the throwaway root type GenerateGo always emits for the
// assembled output-types document; it carries no data and is stripped, leaving
// only the document-level named schemas and the per-command <Prefix>Output types.
const outputRootSentinel = "rotiniGeneratedOutputsRoot"

// buildOutputTypes generates the Go type declarations for a program's output
// types and named schemas as a formatted source fragment (no package clause, no
// root type) ready to inject into the framework file. It returns "" when the
// program declares no schemas and no command outputs.
//
// Every document-level schema becomes a named type, and every command (root + own
// sub-commands) that declares `output` gets a "<Prefix>Output" type — an alias-like
// named type when the output is a bare `$ref`, or a struct for an inline shape.
// Generation reuses jsonschema.GenerateGo (the same engine behind the spec/conf
// types), so refs, nesting, arrays, and allOf embedding all work.
func buildOutputTypes(gp *genProgram, pkg string) (string, error) {
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

// collectOutputDefs assembles the JSON-schema `definitions` for the output-types
// document: each document-level named schema, plus one "<Prefix>Output" per
// command that declares an output. Refs are rewritten from the spec's
// "#/schemas/" space to the document's "#/definitions/" space.
func collectOutputDefs(gp *genProgram) map[string]any {
	defs := map[string]any{}
	for name, sch := range gp.schemas {
		defs[name] = schemaToDoc(sch)
	}
	add := func(prefix string, out *Schema) {
		if out != nil {
			defs[prefix+"Output"] = schemaToDoc(*out)
		}
	}
	add(gp.rootPascal, gp.rootOutput)
	var walk func(nodes []rnode)
	walk = func(nodes []rnode) {
		for _, n := range nodes {
			add(n.prefix, n.output)
			walk(n.children)
		}
	}
	walk(gp.tree)
	return defs
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
