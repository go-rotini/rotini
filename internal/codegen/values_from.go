package codegen

import (
	"fmt"
	"maps"
	"slices"
	"strings"
)

// An input's `values_from: output.tasks` takes its allowed values from the field names of the
// command's declared output. They are derived once, when the spec loads, into the input's
// `enum`, so help, completion, validation and the contract treat them as any other enum.

// resolveValuesFrom writes each flag's and argument's `values_from` field names into its enum.
// An input whose path does not resolve, or that declares values of its own, is left as it is
// for lintValuesFrom to report.
func resolveValuesFrom(s *Spec) {
	schemas := s.Command.Schemas
	walkCommands(s, func(c *Command, _ string) {
		if c.Output == nil {
			return
		}
		derive := func(schema *InputSchema) {
			if schema == nil || schema.ValuesFrom == "" || ownValues(schema) {
				return
			}
			if t := getSchemaType(schema); t != "string" && t != "[]string" {
				return
			}
			if names, err := valuesFromNames(c.Output, schemas, schema.ValuesFrom); err == nil {
				schema.Enum = make([]any, len(names))
				for i, n := range names {
					schema.Enum[i] = n
				}
			}
		}
		for i := range c.Flags {
			derive(c.Flags[i].Schema)
		}
		for i := range c.Arguments {
			derive(c.Arguments[i].Schema)
		}
	})
}

// ownValues reports whether an input declares its values itself: an enum, on the input or its
// items, or a named schema that may carry one.
func ownValues(schema *InputSchema) bool {
	return len(schema.Enum) > 0 || schema.Ref != "" || (schema.Items != nil && (len(schema.Items.Enum) > 0 || schema.Items.Ref != ""))
}

// valuesFromNames returns the property names, sorted, of the object path reaches in output.
func valuesFromNames(output *Schema, schemas map[string]Schema, path string) ([]string, error) {
	node, err := valuesFromNode(output, schemas, path)
	if err != nil {
		return nil, err
	}
	names := slices.Sorted(maps.Keys(node.Properties))
	for _, n := range names {
		if strings.Contains(n, ".") {
			return nil, fmt.Errorf("reaches a property named %q; a dot in a field name reads as a nested field, so values_from can't list it", n)
		}
	}
	return names, nil
}

// valuesFromNode follows path ("output", "output.tasks") through output: each segment after
// the first names a property, and an array met on the way, the output itself included, is
// stepped through to its items. The node reached must be an object with properties.
func valuesFromNode(output *Schema, schemas map[string]Schema, path string) (*Schema, error) {
	segs := strings.Split(path, ".")
	if segs[0] != "output" {
		return nil, fmt.Errorf("values_from %q must start with output", path)
	}
	node, err := valuesFromStep(output, schemas, "output")
	if err != nil {
		return nil, err
	}
	at := "output"
	for _, seg := range segs[1:] {
		if len(node.Properties) == 0 {
			return nil, fmt.Errorf("%s has no properties, so it has no field %q", at, seg)
		}
		prop, ok := node.Properties[seg]
		if !ok {
			return nil, fmt.Errorf("%s has no property %q; it has %s", at, seg, strings.Join(slices.Sorted(maps.Keys(node.Properties)), ", "))
		}
		at += "." + seg
		if node, err = valuesFromStep(&prop, schemas, at); err != nil {
			return nil, err
		}
	}
	if len(node.Properties) == 0 {
		return nil, fmt.Errorf("%s is not an object with properties, so it has no fields to list", at)
	}
	return node, nil
}

// valuesFromStep resolves s's $ref and steps through arrays to their items.
func valuesFromStep(s *Schema, schemas map[string]Schema, at string) (*Schema, error) {
	for {
		resolved := resolveShape(s, schemas)
		if resolved == nil {
			return nil, fmt.Errorf("%s refers to %s, which is not a named schema", at, s.Ref)
		}
		s = resolved
		if s.Type != "array" && !strings.HasPrefix(s.Type, "[]") {
			return s, nil
		}
		if s.Items == nil {
			return nil, fmt.Errorf("%s is a list whose items have no declared shape", at)
		}
		s = s.Items
	}
}

// lintValuesFrom checks `values_from`: on a flag or argument of a command that declares an
// output, of type string or a list of strings, with no enum of its own, not cascading, and a
// path that reaches an object whose property names have no dots.
func lintValuesFrom(spec *Spec) []error {
	var problems []error
	schemas := spec.Command.Schemas
	walkCommandsAt(spec, func(c *Command, path, ptr string) {
		cascading := map[string]bool{}
		for _, f := range c.Flags {
			cascading[f.Name] = f.Cascading
		}
		eachInputAt(c, ptr, func(channel, name, ptr string, schema *InputSchema) {
			if schema == nil || schema.ValuesFrom == "" {
				return
			}
			add := func(msg string) {
				problems = append(problems, inputProblem(ptr+"/schema/values_from", path, channel, name, msg))
			}
			if channel != "flag" && channel != "argument" {
				add("sets `values_from`, which applies to flags and arguments only")
				return
			}
			if c.Output == nil {
				add("sets `values_from`, but its command declares no `output:` to take field names from")
				return
			}
			if channel == "flag" && cascading[name] {
				add("sets `values_from` on a cascading flag; the commands below have outputs of their own, so declare the flag on each")
				return
			}
			if t := getSchemaType(schema); t != "string" && t != "[]string" {
				add(fmt.Sprintf("sets `values_from` but its type is %s; field names are strings, so use string or []string", t))
				return
			}
			names, err := valuesFromNames(c.Output, schemas, schema.ValuesFrom)
			if err != nil {
				add(err.Error())
				return
			}
			if schema.Ref != "" || (schema.Items != nil && schema.Items.Ref != "") || !slices.Equal(enumStrings(schema.Enum), names) ||
				(schema.Items != nil && len(schema.Items.Enum) > 0) {
				add("sets both `values_from` and its own values (`enum` or a named schema); keep one")
			}
		})
	})
	return problems
}

// lintFieldRoles checks the output roles: 'fields' marks a list of strings and 'sort' a string,
// each with the field names to choose from (an `enum` or `values_from`).
func lintFieldRoles(spec *Spec) []error {
	var problems []error
	walkCommandsAt(spec, func(c *Command, path, ptr string) {
		for i, f := range c.Flags {
			if f.Role != "fields" && f.Role != "sort" {
				continue
			}
			add := func(msg string) {
				problems = append(problems, inputProblem(fmt.Sprintf("%s/flags/%d/role", ptr, i), path, "flag", f.Name, msg))
			}
			want := map[string]string{"fields": "[]string", "sort": "string"}[f.Role]
			if t := getSchemaType(f.Schema); t != want {
				add(fmt.Sprintf("has role %s but is a %s; the %s role marks a %s flag", f.Role, t, f.Role, want))
				continue
			}
			s := f.Schema
			if s == nil || (s.ValuesFrom == "" && len(s.Enum) == 0 && (s.Items == nil || len(s.Items.Enum) == 0)) {
				add(fmt.Sprintf("has role %s but no field names to choose from; add `values_from: output` (or a path below it) or an `enum`", f.Role))
			}
		}
	})
	return problems
}

// valuesFrom returns an input's `values_from` path, or "", for the contract.
func valuesFrom(schema *InputSchema) string {
	if schema == nil {
		return ""
	}
	return schema.ValuesFrom
}
