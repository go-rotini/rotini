package codegen

import (
	"maps"
	"slices"
	"strings"
)

// The output shape, as the generated pages describe it: what a command writes to stdout when it
// succeeds, from its `output:`.

// templateDocOutput is a command's Output section: the shape and its top-level fields.
type templateDocOutput struct {
	Description string                   // the shape's description, or the named schema's it references
	Type        string                   // the shape as a type name: TaskList, []Task, object
	Fields      []templateDocOutputField // the top-level fields of an object shape, by name
}

// templateDocOutputField is one top-level field of an output shape.
type templateDocOutputField struct {
	Name        string
	Type        string
	Description string
	Required    bool
}

// outputDoc describes a command's output for its pages, or returns nil when it declares none.
func outputDoc(shape *Schema, schemas map[string]Schema) *templateDocOutput {
	if shape == nil {
		return nil
	}
	d := &templateDocOutput{Type: shapeTypeName(shape), Description: shape.Description}
	resolved := resolveShape(shape, schemas)
	if d.Description == "" && resolved != nil {
		d.Description = resolved.Description
	}
	if resolved == nil || len(resolved.Properties) == 0 {
		return d
	}
	for _, name := range slices.Sorted(maps.Keys(resolved.Properties)) {
		p := resolved.Properties[name]
		d.Fields = append(d.Fields, templateDocOutputField{
			Name:        name,
			Type:        shapeTypeName(&p),
			Description: p.Description,
			Required:    slices.Contains(resolved.Required, name),
		})
	}
	return d
}

// resolveShape follows a shape's $ref to the document-level schema it names, so its fields can
// be listed. A shape that is not a reference is returned as is; an unknown reference yields nil.
func resolveShape(s *Schema, schemas map[string]Schema) *Schema {
	if s == nil || s.Ref == "" {
		return s
	}
	named, ok := schemas[strings.TrimPrefix(s.Ref, "#/schemas/")]
	if !ok {
		return nil
	}
	return &named
}

// shapeTypeName names a shape the way a reader thinks of it: the schema it references (TaskList),
// a list of one ([]Task), or its JSON type.
func shapeTypeName(s *Schema) string {
	switch {
	case s == nil:
		return ""
	case s.Ref != "":
		return s.Ref[strings.LastIndex(s.Ref, "/")+1:]
	case s.Type == "array" || strings.HasPrefix(s.Type, "[]"):
		if s.Items != nil {
			return "[]" + shapeTypeName(s.Items)
		}
		return s.Type
	case s.Type == "" && len(s.Properties) > 0:
		return "object"
	default:
		return s.Type
	}
}
