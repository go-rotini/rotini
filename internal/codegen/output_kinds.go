package codegen

import (
	"encoding/json"
	"fmt"
	"maps"
	"math"
	"slices"
	"strconv"
	"unicode"
)

// A stream whose items come in several kinds declares `output:` as `oneOf:` one object schema
// per kind. Every kind declares the same property, its tag, as a required string holding a
// `const` that names the kind. This file finds the tag and the kinds, for lint, the generated
// types and the runtime's OutputDef.

// outputKind is one kind of item in a stream whose items come in several kinds.
type outputKind struct {
	value string // the tag's value for this kind
	ref   string // the named schema a `$ref` kind is; "" for a kind written inline
	shape Schema // the kind's object schema, its `$ref` followed
	decl  Schema // the kind as written under oneOf
}

// typeName is the kind's generated Go type: its named schema's, or <Prefix>Output<Value> for
// a kind written inline. outputType is the command's <Prefix>Output.
func (k outputKind) typeName(outputType string) string {
	if k.ref != "" {
		return k.ref
	}
	return outputType + toPascalCase(k.value)
}

// shapeAt is a schema with its JSON pointer in the spec.
type shapeAt struct {
	schema Schema
	ptr    string
}

// outputShapesAt lists what an output declares: the output itself, or each kind under its
// oneOf, with each one's pointer below ptr. It is nil for no output.
func outputShapesAt(out *Schema, ptr string) []shapeAt {
	if out == nil {
		return nil
	}
	if len(out.OneOf) == 0 {
		return []shapeAt{{*out, ptr}}
	}
	shapes := make([]shapeAt, len(out.OneOf))
	for i, k := range out.OneOf {
		shapes[i] = shapeAt{k, ptr + "/oneOf/" + strconv.Itoa(i)}
	}
	return shapes
}

// outputKinds returns the tag property and the kinds of an output declared as oneOf, in
// declared order, or "" and nil for any other output. It assumes the spec passed lint.
func outputKinds(out *Schema, schemas map[string]Schema) (string, []outputKind) {
	if out == nil || len(out.OneOf) == 0 {
		return "", nil
	}
	a := analyzeKinds(out, schemas)
	if len(a.tags) != 1 {
		return "", nil
	}
	tag := a.tags[0]
	kinds := make([]outputKind, len(out.OneOf))
	for i, decl := range out.OneOf {
		s := a.shapes[i]
		value, _ := s.Properties[tag].Const.(string)
		kinds[i] = outputKind{value: value, ref: refTypeName(decl.Ref), shape: s, decl: decl}
	}
	return tag, kinds
}

// kindAnalysis is what analyzeKinds finds in a oneOf output.
type kindAnalysis struct {
	shapes     []Schema // each kind's schema, its `$ref` followed; zero when unresolved
	objects    []bool   // whether each kind is an object schema
	unresolved []bool   // a `$ref` naming no declared schema, reported by lintSchemaRefs
	candidates []map[string]bool
	tags       []string // the properties every object kind could be tagged by, sorted
}

// analyzeKinds resolves each kind and lists the properties that could be the tag: a property
// every kind declares as a required string holding a string `const`.
func analyzeKinds(out *Schema, schemas map[string]Schema) kindAnalysis {
	n := len(out.OneOf)
	a := kindAnalysis{shapes: make([]Schema, n), objects: make([]bool, n), unresolved: make([]bool, n), candidates: make([]map[string]bool, n)}
	var common map[string]bool
	for i, decl := range out.OneOf {
		s := decl
		if decl.Ref != "" {
			named, ok := schemas[refTypeName(decl.Ref)]
			if !ok {
				a.unresolved[i] = true
				continue
			}
			s = named
		}
		a.shapes[i] = s
		a.objects[i] = len(s.OneOf) == 0 && (s.Type == "object" || (s.Type == "" && s.Ref == "" && len(s.Properties) > 0))
		if !a.objects[i] {
			continue
		}
		a.candidates[i] = tagCandidates(s)
		if common == nil {
			common = maps.Clone(a.candidates[i])
			continue
		}
		for p := range common {
			if !a.candidates[i][p] {
				delete(common, p)
			}
		}
	}
	a.tags = slices.Sorted(maps.Keys(common))
	return a
}

// tagCandidates lists an object schema's properties that are required strings holding a
// string const.
func tagCandidates(s Schema) map[string]bool {
	out := map[string]bool{}
	for name, p := range s.Properties {
		if _, ok := p.Const.(string); ok && p.Type == "string" && slices.Contains(s.Required, name) {
			out[name] = true
		}
	}
	return out
}

