package codegen

import (
	"fmt"
	"strings"
)

// lintCarapaceValues warns, when the carapace feature is on, about what the carapace spec
// can't carry and leaves out: an enum value starting with "$", which carapace would run as a
// macro, and an enum value or completion message holding " ||| ", which carapace would split
// into modifiers.
func lintCarapaceValues(spec *Spec, conf *Conf) []error {
	if spec == nil || conf == nil || conf.Generate == nil {
		return nil
	}
	if f := conf.Generate.featureOf("carapace"); f == nil || !f.Enabled {
		return nil
	}
	var problems []error
	warn := func(at, key, path, channel, name, msg string) {
		p := schemaKeyProblem(at, key, path, channel, name, msg)
		p.sev = severityWarning
		problems = append(problems, p)
	}
	walkCommandsAt(spec, func(c *Command, path, ptr string) {
		eachInputAt(c, ptr, func(channel, name, at string, schema *InputSchema) {
			if schema == nil || channel == "stdin" {
				return
			}
			for j, v := range enumValues(enumOrItems(schema)) {
				switch {
				case strings.HasPrefix(v.Value, "$"):
					warn(at, fmt.Sprintf("enum/%d", j), path, channel, name, "has the enum value "+quoteValue(v.Value)+
						", which the carapace spec leaves out: carapace runs a value starting with \"$\" as a macro")
				case strings.Contains(v.Value, carapaceModifierSep):
					warn(at, fmt.Sprintf("enum/%d", j), path, channel, name, "has the enum value "+quoteValue(v.Value)+
						", which the carapace spec leaves out: carapace splits a value at \" ||| \" into modifiers")
				}
			}
			if cm := schema.Complete; cm != nil && strings.Contains(cm.Message, carapaceModifierSep) {
				warn(at, "complete/message", path, channel, name, "has a completion message holding \" ||| \", which the carapace spec leaves out: carapace would split it into modifiers")
			}
		})
	})
	return problems
}

// quoteValue quotes a value for a message.
func quoteValue(v string) string { return "\"" + strings.ReplaceAll(v, "\"", "\\\"") + "\"" }
