package codegen

import (
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"maps"
	"reflect"
	"slices"
	"strconv"
	"strings"

	"github.com/go-rotini/jsonschema/gogen"
)

// lintGoNames rejects spec names that generate the same Go identifier, or none, since the
// generated code would not compile or would marshal wrongly. Names that differ only in case,
// "-" or "_" make one Go name: two commands' type prefixes, two inputs' fields in one struct,
// or two properties' fields in one object schema when the schema generator gives them one.
// A property name must also make a field and a json tag on its own.
func lintGoNames(spec *Spec) []error {
	var problems []error
	problems = append(problems, commandPrefixClashes(spec)...)
	walkCommandsAt(spec, func(c *Command, path, ptr string) {
		problems = append(problems, inputFieldClashes(spec, c, path, ptr)...)
	})
	problems = append(problems, propertyNameProblems(spec)...)
	return problems
}

// commandPrefixClashes reports a command whose generated type prefix (root name plus its
// path, PascalCased) another command already makes. Composed children name their own types.
func commandPrefixClashes(spec *Spec) []error {
	var problems []error
	eachOwnPrefix(spec, func(_ *Command, path, ptr, prefix, prev string) {
		if prev != "" {
			problems = append(problems, &problem{kind: "spec", ptr: ptr, loc: "command " + path, msg: fmt.Sprintf(
				"generates the Go name %s, which command %s also generates (a Go name capitalizes each word, drops \"-\" and \"_\", and joins the path's names, so two paths can make one); rename one",
				prefix, prev)})
		}
	})
	return problems
}

// eachOwnPrefix visits each command of spec that names its own generated types (composed
// children name theirs) with its slash path, pointer and type prefix, and the path of an
// earlier command that makes the same prefix ("" when none does).
func eachOwnPrefix(spec *Spec, visit func(c *Command, path, ptr, prefix, prev string)) {
	rootPascal := toPascalCase(spec.Command.Name)
	first := map[string]string{} // prefix -> the command that made it first
	var walk func(c *Command, names []string, ptr string)
	walk = func(c *Command, names []string, ptr string) {
		prefix := rootPascal + toPascalCase(strings.Join(names[1:], "-"))
		path := strings.Join(names, "/")
		prev := first[prefix]
		if prev == "" {
			first[prefix] = path
		}
		visit(c, path, ptr, prefix, prev)
		for i := range c.Commands {
			child := &c.Commands[i]
			if child.Ref != "" || child.Name == "" {
				continue
			}
			walk(child, append(slices.Clip(names), child.Name), fmt.Sprintf("%s/commands/%d", ptr, i))
		}
	}
	walk(&spec.Command, []string{spec.Command.Name}, rootPointer)
}

// lintGeneratedNames rejects two spec entries whose generated top-level Go names coincide,
// where neither is a clash of command prefixes (commandPrefixClashes reports those): a
// command's suffixed types (<Prefix>Inputs, <Prefix>CommandInputs, ...) against another's, a
// named schema against them, an exit constant against them, and any of these against the
// fixed names the cmd file declares, including the enabled doc features' resolvers and
// variables. Generate would refuse the same code without a position.
func lintGeneratedNames(spec *Spec, conf *Conf) []error {
	if spec == nil || spec.Command.Name == "" {
		return nil
	}
	var problems []error
	taken := map[string]string{} // name -> what declares it
	// claim records name for owner; own is how the subject's message names it.
	claim := func(name, own, owner, ptr, loc string) {
		if prev, dup := taken[name]; dup {
			problems = append(problems, &problem{kind: "spec", ptr: ptr, loc: loc, msg: fmt.Sprintf(
				"generates the Go name %s (%s), which %s also declares; rename one", name, own, prev)})
			return
		}
		taken[name] = owner
	}
	features := fixedGoNames(conf, taken)
	schemas := spec.Command.Schemas
	for _, name := range slices.Sorted(maps.Keys(schemas)) {
		if name == "Usage" {
			continue // lintUsageSchemaName's to report
		}
		claim(name, "its type", fmt.Sprintf("schema %q", name), rootPointer+"/schemas/"+name, fmt.Sprintf("%s schema %q", rootLabel(spec), name))
	}
	eachOwnPrefix(spec, func(c *Command, path, ptr, prefix, prev string) {
		if prev != "" {
			return
		}
		loc := "command " + path
		for _, sfx := range commandTypeSuffixes(c) {
			claim(prefix+sfx, "its "+sfx+" type", fmt.Sprintf("command %s's %s type", path, sfx), ptr, loc)
		}
		for _, f := range features {
			if !f.perShell {
				claim(f.varPrefix+prefix, "its "+f.noun+" variable", fmt.Sprintf("command %s's %s variable", path, f.noun), ptr, loc)
			}
		}
		for i, e := range c.ExitStatus {
			if e.Name != "" {
				claim(prefix+"Exit"+toPascalCase(e.Name), fmt.Sprintf("its exit constant for %q", e.Name),
					fmt.Sprintf("command %s's exit constant for %q", path, e.Name), fmt.Sprintf("%s/exit_status/%d", ptr, i), loc)
			}
		}
	})
	return problems
}

