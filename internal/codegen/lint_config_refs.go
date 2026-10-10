package codegen

import (
	"fmt"
	"maps"
	"slices"
)

// lintConfigFileSchemaRefs rejects a `$ref` in a config_files entry's `schema` that names a
// schema the root command doesn't declare: the file could never be checked against it.
func lintConfigFileSchemaRefs(spec *Spec) []error {
	names := slices.Sorted(maps.Keys(spec.Command.Schemas))
	var problems []error
	walkCommandsAt(spec, func(c *Command, path, ptr string) {
		for i, cf := range c.ConfigFiles {
			if cf.Schema == nil {
				continue
			}
			reported := map[string]bool{}
			walkSchemaTreeAt(cf.Schema.BaseSchema, fmt.Sprintf("%s/config_files/%d/schema", ptr, i), "", func(b BaseSchema, at, _ string) {
				name := refTypeName(b.Ref)
				if name == "" || reported[name] {
					return
				}
				if _, ok := spec.Command.Schemas[name]; ok {
					return
				}
				reported[name] = true
				msg := fmt.Sprintf("config file %q: its schema's `$ref` %q points to an undeclared schema (no %q under the root command's `schemas`)", cf.Name, b.Ref, name)
				problems = append(problems, &problem{kind: "spec", ptr: at, loc: "command " + path, msg: didYouMean(msg, name, names)})
			})
		}
	})
	return problems
}
