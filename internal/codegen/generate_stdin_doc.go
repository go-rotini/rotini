package codegen

// This file describes a command's `stdin:` (what it reads from standard input) for the
// generated help, man and markdown pages.

// templateDocStdin is a command's Stdin section: the format, the shape and its top-level
// fields, and how it is read.
type templateDocStdin struct {
	Format      string // json, yaml, jsonc, toml, jsonl, text, lines or bytes
	Type        string // the shape as a type name: TaskList, object, string, []string; one record for jsonl
	Description string // the shape's description, or the named schema's it references
	Required    bool   // empty stdin is an error
	Stream      bool   // read one line or record at a time
	NUL         bool   // lines are separated by NUL bytes
	Unless      string // the file argument (as usage shows it) whose value means stdin is not read
	Fields      []templateDocOutputField
}

// stdinDocFor describes the stdin a command's inputs declare, or returns nil when they declare
// none.
func stdinDocFor(in *Inputs, schemas map[string]Schema) *templateDocStdin {
	d := stdinDoc(in.Stdin, schemas)
	if d == nil || in.Stdin.UnlessArgument == "" {
		return d
	}
	d.Unless = "<" + in.Stdin.UnlessArgument + ">"
	for _, a := range in.Arguments {
		if a.Name == in.Stdin.UnlessArgument && a.Schema != nil && a.Schema.Placeholder != "" {
			d.Unless = "<" + a.Schema.Placeholder + ">"
		}
	}
	return d
}

// stdinDoc describes a command's stdin for its pages, or returns nil when it declares none. A
// raw format (text, lines, bytes) reads a string, its lines or its bytes, so it has no fields.
func stdinDoc(s *StdinSpec, schemas map[string]Schema) *templateDocStdin {
	if s == nil {
		return nil
	}
	d := &templateDocStdin{Format: s.Format, Stream: s.Stream, NUL: s.Separator == "nul"}
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
	case "bytes":
		d.Type = "[]byte"
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
