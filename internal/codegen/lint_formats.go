package codegen

import (
	"fmt"
	"maps"
	"slices"
)

// builtinConfigFormats are the configuration file formats rotini ships a codec for.
var builtinConfigFormats = []string{"json", "yaml", "toml", "jsonc", "dotenv"}

// builtinStdinFormats are the stdin formats rotini reads itself.
var builtinStdinFormats = []string{"json", "yaml", "jsonc", "toml", "text", "lines", "jsonl", "bytes"}

// lintFormatNames warns about a config_files or stdin `format` rotini doesn't ship. The name
// is valid (the program adds its decoder with rotini.Codec), but generate can't wire it, and a
// typo would otherwise surface only when a run reads the file.
func lintFormatNames(spec *Spec) []error {
	var problems []error
	warn := func(ptr, path, subject, format string) {
		problems = append(problems, &problem{
			kind: "spec", ptr: ptr, loc: "command " + path, sev: severityWarning,
			msg: fmt.Sprintf("%s: format %q is not built in; the program must add rotini.Codec(%q, …) with WithFeatures, since generate can't wire it", subject, format, format),
		})
	}
	walkCommandsAt(spec, func(c *Command, path, ptr string) {
		for i, cf := range c.ConfigFiles {
			if cf.Format != "" && !slices.Contains(builtinConfigFormats, cf.Format) {
				warn(fmt.Sprintf("%s/config_files/%d/format", ptr, i), path, fmt.Sprintf("config_files %q", cf.Name), cf.Format)
			}
		}
		if c.Stdin != nil && c.Stdin.Format != "" && !slices.Contains(builtinStdinFormats, c.Stdin.Format) {
			warn(ptr+"/stdin/format", path, "stdin", c.Stdin.Format)
		}
	})
	return problems
}

// lintAdditionalProperties rejects an `additionalProperties` that would not do what it says:
// beside `$ref`, which JSON Schema reads without its siblings; on a schema that is not an
// object; and on an object with no `properties`, which would allow no key at all.
func lintAdditionalProperties(spec *Spec) []error {
	var problems []error
	walkCommandsAt(spec, func(c *Command, path, ptr string) {
		eachDocumentSchema(c, ptr, func(s Schema, at string) {
			if s.AdditionalProperties == nil {
				return
			}
			var why string
			switch {
			case s.Ref != "":
				why = "sits beside `$ref`, which is read without the keys beside it; write it on the named schema"
			case s.Type != "" && s.Type != "object":
				why = fmt.Sprintf("is on a schema of type %s; it applies only to an object", s.Type)
			case len(s.Properties) == 0:
				why = "is on an object schema with no `properties`; list the keys it allows under `properties`"
			default:
				return
			}
			problems = append(problems, &problem{kind: "spec", ptr: at + "/additionalProperties", loc: "command " + path,
				msg: "`additionalProperties` " + why})
		})
	})
	return problems
}

// eachDocumentSchema visits every object-style schema a command declares, with its JSON
// pointer: each config file's schema, its named schemas, its output and exit status outputs,
// and the properties and items of its inputs' schemas, recursing through properties, items and
// oneOf.
func eachDocumentSchema(c *Command, ptr string, visit func(s Schema, ptr string)) {
	var walk func(s Schema, at string)
	walk = func(s Schema, at string) {
		visit(s, at)
		walkSchemaChildren(s.BaseSchema, at, walk)
		for i, k := range s.OneOf {
			walk(k, fmt.Sprintf("%s/oneOf/%d", at, i))
		}
	}
	for i, cf := range c.ConfigFiles {
		if cf.Schema != nil {
			walk(*cf.Schema, fmt.Sprintf("%s/config_files/%d/schema", ptr, i))
		}
	}
	for _, name := range slices.Sorted(maps.Keys(c.Schemas)) {
		walk(c.Schemas[name], ptr+"/schemas/"+name)
	}
	if c.Output != nil {
		walk(*c.Output, ptr+"/output")
	}
	for i, e := range c.ExitStatus {
		if e.Output != nil {
			walk(*e.Output, fmt.Sprintf("%s/exit_status/%d/output", ptr, i))
		}
	}
	eachInputAt(c, ptr, func(_, _, at string, schema *InputSchema) {
		if schema != nil {
			walkSchemaChildren(schema.BaseSchema, at+"/schema", walk)
		}
	})
}

// walkSchemaChildren calls walk on each of b's properties, in sorted order, and on its items.
func walkSchemaChildren(b BaseSchema, at string, walk func(s Schema, at string)) {
	for _, k := range slices.Sorted(maps.Keys(b.Properties)) {
		walk(b.Properties[k], at+"/properties/"+escapePointer(k))
	}
	if b.Items != nil {
		walk(*b.Items, at+"/items")
	}
}