// commandTypeSuffixes lists the suffixes of the types generated under c's prefix.
func commandTypeSuffixes(c *Command) []string {
	suffixes := []string{"Inputs", "CommandInputs", "Flags", "Arguments"}
	if len(c.Env) > 0 {
		suffixes = append(suffixes, "Env")
	}
	if len(c.Config) > 0 {
		suffixes = append(suffixes, "Config")
	}
	if c.Output != nil {
		suffixes = append(suffixes, "Output")
	}
	if c.Stdin != nil && c.Stdin.Schema != nil && !rawStdinFormat(c.Stdin.Format) {
		suffixes = append(suffixes, "Stdin")
	}
	return suffixes
}

// fixedGoNames records in taken the top-level names the cmd file declares whatever the spec
// says, including those of the doc features conf enables, and returns those features.
func fixedGoNames(conf *Conf, taken map[string]string) []docFeature {
	for _, name := range []string{"NewProgram", "NewProgramWith", "Handlers"} {
		taken[name] = "the generated " + name + " function"
	}
	taken["ProgramHandlers"] = "the generated ProgramHandlers interface"
	taken["InputSettings"] = "the generated InputSettings variable"
	var features []docFeature
	if conf != nil {
		for _, f := range []docFeature{helpFeatureDesc, manFeatureDesc, markdownFeatureDesc, completionFeatureDesc} {
			feat := conf.Generate.featureOf(f.name)
			if feat == nil || !feat.Enabled {
				continue
			}
			features = append(features, f)
			by := "the " + f.name + " feature's "
			taken[f.resolver] = by + f.resolver + " function"
			if f.pagesFunc != "" {
				taken[f.pagesFunc] = by + f.pagesFunc + " function"
			}
			if f.manPages {
				taken["ManSection"] = by + "ManSection constant"
			}
			if f.perShell {
				for _, sh := range completionShells {
					v := f.varPrefix + toPascalCase(sh)
					taken[v] = by + v + " variable"
				}
			}
		}
	}
	return features
}

// inputFieldClashes reports an input whose generated struct field another input of the same
// channel on c already makes under a different name. Same names are lintDuplicateInputNames's
// to report, and flags from different flag sets lintFlagSets's.
func inputFieldClashes(spec *Spec, c *Command, path, ptr string) []error {
	var problems []error
	origins := flagOrigins(c, spec.Command.FlagSets)
	type seen struct {
		name string
		at   int
	}
	fields := map[string]seen{} // channel+field -> first input
	idx := map[string]int{}     // channel -> position within the channel
	eachInputAt(c, ptr, func(channel, name, at string, _ *InputSchema) {
		i := idx[channel]
		idx[channel]++
		if name == "" {
			return
		}
		field := toPascalCase(name)
		key := channel + "\x00" + field
		prev, dup := fields[key]
		if !dup {
			fields[key] = seen{name, i}
			return
		}
		if prev.name == name || (channel == "flag" && origins[prev.at].set != origins[i].set) {
			return
		}
		problems = append(problems, inputProblem(at, path, channel, name, fmt.Sprintf(
			"generates the struct field %s, which %s %q also generates (names that differ only in case, \"-\" or \"_\" make the same Go name); rename one",
			field, channel, prev.name)))
	})
	return problems
}

