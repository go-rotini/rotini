package codegen

// This file describes a command's `stdin:` (what it reads from standard input) for the
// generated help, man and markdown pages.

// templateDocStdin is a command's Stdin section: the format, the shape and its top-level
// fields.
type templateDocStdin struct {
	Format      string // json, yaml, jsonc, toml, text or lines
	Type        string // the shape as a type name: TaskList, object, string, []string
	Description string // the shape's description, or the named schema's it references
	Required    bool   // empty stdin is an error
	Fields      []templateDocOutputField
}

// stdinDoc describes a command's stdin for its pages, or returns nil when it declares none. A
// raw format (text, lines) reads a string or its lines, so it has no fields.
func stdinDoc(s *StdinSpec, schemas map[string]Schema) *templateDocStdin {
	if s == nil {
		return nil
	}
	d := &templateDocStdin{Format: s.Format}
	if d.Format == "" {
		d.Format = "json"
	}
	if s.Schema != nil {
		d.Required = s.Schema.Required
	}
	switch d.Format {
	case "text":
		d.Type = "string"
		return d
	case "lines":
		d.Type = "[]string"
		return d
	}
	if s.Schema == nil {
		return d
	}
	if shape := outputDoc(&Schema{BaseSchema: s.Schema.BaseSchema}, schemas); shape != nil {
		d.Type, d.Description, d.Fields = shape.Type, shape.Description, shape.Fields
	}
	return d
}