// lintOutputKinds checks an output declared as oneOf: it must be a stream, stand alone, and
// list object kinds told apart by exactly one tag property with a different value in each.
func lintOutputKinds(spec *Spec) []error {
	var problems []error
	walkCommandsAt(spec, func(c *Command, path, ptr string) {
		if c.Output == nil || len(c.Output.OneOf) == 0 {
			return
		}
		add := func(at, msg string) {
			problems = append(problems, &problem{kind: "spec", ptr: at, loc: "command " + path, msg: msg})
		}
		at := ptr + "/output"
		if !c.OutputStream {
			add(at+"/oneOf", "lists kinds of item under `oneOf`, which only a stream may do; set `output_stream: true` to write items of several kinds one at a time, or declare one shape as `output`")
		}
		if beside := keysBesideOneOf(*c.Output); len(beside) > 0 {
			add(at, fmt.Sprintf("declares %s beside `oneOf`; `oneOf` stands alone on `output` (with an optional `description`), so move them into each kind", keyList(beside)))
		}
		a := analyzeKinds(c.Output, spec.Command.Schemas)
		for i := range c.Output.OneOf {
			if !a.unresolved[i] && !a.objects[i] {
				add(fmt.Sprintf("%s/oneOf/%d", at, i), fmt.Sprintf("`oneOf` entry %d is not an object schema; each kind of item is an object with a tag property naming its kind", i+1))
			}
		}
		for i := range c.Output.OneOf {
			if a.unresolved[i] || !a.objects[i] {
				return // the tag can't be worked out until every kind is an object
			}
		}
		switch len(a.tags) {
		case 0:
			problems = append(problems, missingTagProblems(c.Output, a, at, path)...)
			return
		case 1:
		default:
			add(at+"/oneOf", fmt.Sprintf("every kind could be told apart by any of %s; exactly one property may be a required string with a `const` in every kind, so drop the `const` from the others or leave them out of `required`", keyList(a.tags)))
			return
		}
		tag := a.tags[0]
		seen := map[string]int{}
		types := map[string]int{}
		for i, decl := range c.Output.OneOf {
			value, _ := a.shapes[i].Properties[tag].Const.(string)
			kat := fmt.Sprintf("%s/oneOf/%d", at, i)
			if j, dup := seen[value]; dup {
				add(kat, fmt.Sprintf("`oneOf` entries %d and %d both set %#q to %q; each kind needs its own value", j+1, i+1, tag, value))
				continue
			}
			seen[value] = i
			if decl.Ref != "" {
				continue
			}
			suffix := toPascalCase(value)
			if !identSuffix(suffix) {
				add(kat, fmt.Sprintf("`oneOf` entry %d's %#q value %q can't name its generated Go type; use letters, digits, '-' or '_'", i+1, tag, value))
				continue
			}
			if j, dup := types[suffix]; dup {
				add(kat, fmt.Sprintf("`oneOf` entries %d and %d would both generate the type <Prefix>Output%s; give the kinds values that differ in more than punctuation or case", j+1, i+1, suffix))
				continue
			}
			types[suffix] = i
		}
	})
	return problems
}

// missingTagProblems explains why no property tags every kind: a property that tags most of
// them is reported on each kind that lacks it, and with none, the oneOf itself is.
func missingTagProblems(out *Schema, a kindAnalysis, at, path string) []error {
	counts := map[string]int{}
	for _, c := range a.candidates {
		for p := range c {
			counts[p]++
		}
	}
	best := ""
	for _, p := range slices.Sorted(maps.Keys(counts)) {
		if counts[p] > counts[best] {
			best = p
		}
	}
	if best == "" {
		return []error{&problem{kind: "spec", ptr: at + "/oneOf", loc: "command " + path,
			msg: "no property says which kind an item is; give every kind the same property as a required string with a `const` naming the kind, such as `kind: {type: string, const: progress}` listed in `required`"}}
	}
	var problems []error
	for i := range out.OneOf {
		if a.candidates[i][best] {
			continue
		}
		problems = append(problems, &problem{kind: "spec", ptr: fmt.Sprintf("%s/oneOf/%d", at, i), loc: "command " + path,
			msg: fmt.Sprintf("`oneOf` entry %d has no %#q tagging its kind, which the other kinds have; declare %#q as a string with a `const` naming the kind, and list it in `required`", i+1, best, best)})
	}
	return problems
}

