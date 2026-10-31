package codegen

import (
	"fmt"
	"slices"
	"strings"
)

// reservedChannelNames are the names a declared channel can't take: the built-in outcomes, and
// the key every structured line starts with.
var reservedChannelNames = []string{"info", "success", "warning", "error", "panic", "schema_version"}

// lintChannels checks the root's declared channels: each name unique and not reserved, each
// schema an object schema the record type and line can be made from, and an error channel's
// schema carrying the required string `message` its generated Error method returns.
func lintChannels(spec *Spec) []error {
	var problems []error
	seen := map[string]int{}
	for i, c := range spec.Command.Channels {
		ptr := fmt.Sprintf("%s/channels/%d", rootPointer, i)
		loc := fmt.Sprintf("%s channel %q", rootLabel(spec), c.Name)
		add := func(at, msg string) {
			problems = append(problems, &problem{kind: "spec", ptr: at, loc: loc, msg: msg})
		}
		if slices.Contains(reservedChannelNames, c.Name) {
			add(ptr+"/name", fmt.Sprintf("%q is reserved (%s); choose another name", c.Name, strings.Join(reservedChannelNames, ", ")))
		}
		if prev, dup := seen[c.Name]; dup && c.Name != "" {
			add(ptr+"/name", fmt.Sprintf("is declared twice (also channels[%d]); a channel is known by its name", prev))
		} else {
			seen[c.Name] = i
		}
		switch c.Class {
		case "", "info", "success", "warning", "error":
		default:
			add(ptr+"/class", fmt.Sprintf("unknown class %q; use info, success, warning or error", c.Class))
		}
		shape := &c.Schema
		if c.Schema.Ref != "" {
			if shape = resolveShape(&c.Schema, spec.Command.Schemas); shape == nil {
				continue // lintSchemaRefs reports an unknown reference
			}
		}
		if shape.Import != "" || shape.Ref != "" || (shape.Type != "object" && (shape.Type != "" || len(shape.Properties) == 0)) {
			add(ptr+"/schema", "a record is a JSON object: give the schema `type: object` and its properties, or `$ref` an object schema")
			continue
		}
		if c.Class == "error" {
			msg, ok := shape.Properties["message"]
			if !ok || msg.Ref != "" || msg.Type != "string" || msg.Nullable || !slices.Contains(shape.Required, "message") {
				add(ptr+"/schema", "an error channel's record needs a required, non-nullable string property `message`: its generated Error method returns it, so the record can join the run's error")
			}
		}
	}
	return problems
}
