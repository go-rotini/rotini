package codegen

import "fmt"

// lintStdinSchema requires a schema with a shape on a stdin read in a document format: the
// payload decodes into the generated <Prefix>Stdin type, which the schema describes, so without
// one no Stdin field is generated and the declared stdin silently does nothing. The raw formats
// imply their schema (see impliedStdinSchemas).
func lintStdinSchema(spec *Spec) []error {
	var problems []error
	walkCommandsAt(spec, func(c *Command, path, ptr string) {
		if c.Stdin == nil || rawStdinFormat(c.Stdin.Format) {
			return
		}
		if s := c.Stdin.Schema; s != nil && (s.Type != "" || s.Ref != "") {
			return
		}
		format := c.Stdin.Format
		if format == "" {
			format = "json"
		}
		problems = append(problems, inputProblem(ptr+"/stdin", path, "stdin", "",
			fmt.Sprintf("`format` %q decodes the payload into a typed value, but no schema `type` or `$ref` describes it, so no Stdin field would be generated; declare its schema (a `$ref` to a named schema, or `type: object` with properties), or use `format: text` or `lines` to read it raw", format)))
	})
	return problems
}
