package codegen

import (
	"encoding/json"
	"slices"
	"strconv"
	"strings"
)

// This file turns the spec's examples into tool input examples: the parameters a tool call
// passes to run the same command line. MCP carries them as the input schema's `examples`; the
// OpenAI and Gemini formats have no field for them, so their descriptions list the same
// examples' command lines.

// toolExample is one example of a command, with what its command line sets.
type toolExample struct {
	Example

	use exampleUse
}

// exampleUses returns what each example of the program's own spec sets, keyed by the path of
// the command it runs below the root (space separated), in spec order. Only examples whose
// first command running the program has literal words, parses, and runs a command of this
// spec are kept.
func (p *program) exampleUses() map[string][]toolExample {
	out := map[string][]toolExample{}
	if p.spec == nil {
		return out
	}
	check := newExampleChecker(p.spec)
	walkCommandsAt(p.spec, func(c *Command, _, _ string) {
		for _, ex := range examplesOf(c.Examples) {
			res := check.check(ex.Run)
			if res.use == nil || res.msg != "" {
				continue
			}
			names := make([]string, 0, len(res.use.chain)-1)
			for _, cmd := range res.use.chain[1:] {
				names = append(names, cmd.Name)
			}
			key := strings.Join(names, " ")
			out[key] = append(out[key], toolExample{Example: ex, use: *res.use})
		}
	})
	return out
}

// inputExamples returns the tool calls that run the examples, and the examples they run,
// leaving out each one a call can't express: an input that isn't a parameter, a literal
// secret, a required parameter it leaves out, or a value the parameter's schema can't hold as
// written.
func (t *tool) inputExamples(examples []toolExample) (calls []any, shown []Example) {
	for _, ex := range examples {
		if params, ok := t.exampleParams(ex.use); ok {
			calls = append(calls, params)
			shown = append(shown, ex.Example)
		}
	}
	return calls, shown
}

// describedExamples is description followed by the examples' command lines, each with its
// summary as a trailing comment, for the formats with no field for examples:
//
//	Examples:
//	  taskr add "buy milk"  # add a task
func describedExamples(description string, examples []Example) string {
	if len(examples) == 0 {
		return description
	}
	var b strings.Builder
	if description != "" {
		b.WriteString(description)
		b.WriteString("\n\n")
	}
	b.WriteString("Examples:")
	for _, ex := range examples {
		b.WriteString("\n  ")
		b.WriteString(strings.TrimSpace(ex.Run))
		if ex.Summary != "" {
			b.WriteString("  # ")
			b.WriteString(ex.Summary)
		}
	}
	return b.String()
}

// exampleParams writes what u sets as the tool's parameters.
func (t *tool) exampleParams(u exampleUse) (map[string]any, bool) {
	params := map[string]any{}
	for _, au := range u.arguments() {
		s, ok := t.properties[au.arg.Name].(map[string]any)
		if !ok {
			return nil, false
		}
		var v any
		if typeIs(s, "array") {
			list := make([]any, 0, len(au.values))
			for _, w := range au.values {
				item, ok := toolValue(itemSchema(s), au.arg.Schema, w)
				if !ok {
					return nil, false
				}
				list = append(list, item)
			}
			v = list
		} else {
			if len(au.values) != 1 {
				return nil, false
			}
			if v, ok = toolValue(s, au.arg.Schema, au.values[0]); !ok {
				return nil, false
			}
		}
		params[au.arg.Name] = v
	}
	for _, fu := range u.flags {
		f := fu.flag
		if f.Role == "machine-output" {
			continue // every call adds it
		}
		s, ok := t.properties[f.Name].(map[string]any)
		if !ok || slices.ContainsFunc(u.arguments(), func(au exampleArgUse) bool { return au.arg.Name == f.Name }) {
			return nil, false
		}
		if !setFlagParam(params, f, s, fu) {
			return nil, false
		}
	}
	for _, name := range t.required {
		if _, ok := params[name]; !ok {
			return nil, false
		}
	}
	return params, true
}

// setFlagParam sets the parameter one flag use gives, reporting false when its schema can't hold
// the value.
func setFlagParam(params map[string]any, f *FlagInput, s map[string]any, fu exampleFlagUse) bool {
	switch {
	case isCountFlag(*f):
		n, _ := params[f.Name].(int)
		params[f.Name] = n + 1
		return true
	case fu.value == nil:
		if !typeIs(s, "boolean") {
			return false
		}
		params[f.Name] = !fu.negated
		return true
	case typeIs(s, "array"):
		words := []exampleWord{*fu.value}
		if sep := f.Schema.Separator; sep != "" {
			words = words[:0]
			for item := range strings.SplitSeq(fu.value.text, runtimeSeparator(sep)) {
				words = append(words, exampleWord{text: item})
			}
		}
		list, _ := params[f.Name].([]any)
		for _, w := range words {
			v, ok := toolValue(itemSchema(s), f.Schema, w)
			if !ok {
				return false
			}
			list = append(list, v)
		}
		params[f.Name] = list
		return true
	case typeIs(s, "object"):
		k, val, ok := strings.Cut(fu.value.text, "=")
		values, isMap := s["additionalProperties"].(map[string]any)
		if !ok || !isMap || exampleSecretLiteral(f.Schema, *fu.value) {
			return false
		}
		v, ok := toolValue(values, nil, exampleWord{text: val})
		if !ok {
			return false
		}
		m, _ := params[f.Name].(map[string]any)
		if m == nil {
			m = map[string]any{}
		}
		m[k] = v
		params[f.Name] = m
		return true
	}
	v, ok := toolValue(s, f.Schema, *fu.value)
	if ok {
		params[f.Name] = v
	}
	return ok
}

// itemSchema is an array schema's items schema, or an empty one.
func itemSchema(s map[string]any) map[string]any {
	items, _ := s["items"].(map[string]any)
	return items
}

// toolValue reads one word as a value of the parameter schema s, for an input declared with in
// (nil for a map's value): a number or a bool for those types, an enum's canonical spelling for
// one of its aliases, else the word. It reports false for a literal secret, or a word s can't
// hold.
func toolValue(s map[string]any, in *InputSchema, w exampleWord) (any, bool) {
	if w.opaque || (in != nil && exampleSecretLiteral(in, w)) {
		return nil, false
	}
	text := w.text
	switch {
	case typeIs(s, "integer"):
		n, err := strconv.ParseInt(text, 10, 64)
		if err != nil {
			if n, err = strconv.ParseInt(text, 0, 64); err != nil { // a prefixed base: 0x1f
				return nil, false
			}
		}
		return n, true
	case typeIs(s, "number"):
		if _, err := strconv.ParseFloat(text, 64); err != nil || !json.Valid([]byte(text)) {
			return nil, false
		}
		return json.Number(text), true
	case typeIs(s, "boolean"):
		b, err := strconv.ParseBool(text)
		return b, err == nil
	case typeIs(s, "string"):
		if enum, ok := s["enum"].([]any); ok {
			v := canonicalEnum(in, text)
			return v, slices.Contains(enum, any(v))
		}
		return text, true
	}
	return nil, false
}

// canonicalEnum returns the enum value text spells, through an alias or ignore_case, or text.
func canonicalEnum(in *InputSchema, text string) string {
	if in == nil {
		return text
	}
	same := func(a, b string) bool { return a == b || (in.IgnoreCase && strings.EqualFold(a, b)) }
	for _, v := range enumValues(in.Enum) {
		if same(text, v.Value) || slices.ContainsFunc(v.Aliases, func(a string) bool { return same(text, a) }) {
			return v.Value
		}
	}
	return text
}
