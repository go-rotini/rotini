package codegen

import (
	"errors"
	"fmt"
	"reflect"
	"strings"

	"github.com/go-rotini/rotini"
)

// lintRepeatable rejects `repeatable` where it means nothing: on any input but a flag, and as
// `repeatable: false` on a flag whose occurrences are its values (a list or map), its count,
// or the fields of one object. `repeatable: true` is the default and always allowed.
func lintRepeatable(spec *Spec) []error {
	var problems []error
	walkCommandsAt(spec, func(c *Command, path, ptr string) {
		eachInputAt(c, ptr, func(channel, name, ptr string, schema *InputSchema) {
			if schema == nil || schema.Repeatable == nil {
				return
			}
			add := func(msg string) { problems = append(problems, inputProblem(ptr, path, channel, name, msg)) }
			if channel != "flag" {
				add("sets `repeatable`, which applies only to flags given on the command line; here it would do nothing")
				return
			}
			if *schema.Repeatable {
				return
			}
			typ, _ := inputValueType(schema, spec.Command.Schemas)
			switch {
			case objectRef(schema, spec.Command.Schemas) != "":
				add("sets `repeatable: false`, but an object flag's occurrences merge into one value (or, for a list, each is an element); remove it")
			case schema.Type == "count":
				add("sets `repeatable: false`, but a count flag is its number of occurrences; remove it")
			case strings.HasPrefix(typ, "[]") || strings.HasPrefix(typ, "map["):
				add("sets `repeatable: false`, but a list or map flag takes one value per occurrence; remove it, or use `maxItems: 1`")
			}
		})
	})
	return problems
}

// lintUniqueItems rejects `uniqueItems` where it can't apply (anything but a list, and under
// `items`, since it is a rule about the whole list) and a list default that repeats a value,
// compared as the runtime compares it.
func lintUniqueItems(spec *Spec) []error {
	var problems []error
	walkCommandsAt(spec, func(c *Command, path, ptr string) {
		eachInputAt(c, ptr, func(channel, name, ptr string, schema *InputSchema) {
			if schema == nil || channel == "stdin" {
				return
			}
			add := func(msg string) { problems = append(problems, inputProblem(ptr, path, channel, name, msg)) }
			if schema.Items != nil && schema.Items.UniqueItems {
				add("sets `uniqueItems` under `items`, but it is a rule about the whole list; move it up beside `type`")
			}
			if !schema.UniqueItems {
				return
			}
			typ, _ := inputValueType(schema, spec.Command.Schemas)
			if !strings.HasPrefix(typ, "[]") || schema.Type == "count" {
				add(fmt.Sprintf("sets `uniqueItems`, which applies to list types only, not %s; it would be silently ignored", typeSpelling(typ)))
				return
			}
			if objectRef(schema, spec.Command.Schemas) != "" {
				return
			}
			if dup := duplicateDefault(schema); dup != "" {
				add(fmt.Sprintf("`default` repeats %s, but `uniqueItems` is set; %s", dup, defaultFails))
			}
		})
	})
	return problems
}

// duplicateDefault reports the value a list default holds twice, as the runtime would compare
// it, or "" when none repeats or the type can't be built here.
func duplicateDefault(schema *InputSchema) string {
	values := defaultList(schema.Default)
	if len(values) < 2 {
		return ""
	}
	rt, ok := reflectTypeOf(getSchemaType(schema))
	if !ok {
		return ""
	}
	fd := rotini.FlagDef{
		Name: "v", Identifiers: []string{"--v"}, Type: definitionType(schema, nil),
		Enum: enumStrings(schema.Enum), EnumValues: runtimeEnumValues(schema.Enum), IgnoreCase: schema.IgnoreCase,
		UniqueItems: true,
	}
	withTimeForms(&fd, schema)
	argv := make([]string, len(values))
	for i, v := range values {
		argv[i] = "--v=" + v
	}
	flags := reflect.StructOf([]reflect.StructField{{Name: "V", Type: rt, Tag: `rotini:"v"`}})
	cmd := reflect.StructOf([]reflect.StructField{
		{Name: "Flags", Type: flags},
		{Name: "Arguments", Type: reflect.TypeFor[struct{}]()},
	})
	out := reflect.New(reflect.StructOf([]reflect.StructField{{Name: "App", Type: cmd}}))
	def := rotini.Definition{Name: "app", Handler: "App", Flags: []rotini.FlagDef{fd}}
	err := rotini.NewParser().Parse(lintContext(def, argv), out.Interface())
	var pe *rotini.ParseError
	if !errors.As(err, &pe) || pe.Kind != rotini.ParseKindConstraintViolation {
		return ""
	}
	_, got, ok := strings.Cut(pe.Msg, "(got ")
	if !ok {
		return ""
	}
	return strings.TrimSuffix(strings.TrimSuffix(got, ")"), " twice")
}
