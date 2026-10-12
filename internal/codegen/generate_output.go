package codegen

import (
	"maps"
	"slices"
	"strings"
)

// This file describes a command's `output:` schema (what it writes to stdout on success) for
// the generated help, man, and markdown pages.

// templateDocOutput is a command's Output section: the shape and its top-level fields.
type templateDocOutput struct {
	Description string                   // the shape's description, or the named schema's it references
	Type        string                   // the shape as a type name: TaskList, []Task, object
	Fields      []templateDocOutputField // the top-level fields of an object shape, by name
	Stream      bool                     // the command writes a stream of Type, one item at a time
	// For a stream whose items come in several kinds: the property naming each item's kind,
	// and the kinds in declared order. Type is then the kinds' values joined by " | ".
	Tag   string
	Kinds []templateDocOutputKind
}

// templateDocOutputKind is one kind of item in a stream whose items come in several kinds.
type templateDocOutputKind struct {
	Value       string                   // the tag's value for this kind
	Type        string                   // the named schema a `$ref` kind is; "" for one written inline
	Description string                   // the kind's description, or its named schema's
	Fields      []templateDocOutputField // its top-level fields, by name
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
	if tag, kinds := outputKinds(shape, schemas); kinds != nil {
		d := &templateDocOutput{Description: shape.Description, Tag: tag}
		values := make([]string, len(kinds))
		for i, k := range kinds {
			values[i] = k.value
			description := k.decl.Description
			if description == "" {
				description = k.shape.Description
			}
			d.Kinds = append(d.Kinds, templateDocOutputKind{Value: k.value, Type: k.ref, Description: description, Fields: shapeFields(&k.shape)})
		}
		d.Type = strings.Join(values, " | ")
		return d
	}
	d := &templateDocOutput{Type: shapeTypeName(shape), Description: shape.Description}
	resolved := resolveShape(shape, schemas)
	if d.Description == "" && resolved != nil {
		d.Description = resolved.Description
	}
	if resolved != nil {
		d.Fields = shapeFields(resolved)
	}
	return d
}

// shapeFields lists an object shape's top-level fields, by name.
func shapeFields(s *Schema) []templateDocOutputField {
	if len(s.Properties) == 0 {
		return nil
	}
	fields := make([]templateDocOutputField, 0, len(s.Properties))
	for _, name := range slices.Sorted(maps.Keys(s.Properties)) {
		p := s.Properties[name]
		fields = append(fields, templateDocOutputField{
			Name:        name,
			Type:        shapeTypeName(&p),
			Description: p.Description,
			Required:    slices.Contains(s.Required, name),
		})
	}
	return fields
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

// shapeTypeName names a shape for display: the schema it references (TaskList), a list of one
// ([]Task), "object" for an untyped shape with properties, or its declared type.
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

// markStream marks an Output section as a stream of its type (output_stream).
func markStream(d *templateDocOutput, stream bool) {
	if d != nil {
		d.Stream = stream
	}
}