// propertyNameProblems reports object-schema properties, in named schemas, outputs and stdin
// payloads, that clash on one generated field or can't make a field and json tag at all. The
// fields are the ones the schema generator really makes.
func propertyNameProblems(spec *Spec) []error {
	var problems []error
	tree := func(loc, ptr string, root BaseSchema) {
		walkSchemaTreeAt(root, ptr, "", func(b BaseSchema, ptr, path string) {
			if len(b.Properties) == 0 {
				return
			}
			keys := slices.Sorted(maps.Keys(b.Properties))
			fields := generatedFieldNames(slices.DeleteFunc(slices.Clone(keys), untaggable))
			first := map[string]string{} // field -> the property that made it first
			for _, k := range keys {
				subject := "property " + quoteName(path, k)
				at := ptr + "/properties/" + k
				field := fields[k]
				switch {
				case field == "" || untaggable(k):
					problems = append(problems, &problem{kind: "spec", ptr: at, loc: loc, msg: subject +
						": can't become a Go field and json tag; start it with a letter, and leave out backticks, '\"' and ','"})
				case first[field] != "":
					problems = append(problems, &problem{kind: "spec", ptr: at, loc: loc, msg: fmt.Sprintf(
						"%s: generates the struct field %s, which property %q also generates (names that differ only in case or punctuation make the same Go name); rename one",
						subject, field, first[field])})
				default:
					first[field] = k
				}
			}
		})
	}
	schemas := spec.Command.Schemas
	for _, name := range slices.Sorted(maps.Keys(schemas)) {
		tree(fmt.Sprintf("%s schema %q", rootLabel(spec), name), rootPointer+"/schemas/"+name, schemas[name].BaseSchema)
	}
	walkCommandsAt(spec, func(c *Command, path, ptr string) {
		for _, s := range outputShapesAt(c.Output, ptr+"/output") {
			tree("command "+path+" output", s.ptr, s.schema.BaseSchema)
		}
		if c.Stdin != nil && c.Stdin.Schema != nil {
			tree("command "+path+" stdin", ptr+"/stdin/schema", c.Stdin.Schema.BaseSchema)
		}
	})
	return problems
}

// quoteName renders a property for a message: its dotted path within the schema, quoted.
func quoteName(path, key string) string {
	if path == "" {
		return fmt.Sprintf("%q", key)
	}
	return fmt.Sprintf("%q", path+"."+key)
}

// untaggable reports whether a property key holds a character that breaks its json tag.
func untaggable(key string) bool {
	return strings.ContainsAny(key, "`\",")
}

// generatedFieldNames returns the struct field the schema generator makes for each key when
// the keys are one object's properties, or "" for a key it makes no named field for. It asks
// the generator rotini is built with, so it reflects that version's naming: whether two keys
// share a field, and which keys make none.
func generatedFieldNames(keys []string) map[string]string {
	if fields, ok := probeFieldNames(keys); ok {
		return fields
	}
	var usable []string // the generator refused the object: keep the keys it accepts alone
	for _, k := range keys {
		if _, ok := probeFieldNames([]string{k}); ok {
			usable = append(usable, k)
		}
	}
	fields, _ := probeFieldNames(usable)
	return fields
}

// probeFieldNames generates an object whose properties are keys and reads each key's field
// name back by its json tag. It reports false when the generator refuses the object or emits
// source that doesn't parse.
func probeFieldNames(keys []string) (map[string]string, bool) {
	props := make(map[string]any, len(keys))
	for _, k := range keys {
		props[k] = map[string]any{"type": "string"}
	}
	doc, err := json.Marshal(map[string]any{
		"$schema": "http://json-schema.org/draft-07/schema#", "type": "object", "properties": props,
	})
	if err != nil {
		return nil, false
	}
	src, err := gogen.Generate(doc, gogen.WithPackage("p"), gogen.WithRootType("Root"))
	if err != nil {
		return nil, false
	}
	file, err := parser.ParseFile(token.NewFileSet(), "", src, 0)
	if err != nil {
		return nil, false
	}
	fields := make(map[string]string, len(keys))
	ast.Inspect(file, func(n ast.Node) bool {
		f, ok := n.(*ast.Field)
		if !ok || f.Tag == nil {
			return true
		}
		tag, err := strconv.Unquote(f.Tag.Value)
		if err != nil {
			return true
		}
		key, _, _ := strings.Cut(reflect.StructTag(tag).Get("json"), ",")
		if len(f.Names) == 1 {
			fields[key] = f.Names[0].Name
		} else {
			fields[key] = ""
		}
		return true
	})
	return fields, true
}