// lintOutputSatisfiable rejects an output shape that no output could pass: an object whose
// `required` names a property it doesn't declare, or a property whose `const` is not a value
// of its own type (`const: 3` on a string). It checks a command's output, each kind of a
// stream's items and each exit status's output; input schemas are not output shapes.
func lintOutputSatisfiable(spec *Spec) []error {
	var problems []error
	walkCommandsAt(spec, func(c *Command, path, ptr string) {
		check := func(subject string, out *Schema, shapes []shapeAt) {
			for k, s := range shapes {
				walkOutputShapeAt(s.schema, s.ptr, "", func(node Schema, at, prop string) {
					who := subject
					if len(out.OneOf) > 0 {
						who += fmt.Sprintf(" `oneOf` entry %d", k+1)
					}
					if prop != "" {
						who += " property " + strconv.Quote(prop)
					}
					add := func(at, msg string) {
						problems = append(problems, &problem{kind: "spec", ptr: at, loc: "command " + path, msg: who + ": " + msg})
					}
					if len(node.Properties) > 0 {
						declared := slices.Sorted(maps.Keys(node.Properties))
						for i, name := range node.Required {
							if _, ok := node.Properties[name]; !ok {
								add(fmt.Sprintf("%s/required/%d", at, i), didYouMean(fmt.Sprintf(
									"`required` names %q, which isn't one of its `properties`, so no output can pass; declare the property, or take it out of `required`", name), name, declared))
							}
						}
					}
					if msg := constTypeProblem(node); msg != "" {
						add(at+"/const", msg)
					}
				})
			}
		}
		check("output", c.Output, outputShapesAt(c.Output, ptr+"/output"))
		for i, e := range c.ExitStatus {
			check(fmt.Sprintf("exit_status %d output", e.Code), e.Output, outputShapesAt(e.Output, fmt.Sprintf("%s/exit_status/%d/output", ptr, i)))
		}
	})
	return problems
}

// walkOutputShapeAt visits s and every property and list element schema below it, with each
// one's pointer and dotted property path ("" for s itself).
func walkOutputShapeAt(s Schema, ptr, path string, visit func(s Schema, ptr, path string)) {
	visit(s, ptr, path)
	for _, k := range slices.Sorted(maps.Keys(s.Properties)) {
		sub := k
		if path != "" {
			sub = path + "." + k
		}
		walkOutputShapeAt(s.Properties[k], ptr+"/properties/"+k, sub, visit)
	}
	if s.Items != nil {
		walkOutputShapeAt(*s.Items, ptr+"/items", path+"[]", visit)
	}
}

// constTypeProblem says why a schema's `const` can't be a value of its declared `type`, or
// returns "". A type that isn't one of JSON Schema's own or rotini's (a Go type with `import`)
// is not checked.
func constTypeProblem(s Schema) string {
	if s.Const == nil || s.Type == "" || s.Ref != "" {
		return ""
	}
	want, _ := standardType(s.Type, map[string]any{})["type"].(string)
	got := jsonKind(s.Const)
	if want == "" || got == "" {
		return ""
	}
	if got == want || (want == "number" && got == "integer") {
		return ""
	}
	shown := fmt.Sprint(s.Const)
	if text, ok := s.Const.(string); ok {
		shown = strconv.Quote(text)
	}
	return fmt.Sprintf("`const` %s is %s, but the type is %s, so no output can pass; make the `const` a value of the type, or change the type", shown, jsonKindNames[got], displayType(s.Type))
}

// jsonKind is the JSON Schema type of a decoded value: "integer" for a whole number, "number"
// for any other, or "" for a value no JSON document holds.
func jsonKind(v any) string {
	switch x := v.(type) {
	case string:
		return "string"
	case bool:
		return "boolean"
	case int, int64, uint64:
		return "integer"
	case float64:
		if x == math.Trunc(x) && !math.IsInf(x, 0) {
			return "integer"
		}
		return "number"
	case json.Number:
		if _, err := x.Int64(); err == nil {
			return "integer"
		}
		return "number"
	case []any:
		return "array"
	case map[string]any:
		return "object"
	}
	return ""
}

// jsonKindNames describes each JSON Schema type for a message.
var jsonKindNames = map[string]string{
	"string": "a string", "boolean": "a boolean", "integer": "a whole number", "number": "a number",
	"array": "a list", "object": "an object",
}

// keysBesideOneOf lists the keys an output declares beside its oneOf, other than description.
func keysBesideOneOf(s Schema) []string {
	plain := s
	plain.OneOf, plain.Description = nil, ""
	var keys []string
	raw := schemaToDoc(plain)
	if m, ok := raw.(map[string]any); ok {
		keys = slices.Sorted(maps.Keys(m))
	}
	return keys
}

// identSuffix reports whether s can end a Go identifier.
func identSuffix(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r != '_' && !unicode.IsLetter(r) && !unicode.IsDigit(r) {
			return false
		}
	}
	return true
}
