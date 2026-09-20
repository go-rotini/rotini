package codegen

import (
	"encoding/json"
	"fmt"
	"maps"
	"sort"
	"strings"
)

// Rendering the embedded JSON Schemas as Markdown reference pages.
//
// rotini's real documentation has always lived in the schemas: every key carries a paragraph
// saying what it does, what it rejects, and why — and TestReferenceDocsValidate keeps them
// honest by failing the build when they drift. What the published site had instead was a
// hand-written skeleton: 974 lines of prose for a 119-key schema, which no one could keep in
// step by hand and no one did.
//
// So the site's reference pages are GENERATED from the same bytes validation judges. There is
// no second copy to drift, writing a key documents it, and TestSchemaDocsInSync fails until
// the pages are regenerated.

// schemaDoc is the subset of JSON Schema the renderer reads. It is deliberately not a full
// Draft-7 model: these are rotini's own schemas, whose shapes are known.
type schemaDoc struct {
	Title       string               `json:"title"`
	Description string               `json:"description"`
	Type        any                  `json:"type"`
	Required    []string             `json:"required"`
	Properties  map[string]schemaDoc `json:"properties"`
	Definitions map[string]schemaDoc `json:"definitions"`
	Items       *schemaDoc           `json:"items"`
	Ref         string               `json:"$ref"`
	Enum        []any                `json:"enum"`
	Default     any                  `json:"default"`
	Pattern     string               `json:"pattern"`
	AllOf       []schemaDoc          `json:"allOf"`
	AnyOf       []schemaDoc          `json:"anyOf"`
}

// renderSchemaMarkdown renders one schema as a Hugo content page: the document's own keys
// first, then a section per definition, each a table of keys with their full descriptions.
func renderSchemaMarkdown(title, weight string, raw []byte) (string, error) {
	var doc schemaDoc
	if err := json.Unmarshal(raw, &doc); err != nil {
		return "", fmt.Errorf("parse schema for %s: %w", title, err)
	}

	var b strings.Builder
	fmt.Fprintf(&b, "---\ntitle: %q\nweight: %s\n---\n\n", title, weight)
	fmt.Fprintf(&b, "<!-- Code generated from the rotini JSON Schema; DO NOT EDIT.\n")
	fmt.Fprintf(&b, "     Edit the schema's descriptions instead, then run:\n")
	fmt.Fprintf(&b, "       go test ./internal/codegen -run SchemaDocs -update-schema-docs -->\n\n")

	fmt.Fprintf(&b, "# %s\n\n", doc.Title)
	if doc.Description != "" {
		fmt.Fprintf(&b, "%s\n\n", doc.Description)
	}
	b.WriteString("Every key below is checked by `rotini validate` before a line of Go is\n")
	b.WriteString("generated. This page is rendered from the schema itself, so it cannot drift\n")
	b.WriteString("from what the tool actually accepts.\n\n")

	b.WriteString("## Document\n\n")
	b.WriteString(renderKeyTable(doc, doc.Required))

	names := make([]string, 0, len(doc.Definitions))
	for name := range doc.Definitions {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		def := doc.Definitions[name]
		fmt.Fprintf(&b, "\n## %s\n\n", name)
		if d := flattenedDescription(def); d != "" {
			fmt.Fprintf(&b, "%s\n\n", d)
		}
		b.WriteString(renderKeyTable(flattenAllOf(def), flattenedRequired(def)))
	}
	return b.String(), nil
}

// renderKeyTable renders a schema's properties as a definition list — one entry per key, its
// type and whether it is required on one line, and the schema's own paragraph beneath.
//
// A list rather than a table: these descriptions are paragraphs, and a table cell is the wrong
// shape for a paragraph.
func renderKeyTable(doc schemaDoc, required []string) string {
	if len(doc.Properties) == 0 {
		return "_No keys._\n"
	}
	req := map[string]bool{}
	for _, r := range required {
		req[r] = true
	}
	names := make([]string, 0, len(doc.Properties))
	for name := range doc.Properties {
		names = append(names, name)
	}
	sort.Strings(names)

	var b strings.Builder
	for _, name := range names {
		p := doc.Properties[name]
		fmt.Fprintf(&b, "### `%s`\n\n", name)

		var facts []string
		if t := typeLabel(p); t != "" {
			facts = append(facts, t)
		}
		if req[name] {
			facts = append(facts, "**required**")
		}
		if len(p.Enum) > 0 {
			facts = append(facts, "one of "+joinLiterals(p.Enum))
		}
		if p.Default != nil {
			facts = append(facts, fmt.Sprintf("default `%v`", p.Default))
		}
		if len(facts) > 0 {
			fmt.Fprintf(&b, "%s\n\n", strings.Join(facts, " · "))
		}
		if d := flattenedDescription(p); d != "" {
			fmt.Fprintf(&b, "%s\n\n", d)
		}
	}
	return b.String()
}

// typeLabel renders a property's type for the one-line fact row, following a $ref to the
// definition it names so a reader can jump to it.
func typeLabel(p schemaDoc) string {
	if p.Ref != "" {
		name := p.Ref[strings.LastIndex(p.Ref, "/")+1:]
		return fmt.Sprintf("[`%s`](#%s)", name, strings.ToLower(name))
	}
	switch t := p.Type.(type) {
	case string:
		if t == "array" && p.Items != nil {
			return "array of " + strings.TrimPrefix(typeLabel(*p.Items), "`")
		}
		return "`" + t + "`"
	case []any:
		parts := make([]string, 0, len(t))
		for _, v := range t {
			parts = append(parts, fmt.Sprintf("`%v`", v))
		}
		return strings.Join(parts, " or ")
	}
	if len(p.AllOf) > 0 || len(p.AnyOf) > 0 {
		return "`object`"
	}
	return ""
}

// flattenAllOf merges an allOf-composed definition into one shape, so a schema written as
// "the object, plus an if/then" documents as the object. rotini uses allOf for inheritance
// (Schema and InputSchema over BaseSchema) and for conditional requirements.
func flattenAllOf(doc schemaDoc) schemaDoc {
	if len(doc.AllOf) == 0 {
		return doc
	}
	out := doc
	if out.Properties == nil {
		out.Properties = map[string]schemaDoc{}
	}
	merged := make(map[string]schemaDoc, len(out.Properties))
	maps.Copy(merged, out.Properties)
	for _, part := range doc.AllOf {
		maps.Copy(merged, flattenAllOf(part).Properties)
	}
	out.Properties = merged
	return out
}

// flattenedRequired collects the required keys of a definition and of its allOf members,
// skipping the conditional branches, whose requirements apply only in that branch.
func flattenedRequired(doc schemaDoc) []string {
	out := append([]string(nil), doc.Required...)
	for _, part := range doc.AllOf {
		out = append(out, flattenedRequired(part)...)
	}
	return out
}

// flattenedDescription returns a schema's description, or the first one its allOf members
// carry when the outer shape has none.
func flattenedDescription(doc schemaDoc) string {
	if doc.Description != "" {
		return doc.Description
	}
	for _, part := range doc.AllOf {
		if d := flattenedDescription(part); d != "" {
			return d
		}
	}
	return ""
}

// joinLiterals renders an enum as inline code, comma-separated.
func joinLiterals(vals []any) string {
	parts := make([]string, 0, len(vals))
	for _, v := range vals {
		parts = append(parts, fmt.Sprintf("`%v`", v))
	}
	return strings.Join(parts, ", ")
}
