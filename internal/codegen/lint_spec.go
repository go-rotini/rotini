package codegen

import (
	"fmt"
	"go/ast"
	"go/parser"
	"maps"
	"math"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// The spec lint stage: rotini-specific rules the JSON Schema cannot express. Each
// rule is a pure func(*Spec) []error registered in specLints.

// lintSpec runs every spec rule plus the composed-$ref tree check and positions each
// problem in source. It assumes the spec is schema-valid.
func (p *Processor) lintSpec(rs *reconciledSpec) []error {
	problems := make([]error, 0, len(specLints))
	for _, rule := range specLints {
		problems = append(problems, rule(rs.spec)...)
	}
	problems = append(problems, lintComposedTree(rs.spec, rs.path)...)
	locateProblems(problems, rs.path, rs.locate)
	return problems
}

// specLints is the ordered set of spec rules. The order is observable (problems are
// reported in rule order), so keep it stable.
var specLints = []func(*Spec) []error{
	lintRootCommand,
	lintRootAliases,
	lintDocLevelKeys,
	lintRefNodeKeys,
	lintHandlerSource,
	lintImportConsistency,
	lintLocalTimeout,
	lintFlagGroups,
	lintFlagDependencies,
	lintDuplicateFlagIdentifiers,
	lintSchemaRefs,
	lintHandlerFilenames,
	lintSiblingCollisions,
	lintDuplicateInputNames,
	lintVariadicArguments,
	lintDeprecatedIdentifiers,
	lintPluginTimeouts,
	lintDottedKeys,
	lintFrom,
	lintConfigurationFiles,
	lintConfigFilesScope,
	lintConfigSource,
	lintEnvNesting,
	lintConfigInputFiles,
	lintConstraintApplicability,
	lintCountFlags,
	lintPassthrough,
	lintPatternCompiles,
	lintSchemaTypes,
	lintVariable,
	lintNegatable,
	lintShortCircuit,
	lintStdinFormat,
	lintComplete,
	lintDefaultScalar,
	lintDefaultConstraints,
	lintItemConstraints,
	lintRequiredArgumentOrder,
	lintIgnoreCase,
	lintSeparator,
	lintImplicitValue,
	lintValuesParse,
	lintObjectFlags,
	lintLayout,
}

// lintRootCommand requires the root command, which is the binary itself, to have a
// name and forbids it from being composed via $ref.
func lintRootCommand(spec *Spec) []error {
	var problems []error
	loc := rootLabel(spec)
	if spec.Command.Ref != "" {
		problems = append(problems, &problem{kind: "spec", ptr: rootPointer + "/$ref", loc: loc, msg: "the root command cannot use `$ref`; compose child specs as sub-commands instead"})
	}
	if spec.Command.Name == "" {
		problems = append(problems, &problem{kind: "spec", ptr: rootPointer, loc: loc, msg: "the root command must have a `name` (it is the binary name)"})
	}
	return problems
}

// rootLabel names the root command in a problem: `command <name>`, or `command (root)`
// when it has no name.
func rootLabel(spec *Spec) string {
	if spec.Command.Name == "" {
		return "command (root)"
	}
	return "command " + spec.Command.Name
}

// lintDocLevelKeys rejects env_prefix, schemas and display_name on a non-root command:
// codegen reads them only on the root, so elsewhere they would be silently ignored.
func lintDocLevelKeys(spec *Spec) []error {
	var problems []error
	walkCommandsAt(spec, func(c *Command, path, ptr string) {
		if c == &spec.Command {
			return
		}
		add := func(key string) {
			problems = append(problems, &problem{
				kind: "spec", ptr: ptr + "/" + key, loc: "command " + path,
				msg: fmt.Sprintf("sets %#q, a root-command-level key valid only on the root command; remove it (codegen reads it only at the root, so here it is silently ignored)", key),
			})
		}
		if c.EnvPrefix != "" {
			add("env_prefix")
		}
		if c.Schemas != nil {
			add("schemas")
		}
		if c.DisplayName != "" {
			add("display_name")
		}
	})
	return problems
}

// lintRefNodeKeys rejects handler-coupled keys (inputs, output, plugins, passthrough) on a
// `$ref` node. The child's handler is generated against the child's own inputs and output,
// so an overlay would yield a parser that handler does not match. Only identity and
// presentation keys plus additive `commands:` may be overlaid.
func lintRefNodeKeys(spec *Spec) []error {
	var problems []error
	walkCommandsAt(spec, func(c *Command, path, ptr string) {
		if c.Ref == "" {
			return
		}
		// An unnamed $ref node's path segment is the ref path itself, so locate the
		// problem at its parent and name the ref in the message.
		loc, subject := "command "+path, ""
		if c.Name == "" {
			loc, subject = "command "+strings.TrimSuffix(path, "/"+c.Ref), fmt.Sprintf("$ref %q: ", c.Ref)
		}
		reject := func(key string) {
			problems = append(problems, &problem{
				kind: "spec", ptr: ptr + "/" + key, loc: loc,
				msg: fmt.Sprintf("%ssets %#q on a `$ref` node; a composed command delegates to the child's handler (built against the child's own inputs and output), so %#q cannot be overlaid here; declare it in the child spec instead", subject, key, key),
			})
		}
		if len(c.Flags) > 0 {
			reject("flags")
		}
		if len(c.Arguments) > 0 {
			reject("arguments")
		}
		if len(c.Env) > 0 {
			reject("env")
		}
		if len(c.Config) > 0 {
			reject("config")
		}
		if len(c.ConfigFiles) > 0 {
			reject("config_files")
		}
		if c.Stdin != nil {
			reject("stdin")
		}
		if len(c.FlagGroups) > 0 {
			reject("flag_groups")
		}
		if len(c.FlagDependencies) > 0 {
			reject("flag_dependencies")
		}
		if c.Output != nil {
			reject("output")
		}
		if len(c.Plugins) > 0 {
			reject("plugins")
		}
		if c.PluginDiscovery != nil {
			reject("plugin_discovery")
		}
		if c.Passthrough {
			reject("passthrough")
		}
	})
	return problems
}

// lintHandlerSource rejects `handler:` on the root command. The generator builds the root
// handler directly with no delegation seam, so the key would be silently ignored there.
func lintHandlerSource(spec *Spec) []error {
	var problems []error
	if spec.Command.Handler != nil {
		problems = append(problems, &problem{
			kind: "spec", ptr: rootPointer + "/handler", loc: rootLabel(spec),
			msg: "sets `handler` on the root command; handler delegation is supported on sub-commands only (a `$ref` node or an inline command), not the root",
		})
	}
	return problems
}

// lintRootAliases rejects aliases and deprecated_identifiers on the root command: the root
// is reached by invoking the binary, not by a routing token, so they would dispatch nothing.
func lintRootAliases(spec *Spec) []error {
	var problems []error
	if len(spec.Command.Aliases) > 0 {
		problems = append(problems, &problem{
			kind: "spec", ptr: rootPointer + "/aliases", loc: rootLabel(spec),
			msg: fmt.Sprintf("the root command cannot declare `aliases` (%s); it is reached by invoking the binary, not by a routing token; declare aliases on sub-commands",
				quotedList(spec.Command.Aliases)),
		})
	}
	if len(spec.Command.DeprecatedIdentifiers) > 0 {
		problems = append(problems, &problem{
			kind: "spec", ptr: rootPointer + "/deprecated_identifiers", loc: rootLabel(spec),
			msg: fmt.Sprintf("the root command cannot declare `deprecated_identifiers` (%s); with no routing token, a deprecated root alias can never be detected; declare them on sub-commands",
				quotedList(spec.Command.DeprecatedIdentifiers)),
		})
	}
	return problems
}

// lintSiblingCollisions rejects duplicate dispatch tokens among one command's children:
// sub-command and plugin names and aliases share one namespace, and dispatch tries
// sub-commands first, so a colliding plugin would be silently shadowed.
func lintSiblingCollisions(spec *Spec) []error {
	var problems []error
	walkCommandsAt(spec, func(c *Command, path, ptr string) {
		claimedBy := map[string]string{} // token -> the sibling that first claimed it
		claim := func(owner, at string, tokens ...string) {
			for _, tok := range tokens {
				if tok == "" {
					continue
				}
				if prev, dup := claimedBy[tok]; dup {
					msg := fmt.Sprintf("dispatch token %q is claimed by both %q and %q", tok, prev, owner)
					if prev == owner {
						msg = fmt.Sprintf("two sibling commands are both named %q", tok)
					}
					problems = append(problems, &problem{kind: "spec", ptr: at, loc: "command " + path, msg: msg})
					continue
				}
				claimedBy[tok] = owner
			}
		}
		for i := range c.Commands {
			child := &c.Commands[i]
			owner := child.Name
			if owner == "" {
				owner = child.Ref
			}
			claim(owner, fmt.Sprintf("%s/commands/%d", ptr, i), append([]string{child.Name}, child.Aliases...)...)
		}
		for i, r := range c.Plugins {
			claim("plugin "+r.Name, fmt.Sprintf("%s/plugins/%d", ptr, i), append([]string{r.Name}, r.Aliases...)...)
		}
	})
	return problems
}

// lintDuplicateInputNames rejects two inputs of the same channel sharing a name on one
// command: codegen derives one Go field per name, so a duplicate would not compile.
func lintDuplicateInputNames(spec *Spec) []error {
	var problems []error
	walkCommandsAt(spec, func(c *Command, path, ptr string) {
		seen := map[string]string{} // channel+name -> first declaration
		eachInputAt(c, ptr, func(channel, name, ptr string, _ *InputSchema) {
			if name == "" {
				return // stdin has no logical name
			}
			key := channel + "\x00" + name
			if _, dup := seen[key]; dup {
				problems = append(problems, inputProblem(ptr, path, channel, name,
					fmt.Sprintf("declared twice; each %s needs a unique name", channel)))
				return
			}
			seen[key] = name
		})
	})
	return problems
}

// lintVariadicArguments rejects a variadic (slice-typed) argument anywhere but last: it
// absorbs the remaining positionals, so later arguments could never bind.
func lintVariadicArguments(spec *Spec) []error {
	var problems []error
	walkCommandsAt(spec, func(c *Command, path, ptr string) {
		if c.inputs() == nil {
			return
		}
		for i, a := range c.inputs().Arguments {
			if i == len(c.inputs().Arguments)-1 {
				break
			}
			if strings.HasPrefix(getSchemaType(a.Schema), "[]") {
				problems = append(problems, inputProblem(fmt.Sprintf("%s/arguments/%d", ptr, i), path, "argument", a.Name,
					"variadic but not last; it would absorb every remaining positional, so later arguments could never bind"))
			}
		}
	})
	return problems
}

// lintRequiredArgumentOrder rejects a required positional argument declared after an
// optional one. Positionals fill in order, so the first value always lands in the optional
// argument, which therefore can never be omitted. A variadic with minItems > 0 counts as
// required.
func lintRequiredArgumentOrder(spec *Spec) []error {
	var problems []error
	walkCommandsAt(spec, func(c *Command, path, ptr string) {
		in := c.inputs()
		if in == nil {
			return
		}
		firstOptional := ""
		for i, a := range in.Arguments {
			if !argumentRequired(a) {
				if firstOptional == "" {
					firstOptional = a.Name
				}
				continue
			}
			if firstOptional != "" {
				problems = append(problems, inputProblem(fmt.Sprintf("%s/arguments/%d", ptr, i), path, "argument", a.Name,
					fmt.Sprintf("required but comes after optional argument %q; positionals fill in order, so the first value always goes to %q and it can never be left out. Make %q required too, or move %q before it",
						firstOptional, firstOptional, firstOptional, a.Name)))
			}
		}
	})
	return problems
}

// lintIgnoreCase requires ignore_case to accompany an enum (it only affects enum matching)
// and rejects enum members that differ only in case, which it would make indistinguishable.
func lintIgnoreCase(spec *Spec) []error {
	var problems []error
	walkCommandsAt(spec, func(c *Command, path, ptr string) {
		eachInputAt(c, ptr, func(channel, name, ptr string, schema *InputSchema) {
			if schema == nil || objectRef(schema, spec.Command.Schemas) != "" || !schema.IgnoreCase {
				return // an object input: lintObjectFlags
			}
			add := func(msg string) { problems = append(problems, inputProblem(ptr, path, channel, name, msg)) }
			if len(schema.Enum) == 0 {
				add("sets `ignore_case` but declares no `enum`; `ignore_case` changes how a value is matched against enum members, so without one it does nothing")
				return
			}
			seen := map[string]string{}
			for _, m := range schema.Enum {
				if prev, ok := seen[strings.ToLower(m)]; ok && prev != m {
					add(fmt.Sprintf("sets `ignore_case` but enum members %q and %q differ only in case; a value matching both could bind either", prev, m))
					return
				}
				seen[strings.ToLower(m)] = m
			}
		})
	})
	return problems
}

// lintSeparator restricts separator to list or map flags and arguments (env and config
// inputs never consult it) and rejects characters that collide with the syntax being split:
// a double quote (CSV quoting), a line break (CSV record end), and '=' on a map (key/value
// separator).
func lintSeparator(spec *Spec) []error {
	var problems []error
	walkCommandsAt(spec, func(c *Command, path, ptr string) {
		eachInputAt(c, ptr, func(channel, name, ptr string, schema *InputSchema) {
			if schema == nil || objectRef(schema, spec.Command.Schemas) != "" || schema.Separator == "" {
				return // an object input: lintObjectFlags
			}
			add := func(msg string) { problems = append(problems, inputProblem(ptr, path, channel, name, msg)) }
			t := getSchemaType(schema)
			isList, isMap := strings.HasPrefix(t, "[]"), strings.HasPrefix(t, "map[")
			switch {
			case channel != "flag" && channel != "argument":
				add("sets `separator`, which applies to flags and arguments only; an env input's list splits on commas and a configuration file writes a list as a list, so there is nothing for it to choose")
			case !isList && !isMap:
				add(fmt.Sprintf("sets `separator` but its type is %s; only a list or map takes several values to split into", displayType(t)))
			case schema.Separator == `"`:
				add("sets `separator` to a double quote, which is how an item that contains the separator is quoted")
			case strings.ContainsAny(schema.Separator, "\r\n"):
				add("sets `separator` to a line break, which ends a CSV record rather than separating items in one")
			case isMap && schema.Separator == "=":
				add("sets `separator` to \"=\", which already separates each map entry's key from its value")
			}
		})
	})
	return problems
}

// enumMember reports whether v is one of schema's enum members, the way the runtime matches:
// exactly, or regardless of case under ignore_case.
func enumMember(schema *InputSchema, v string) bool {
	if schema.IgnoreCase {
		return slices.ContainsFunc(schema.Enum, func(m string) bool { return strings.EqualFold(m, v) })
	}
	return slices.Contains(schema.Enum, v)
}

// lintImplicitValue restricts implicit_value to single-valued flags (not bool, count, list
// or map) and checks the value against the flag's enum and constraints as a default is, since
// a value that always fails would make the bare flag unusable.
func lintImplicitValue(spec *Spec) []error {
	var problems []error
	walkCommandsAt(spec, func(c *Command, path, ptr string) {
		eachInputAt(c, ptr, func(channel, name, ptr string, schema *InputSchema) {
			if schema == nil || objectRef(schema, spec.Command.Schemas) != "" || schema.ImplicitValue == nil {
				return // an object input: lintObjectFlags
			}
			add := func(msg string) { problems = append(problems, inputProblem(ptr, path, channel, name, msg)) }
			t := getSchemaType(schema)
			switch {
			case channel != "flag":
				add("sets `implicit_value`, which applies to flags only; it is what a flag given without a value takes")
			case t == "bool" || schema.Type == "count":
				add(fmt.Sprintf("sets `implicit_value` but its type is %s, which already takes no value", displayType(schema.Type)))
			case strings.HasPrefix(t, "[]") || strings.HasPrefix(t, "map["):
				add(fmt.Sprintf("sets `implicit_value` but its type is %s; an optional value applies to a flag that takes exactly one", displayType(t)))
			default:
				if v := defaultViolation(schema, "`implicit_value`", defaultString(schema.ImplicitValue)); v != "" {
					add(v + "; the flag given bare would always fail")
				}
			}
		})
	})
	return problems
}

// argumentRequired reports whether a positional must be supplied: declared required, or a
// variadic that needs at least one value.
func argumentRequired(a ArgumentInput) bool {
	if a.Schema == nil {
		return false
	}
	return a.Schema.Required || (strings.HasPrefix(getSchemaType(a.Schema), "[]") && a.Schema.MinItems > 0)
}

// lintDottedKeys restricts dotted_keys to flags of type map[string]any, the only map type
// that can hold a nested subtree.
func lintDottedKeys(spec *Spec) []error {
	var problems []error
	walkCommandsAt(spec, func(c *Command, path, ptr string) {
		eachInputAt(c, ptr, func(channel, name, ptr string, schema *InputSchema) {
			if schema == nil || !schema.DottedKeys {
				return
			}
			if channel != "flag" {
				problems = append(problems, inputProblem(ptr, path, channel, name, "sets `dotted_keys`, which applies to flags only"))
				return
			}
			if t := getSchemaType(schema); t != "map[string]any" {
				problems = append(problems, inputProblem(ptr, path, channel, name,
					fmt.Sprintf("sets `dotted_keys` but its type is %s; dotted keys need type \"map\" (map[string]any) to nest into", t)))
			}
		})
	})
	return problems
}

// lintConfigurationFiles requires each config_files entry to set exactly one of path or
// discover, with xdg requiring app and walk-up rejecting it. Name uniqueness is checked by
// lintConfigFilesScope.
func lintConfigurationFiles(spec *Spec) []error {
	var problems []error
	walkCommandsAt(spec, func(c *Command, path, ptr string) {
		for i, cf := range c.ConfigFiles {
			add := func(msg string) { problems = append(problems, configFileProblem(ptr, path, i, cf.Name, msg)) }
			switch {
			case cf.Path == "" && cf.Discover == nil:
				add("needs a location; set `path` or `discover`")
			case cf.Path != "" && cf.Discover != nil:
				add("sets both `path` and `discover`; exactly one locates the file")
			}
			if d := cf.Discover; d != nil {
				if d.Strategy == "xdg" && d.App == "" {
					add("`discover` strategy \"xdg\" needs `app` (the directory under the XDG config root)")
				}
				if d.Strategy == "walk-up" && d.App != "" {
					add("`discover` strategy \"walk-up\" does not use `app`; remove it (it would be silently ignored)")
				}
			}
		}
	})
	return problems
}

// configFileProblem is [inputProblem] for the i'th config_files entry of the command at
// cmdPtr.
func configFileProblem(cmdPtr, path string, i int, name, msg string) *problem {
	return &problem{
		kind: "spec", ptr: fmt.Sprintf("%s/config_files/%d", cmdPtr, i), loc: "command " + path,
		msg: fmt.Sprintf("config_files %q: %s", name, msg),
	}
}

// lintEnvNesting restricts nesting, which aggregates a family of variables into one nested
// map, to env inputs of type map[string]any with no default.
func lintEnvNesting(spec *Spec) []error {
	var problems []error
	walkCommandsAt(spec, func(c *Command, path, ptr string) {
		eachInputAt(c, ptr, func(channel, name, ptr string, schema *InputSchema) {
			if schema == nil || schema.Nesting == "" {
				return
			}
			add := func(msg string) { problems = append(problems, inputProblem(ptr, path, channel, name, msg)) }
			if channel != "env" {
				add("sets `nesting`, which applies to env inputs only")
				return
			}
			if t := getSchemaType(schema); t != "map[string]any" {
				add(fmt.Sprintf("sets `nesting` but its type is %s; a variable family needs type \"map\" (map[string]any) to nest into", t))
			}
			if schema.Default != nil {
				add("sets both `nesting` and `default`; a nested family has no single default; seed defaults in code or config instead")
			}
		})
	})
	return problems
}

// ancestorConfigIndex maps each config_files name and physical location in an ancestor
// chain to the first ancestor that declared it.
func ancestorConfigIndex(ancestors []*Command) (names, locs map[string]string) {
	names, locs = map[string]string{}, map[string]string{}
	for _, a := range ancestors {
		if a.inputs() == nil {
			continue
		}
		label := a.Name
		if label == "" {
			label = a.Ref
		}
		if label == "" {
			label = "(root)"
		}
		for _, cf := range a.inputs().ConfigFiles {
			if _, ok := names[cf.Name]; !ok {
				names[cf.Name] = label
			}
			if key := configFileLocationKey(cf); key != "" {
				if _, ok := locs[key]; !ok {
					locs[key] = fmt.Sprintf("%q on %s", cf.Name, label)
				}
			}
		}
	}
	return names, locs
}

// lintConfigFilesScope enforces config_files uniqueness along each command chain, matching
// the runtime cascade. A duplicate name is an error because `file` pins and config_source
// target entries by name; two entries resolving to the same file are a warning. Sibling
// chains are independent.
func lintConfigFilesScope(spec *Spec) []error {
	var problems []error
	walkChainsAt(spec, func(chain []*Command, path, ptr string) {
		cmd := chain[len(chain)-1]
		if cmd.inputs() == nil {
			return
		}
		ancestorName, ancestorLoc := ancestorConfigIndex(chain[:len(chain)-1])
		ownName := map[string]bool{}
		ownLoc := map[string]string{} // location key → first own entry name
		for i, cf := range cmd.ConfigFiles {
			add := func(sev severity, msg string) {
				p := configFileProblem(ptr, path, i, cf.Name, msg)
				p.sev = sev
				problems = append(problems, p)
			}
			switch {
			case ownName[cf.Name]:
				add(severityError, "declared twice; logical names identify entries (`file` pins, `config_source`) and must be unique")
			case ancestorName[cf.Name] != "":
				add(severityError, fmt.Sprintf("shadows the entry declared on ancestor %s; names cascade and must be unique along the chain (a `file` pin or `config_source` would be ambiguous); rename one", ancestorName[cf.Name]))
			}
			ownName[cf.Name] = true

			key := configFileLocationKey(cf)
			if key == "" {
				continue // no location: lintConfigurationFiles errors on that
			}
			switch {
			case ownLoc[key] != "":
				add(severityWarning, fmt.Sprintf("resolves to the same file as %q; the later shadows the earlier (nearest-wins); declare it once", ownLoc[key]))
			case ancestorLoc[key] != "":
				add(severityWarning, fmt.Sprintf("resolves to the same file as %s; the nearer shadows it (nearest-wins); declare it once", ancestorLoc[key]))
			}
			if ownLoc[key] == "" {
				ownLoc[key] = cf.Name
			}
		}
	})
	return problems
}

// configFileLocationKey identifies a config file's physical location: its path, or its
// discover target (strategy|file|app). It is "" when neither is set.
func configFileLocationKey(cf ConfigurationFile) string {
	if cf.Path != "" {
		return "path:" + cf.Path
	}
	if d := cf.Discover; d != nil {
		return "discover:" + d.Strategy + "|" + d.File + "|" + d.App
	}
	return ""
}

// lintConfigSource restricts config_source to string-typed flag and env inputs naming a
// config_files entry in scope, and allows at most one flag and one env claim per entry along
// a chain, since a second claim would shadow the first.
func lintConfigSource(spec *Spec) []error {
	var problems []error
	walkChainsAt(spec, func(chain []*Command, path, ptr string) {
		cmd := chain[len(chain)-1]
		declared := chainConfigNames(chain)
		claims := map[string]map[string]string{} // target → channel → claiming input
		// Seed with ancestor claims so this command's claims collide with them. An
		// ancestor's own conflicts are reported when that ancestor is visited.
		for _, a := range chain[:len(chain)-1] {
			eachInputSchema(a.inputs(), func(channel, name string, schema *InputSchema) {
				if schema == nil || schema.ConfigSource == "" || (channel != "flag" && channel != "env") {
					return
				}
				if claims[schema.ConfigSource] == nil {
					claims[schema.ConfigSource] = map[string]string{}
				}
				if _, ok := claims[schema.ConfigSource][channel]; !ok {
					claims[schema.ConfigSource][channel] = name
				}
			})
		}
		eachInputAt(cmd, ptr, func(channel, name, ptr string, schema *InputSchema) {
			if schema == nil || schema.ConfigSource == "" {
				return
			}
			target := schema.ConfigSource
			add := func(msg string) { problems = append(problems, inputProblem(ptr, path, channel, name, msg)) }
			if channel != "flag" && channel != "env" {
				add("sets `config_source`, which applies to flag and env inputs only")
				return
			}
			if !declared[target] {
				add(fmt.Sprintf("names `config_source` %q, which is not a `config_files` entry in scope (declared on this command or an ancestor)", target))
				return
			}
			if t := getSchemaType(schema); t != "string" {
				add(fmt.Sprintf("sets `config_source` but its type is %s; a file path is a string", t))
			}
			if claims[target] == nil {
				claims[target] = map[string]string{}
			}
			if prev, dup := claims[target][channel]; dup {
				add(fmt.Sprintf("claims `config_source` %q, already claimed by %s %q; one %s per entry", target, channel, prev, channel))
				return
			}
			claims[target][channel] = name
		})
	})
	return problems
}

// constraintNumericFamily mirrors the runtime's numericFamily in parser.go: the
// range-checkable int, uint and float types plus the JSON Schema aliases.
var constraintNumericFamily = map[string]bool{
	"int": true, "int8": true, "int16": true, "int32": true, "int64": true,
	"uint": true, "uint8": true, "uint16": true, "uint32": true, "uint64": true,
	"float32": true, "float64": true,
	"integer": true, "number": true,
}

// inputProblem builds a spec problem about one input, labeled `command <path>: <channel>
// "<name>": …` and positioned at ptr, the input's pointer from [eachInputAt].
func inputProblem(ptr, path, channel, name, msg string) *problem {
	subject := channel
	if name != "" {
		subject = fmt.Sprintf("%s %q", channel, name)
	}
	return &problem{kind: "spec", ptr: ptr, loc: "command " + path, msg: subject + ": " + msg}
}

// eachInputAt is [eachInputSchema] plus each input's positional JSON pointer, which stays
// exact even when two inputs share a name.
func eachInputAt(c *Command, cmdPtr string, visit func(channel, name, ptr string, schema *InputSchema)) {
	for i := range c.Flags {
		visit("flag", c.Flags[i].Name, fmt.Sprintf("%s/flags/%d", cmdPtr, i), c.Flags[i].Schema)
	}
	for i := range c.Arguments {
		visit("argument", c.Arguments[i].Name, fmt.Sprintf("%s/arguments/%d", cmdPtr, i), c.Arguments[i].Schema)
	}
	for i := range c.Env {
		visit("env", c.Env[i].Name, fmt.Sprintf("%s/env/%d", cmdPtr, i), c.Env[i].Schema)
	}
	for i := range c.Config {
		visit("config", c.Config[i].Name, fmt.Sprintf("%s/config/%d", cmdPtr, i), c.Config[i].Schema)
	}
	if c.Stdin != nil {
		visit("stdin", "", cmdPtr+"/stdin", c.Stdin.Schema)
	}
}

// inertKeyProblems reports channel-specific keys set on a channel where they would silently
// do nothing. The reference's per-channel table is derived from this function.
func inertKeyProblems(ptr, path, channel, name string, schema *InputSchema) []error {
	var problems []error
	inert := func(key, where string) {
		problems = append(problems, inputProblem(ptr, path, channel, name,
			fmt.Sprintf("sets %#q, which applies only to %s; here it would do nothing", key, where)))
	}
	if schema.Negatable && channel != "flag" {
		inert("negatable", "bool flags (it derives a --no-<name> form)")
	}
	if schema.Key != "" && channel != "flag" && channel != "config" {
		inert("key", "config inputs and a flag's configuration fallback")
	}
	if len(schema.Properties) > 0 && channel != "flag" {
		inert("properties", "a map flag, whose property names feed shell completion (an object's shape belongs in a named schema)")
	}
	return problems
}

// lintConstraintApplicability rejects a constraint on a type it can never check: numeric
// bounds on non-numerics, length or pattern on non-strings, item counts on non-collections.
// Per-value constraints on an array apply to its element type. Stdin is exempt because its
// schema validates the piped document with full JSON Schema semantics.
func lintConstraintApplicability(spec *Spec) []error {
	var problems []error
	walkCommandsAt(spec, func(c *Command, path, ptr string) {
		eachInputAt(c, ptr, func(channel, name, ptr string, schema *InputSchema) {
			if channel == "stdin" || schema == nil {
				return
			}
			problems = append(problems, inertKeyProblems(ptr, path, channel, name, schema)...)
			typ := getSchemaType(schema)
			if t := namedScalarType(typ, spec.Command.Schemas); t != "" {
				typ = t // checked as the type the named schema declares, as the runtime does
			}
			elem := strings.TrimPrefix(typ, "[]")
			add := func(msg string) { problems = append(problems, inputProblem(ptr, path, channel, name, msg)) }
			numericBounds := schema.Minimum != nil || schema.Maximum != nil ||
				schema.ExclusiveMinimum != nil || schema.ExclusiveMaximum != nil || schema.MultipleOf != nil
			switch {
			case numericBounds && measuredTypes[elem]:
				for _, msg := range measuredBoundProblems(schema, elem) {
					add(msg)
				}
			case numericBounds && !constraintNumericFamily[elem]:
				add(fmt.Sprintf("`minimum`/`maximum`/`exclusiveMinimum`/`exclusiveMaximum`/`multipleOf` apply to numeric types, durations and sizes only, not %s; the bound would be silently ignored", typ))
			case numericBounds:
				for _, key := range boundKeys {
					if text, ok := boundsByKey(schema)[key].(string); ok {
						add(fmt.Sprintf("%#q %q is text, but %s takes a number; a string bound is for a duration or bytesize input", key, text, typ))
					}
				}
			}
			if (schema.MinLength != 0 || schema.MaxLength != 0 || schema.Pattern != "") && !stringValued(elem) {
				add(fmt.Sprintf("`minLength`/`maxLength`/`pattern` apply to string types only, not %s; the constraint would be silently ignored", typ))
			}
			if (schema.MinItems != 0 || schema.MaxItems != 0) &&
				!strings.HasPrefix(typ, "[]") && !strings.HasPrefix(typ, "map[") {
				add(fmt.Sprintf("`minItems`/`maxItems` apply to repeatable (list or map) types only, not %s; the count bound would be silently ignored", typ))
			}
		})
	})
	return problems
}

// boundKeys lists the numeric-bound keys in a fixed order so messages are deterministic.
var boundKeys = []string{"minimum", "maximum", "exclusiveMinimum", "exclusiveMaximum", "multipleOf"}

// boundsByKey maps each numeric-bound key to its declared value, absent ones included as nil.
func boundsByKey(s *InputSchema) map[string]any {
	return map[string]any{
		"minimum": s.Minimum, "maximum": s.Maximum, "exclusiveMinimum": s.ExclusiveMinimum,
		"exclusiveMaximum": s.ExclusiveMaximum, "multipleOf": s.MultipleOf,
	}
}

// measuredBoundProblems reports bounds on a duration or bytesize input that normalizeBounds
// left as text because they did not parse in the type's unit.
func measuredBoundProblems(s *InputSchema, elem string) []string {
	want := "a size such as 512Mi, 10MB or 1073741824"
	if elem == "time.Duration" {
		want = "a duration such as 30s or 1h30m"
	}
	var out []string
	for _, key := range boundKeys {
		if text, ok := boundsByKey(s)[key].(string); ok {
			out = append(out, fmt.Sprintf("%#q %q is not %s", key, text, want))
		}
	}
	return out
}

// stringValued reports whether a type's value is a string and so accepts string constraints:
// "string" and the path types, whose generated field is a plain string.
func stringValued(elem string) bool {
	return elem == "string" || elem == "existingfile" || elem == "existingdir"
}

// lintPassthrough requires a passthrough command, whose every token is a raw positional, to
// declare no flags, sub-commands or plugins and to end with a variadic []string argument
// that receives the tokens.
func lintPassthrough(spec *Spec) []error {
	var problems []error
	walkCommandsAt(spec, func(c *Command, path, ptr string) {
		if !c.Passthrough {
			return
		}
		add := func(key, msg string) {
			problems = append(problems, &problem{kind: "spec", ptr: ptr + "/" + key, loc: "command " + path, msg: msg})
		}
		if len(c.Flags) > 0 {
			add("flags", "sets `passthrough` and declares `flags`, but a passthrough command parses none; its tokens are raw positionals")
		}
		if len(c.Commands) > 0 {
			add("commands", "sets `passthrough` and declares `commands`, but a passthrough command never descends; a child token is a raw positional")
		}
		if len(c.Plugins) > 0 || c.PluginDiscovery != nil {
			add("passthrough", "sets `passthrough` and declares `plugins` or `plugin_discovery`, but a passthrough command never dispatches; the token is a raw positional")
		}
		args := []ArgumentInput{}
		if c.inputs() != nil {
			args = c.inputs().Arguments
		}
		if len(args) == 0 || getSchemaType(args[len(args)-1].Schema) != "[]string" {
			add("passthrough", "sets `passthrough` but its last argument is not a variadic []string; declare one to receive the raw tokens")
		}
	})
	return problems
}

// lintCountFlags restricts `type: count` to flags and rejects every value-shaped key on it:
// a count flag takes no value, and its int field is the occurrence tally.
func lintCountFlags(spec *Spec) []error {
	var problems []error
	walkCommandsAt(spec, func(c *Command, path, ptr string) {
		eachInputAt(c, ptr, func(channel, name, ptr string, schema *InputSchema) {
			if schema == nil || schema.Type != "count" {
				return
			}
			add := func(msg string) { problems = append(problems, inputProblem(ptr, path, channel, name, msg)) }
			if channel != "flag" {
				add("`type: count` counts argv flag occurrences; it applies to flags only")
				return
			}
			var bad []string
			for key, set := range map[string]bool{
				"default":         schema.Default != nil,
				"enum":            len(schema.Enum) > 0,
				"required":        schema.Required,
				"nullable":        schema.Nullable,
				"secret":          schema.Secret,
				"placeholder":     schema.Placeholder != "",
				"key":             schema.Key != "",
				"file":            schema.File != "",
				"variable":        len(variables(schema)) > 0,
				"from":            len(schema.From) > 0,
				"config_source":   schema.ConfigSource != "",
				"dotted_keys":     schema.DottedKeys,
				"nesting":         schema.Nesting != "",
				"items":           schema.Items != nil,
				"minimum/maximum": schema.Minimum != nil || schema.Maximum != nil,
				"exclusiveMinimum/exclusiveMaximum/multipleOf": schema.ExclusiveMinimum != nil || schema.ExclusiveMaximum != nil || schema.MultipleOf != nil,
				"minLength/maxLength/pattern":                  schema.MinLength != 0 || schema.MaxLength != 0 || schema.Pattern != "",
				"minItems/maxItems":                            schema.MinItems != 0 || schema.MaxItems != 0,
			} {
				if set {
					bad = append(bad, key)
				}
			}
			if len(bad) > 0 {
				sort.Strings(bad)
				add(fmt.Sprintf("a count flag has no value to resolve, so %s cannot apply; remove them (the generated int field is the occurrence tally)", keyList(bad)))
			}
		})
	})
	return problems
}

// lintVariable restricts variable, which names the environment variable an input reads, to
// env inputs and flags (as the flag's env fallback), and requires a nested env input to name
// a single prefix.
func lintVariable(spec *Spec) []error {
	var problems []error
	walkCommandsAt(spec, func(c *Command, path, ptr string) {
		eachInputAt(c, ptr, func(channel, name, ptr string, schema *InputSchema) {
			vars := variables(schema)
			if schema == nil || len(vars) == 0 {
				return
			}
			if channel == "env" || channel == "flag" {
				if len(vars) > 1 && schema.Nesting != "" {
					problems = append(problems, inputProblem(ptr, path, channel, name,
						fmt.Sprintf("sets `nesting` with %d variables; a nested input reads the family of variables under ONE prefix, so name one", len(vars))))
				}
				return
			}
			problems = append(problems, inputProblem(ptr, path, channel, name,
				"sets `variable`, which names an environment variable; only env inputs and flags (as a flag's env fallback) read one, so here it would be silently ignored"))
		})
	})
	return problems
}

// lintNegatable restricts negatable to bool flags with a long identifier to derive
// "--no-<name>" from, and rejects a derived form that collides with a declared identifier.
func lintNegatable(spec *Spec) []error {
	var problems []error
	walkCommandsAt(spec, func(c *Command, path, ptr string) {
		flagPtr := func(i int) string { return fmt.Sprintf("%s/flags/%d", ptr, i) }
		for i, f := range c.Flags {
			if f.Schema == nil || !f.Schema.Negatable {
				continue
			}
			add := func(msg string) { problems = append(problems, inputProblem(flagPtr(i), path, "flag", f.Name, msg)) }
			if t := f.Schema.Type; t != "bool" && t != "boolean" {
				add(fmt.Sprintf("sets `negatable` but its type is %s; the negated form sets a bool false, so it applies to bool flags only", displayType(t)))
				continue
			}
			if !slices.ContainsFunc(flagIdentifiers(f), func(id string) bool { return strings.HasPrefix(id, "--") }) {
				add("sets `negatable` but declares no long identifier; the negated form is derived as \"--no-<name>\", so there is nothing to derive it from")
			}
		}
		declared := map[string]string{}
		for _, f := range c.Flags {
			for _, id := range flagIdentifiers(f) {
				declared[id] = f.Name
			}
		}
		for i, f := range c.Flags {
			if f.Schema == nil || !f.Schema.Negatable {
				continue
			}
			for _, id := range flagIdentifiers(f) {
				if !strings.HasPrefix(id, "--") {
					continue
				}
				neg := "--no-" + strings.TrimPrefix(id, "--")
				if owner, clash := declared[neg]; clash {
					problems = append(problems, inputProblem(flagPtr(i), path, "flag", f.Name,
						fmt.Sprintf("sets `negatable`, deriving %q, which flag %q already declares; one of the two would never match", neg, owner)))
				}
			}
		}
	})
	return problems
}

// lintShortCircuit keeps a short_circuit flag to what the waiver can mean: a bool switch set
// only on the command line, never a requirement itself. Being required, defaulting to true,
// or reading an environment variable or config key would waive every run; negating it has no
// meaning; and a flag group or dependency is a requirement the flag would waive.
func lintShortCircuit(spec *Spec) []error {
	var problems []error
	walkCommandsAt(spec, func(c *Command, path, ptr string) {
		grouped := map[string]bool{}
		for _, g := range c.FlagGroups {
			for _, name := range g.Flags {
				grouped[name] = true
			}
		}
		dependent := map[string]bool{}
		for _, dep := range c.FlagDependencies {
			dependent[dep.When] = true
			for _, name := range dep.Requires {
				dependent[name] = true
			}
		}
		for i, f := range c.Flags {
			if !f.ShortCircuit {
				continue
			}
			for _, msg := range shortCircuitFlagProblems(f, grouped[f.Name], dependent[f.Name]) {
				problems = append(problems, inputProblem(fmt.Sprintf("%s/flags/%d", ptr, i), path, "flag", f.Name, msg))
			}
		}
	})
	return problems
}

// shortCircuitFlagProblems lists what is wrong with one short_circuit flag's declaration.
func shortCircuitFlagProblems(f FlagInput, grouped, dependent bool) []string {
	s := f.Schema
	if s == nil {
		s = &InputSchema{}
	}
	var msgs []string
	if s.Type != "bool" && s.Type != "boolean" {
		msgs = append(msgs, fmt.Sprintf("sets `short_circuit` but its type is %s; a short-circuit flag is a bool switch", displayType(s.Type)))
	}
	if s.Required {
		msgs = append(msgs, "sets `short_circuit` and `required`; a short-circuit flag replaces the run, so it can't also be required")
	}
	if defaultString(s.Default) == "true" {
		msgs = append(msgs, "sets `short_circuit` with a default of true, which would short-circuit every run")
	}
	if s.Key != "" || len(variables(s)) > 0 {
		msgs = append(msgs, "sets `short_circuit` and reads an environment variable or config key (`key` / `variable`); only the command line sets a short-circuit flag, or every run with that value set would be short-circuited")
	}
	if s.Negatable {
		msgs = append(msgs, "sets `short_circuit` and `negatable`; a negated short-circuit flag has no meaning")
	}
	if grouped {
		msgs = append(msgs, "sets `short_circuit` but is listed in `flag_groups`; a short-circuit flag waives flag groups, so the group could never apply to it")
	}
	if dependent {
		msgs = append(msgs, "sets `short_circuit` but is listed in `flag_dependencies`; a short-circuit flag waives flag dependencies, so the rule could never apply to it")
	}
	return msgs
}

// displayType renders a declared type for a message, describing an omitted type explicitly.
func displayType(t string) string {
	if t == "" {
		return "unset (string by default)"
	}
	return t
}

// lintStdinFormat requires the schema type of a raw stdin format to match what the payload
// becomes: string for 'text', []string for 'lines'. A mismatch would otherwise surface at run
// time as a wiring error. Document formats decode into the generated struct and are
// unconstrained.
func lintStdinFormat(spec *Spec) []error {
	var problems []error
	walkCommandsAt(spec, func(c *Command, path, ptr string) {
		if c.Stdin == nil || c.Stdin.Schema == nil || !rawStdinFormat(c.Stdin.Format) {
			return
		}
		typ := getSchemaType(c.Stdin.Schema)
		add := func(want string) {
			problems = append(problems, inputProblem(ptr+"/stdin", path, "stdin", "",
				fmt.Sprintf("`format` %q binds the payload as %s, but the schema declares %s; declare %s, or use a document format (json/yaml/jsonc/toml) to decode into a typed payload instead",
					c.Stdin.Format, want, displayType(typ), want)))
		}
		switch c.Stdin.Format {
		case "text":
			if typ != "string" {
				add("string")
			}
		case "lines":
			if typ != "[]string" && typ != "array" {
				add("[]string")
			}
		}
	})
	return problems
}

// lintComplete restricts the completion hint to flags and arguments that take a value, and
// `complete.extensions` to kind "file".
func lintComplete(spec *Spec) []error {
	var problems []error
	walkCommandsAt(spec, func(c *Command, path, ptr string) {
		eachInputAt(c, ptr, func(channel, name, ptr string, schema *InputSchema) {
			if schema == nil || schema.Complete == nil || schema.Complete.Kind == "" {
				return
			}
			add := func(msg string) { problems = append(problems, inputProblem(ptr, path, channel, name, msg)) }
			if channel != "flag" && channel != "argument" {
				add("sets `complete`, which describes a value typed on the command line; only flags and arguments are, so here it would be silently ignored")
				return
			}
			if t := getSchemaType(schema); channel == "flag" && (t == "bool" || t == "boolean" || t == "count") {
				add(fmt.Sprintf("sets `complete` but its type is %s, which takes no value; there is nothing to complete", t))
				return
			}
			if len(schema.Complete.Extensions) > 0 && schema.Complete.Kind != "file" {
				add(fmt.Sprintf("sets `complete.extensions` with kind %q; extensions narrow files, so they apply to kind \"file\" only", schema.Complete.Kind))
			}
		})
	})
	return problems
}

// lintDefaultScalar requires a `default:` to be a scalar, or, on a repeatable (list or map)
// input, a list or map of scalars, each seeded as one occurrence.
func lintDefaultScalar(spec *Spec) []error {
	var problems []error
	walkCommandsAt(spec, func(c *Command, path, ptr string) {
		eachInputAt(c, ptr, func(channel, name, ptr string, schema *InputSchema) {
			if schema == nil || objectRef(schema, spec.Command.Schemas) != "" || schema.Default == nil {
				return // an object input: lintObjectFlags
			}
			switch v := schema.Default.(type) {
			case string, bool, float64, int, int64, nil:
				return
			case []any, map[string]any:
				if repeatableSchema(schema) {
					if bad := nonScalarElement(v); bad != "" {
						problems = append(problems, inputProblem(ptr, path, channel, name,
							fmt.Sprintf("every element of a multi-value `default` must be a scalar, and one is %s; each element is seeded as one occurrence and coerced through the input's element type, so there is nowhere for a nested list or map to go", bad)))
					}
					return
				}
				problems = append(problems, inputProblem(ptr, path, channel, name,
					fmt.Sprintf("a multi-value `default` needs a repeatable type, and this one is %s; each element is seeded as a separate occurrence, which a single-valued input has nowhere to put. Declare the type as a list (e.g. []string) or a map, or give a single scalar default", displayType(schema.Type))))
				return
			}
			problems = append(problems, inputProblem(ptr, path, channel, name,
				fmt.Sprintf("`default` must be a scalar, or a list for a repeatable input; not %s. A default is seeded as argv occurrences and coerced through the input's type, and there is no spelling that turns %s into one",
					defaultKindName(schema.Default), defaultKindName(schema.Default))))
		})
	})
	return problems
}

// defaultKindName names a rejected default's shape for a message.
func defaultKindName(v any) string {
	switch v.(type) {
	case []any:
		return "a list"
	case map[string]any:
		return "a map"
	}
	return fmt.Sprintf("%T", v)
}

// lintPatternCompiles rejects an input `pattern` that is not a valid Go regular expression,
// since the runtime tolerates a failed compile and the pattern would never enforce. Stdin
// patterns are exempt because they fail loudly at bind time. It also rejects a
// `pattern_message` anywhere in a schema tree without a `pattern` beside it.
func lintPatternCompiles(spec *Spec) []error {
	var problems []error
	orphans := func(path, ptr, where string, b BaseSchema) {
		walkSchemaTree(b, func(s BaseSchema) {
			if s.PatternMessage != "" && s.Pattern == "" {
				problems = append(problems, &problem{
					kind: "spec", ptr: ptr, loc: "command " + path,
					msg: fmt.Sprintf("%s: `pattern_message` %q has no `pattern` beside it; it replaces a pattern's failure message, so it would never be shown", where, s.PatternMessage),
				})
			}
		})
	}
	walkCommandsAt(spec, func(c *Command, path, ptr string) {
		for _, name := range slices.Sorted(maps.Keys(c.Schemas)) {
			orphans(path, ptr+"/schemas/"+name, fmt.Sprintf("schema %q", name), c.Schemas[name].BaseSchema)
		}
		if c.Output != nil {
			orphans(path, ptr+"/output", "output", c.Output.BaseSchema)
		}
		eachInputAt(c, ptr, func(channel, name, ptr string, schema *InputSchema) {
			if schema != nil {
				where := fmt.Sprintf("%s %q", channel, name)
				if channel == "stdin" {
					where = "stdin"
				}
				orphans(path, ptr, where, schema.BaseSchema)
			}
			if channel == "stdin" || schema == nil || schema.Pattern == "" {
				return
			}
			if _, err := regexp.Compile(schema.Pattern); err != nil {
				problems = append(problems, inputProblem(ptr, path, channel, name,
					fmt.Sprintf("`pattern` %q does not compile (%v); it would silently never enforce", schema.Pattern, err)))
			}
		})
	})
	return problems
}

// lintConfigInputFiles restricts `file`, which pins a config input to one config_files
// entry, to config inputs naming an entry declared on the command or an ancestor.
func lintConfigInputFiles(spec *Spec) []error {
	var problems []error
	walkChainsAt(spec, func(chain []*Command, path, ptr string) {
		cmd := chain[len(chain)-1]
		declared := chainConfigNames(chain)
		eachInputAt(cmd, ptr, func(channel, name, ptr string, schema *InputSchema) {
			if schema == nil || schema.File == "" {
				return
			}
			if channel != "config" {
				problems = append(problems, inputProblem(ptr, path, channel, name, "sets `file`, which applies to config inputs only"))
				return
			}
			if !declared[schema.File] {
				problems = append(problems, inputProblem(ptr, path, channel, name,
					fmt.Sprintf("pins `file` %q, which is not a `config_files` entry in scope (declared on this command or an ancestor)", schema.File)))
			}
		})
	})
	return problems
}

// lintFrom restricts `from` to non-bool flags and allows one stdin consumer per command: a
// from:stdin flag cannot coexist with a `stdin:` channel or another from:stdin flag.
func lintFrom(spec *Spec) []error {
	var problems []error
	walkCommandsAt(spec, func(c *Command, path, ptr string) {
		stdinClaim := "" // what already claimed this command's stdin
		if c.Stdin != nil {
			stdinClaim = "the `stdin` channel"
		}
		eachInputAt(c, ptr, func(channel, name, ptr string, schema *InputSchema) {
			if schema == nil || len(schema.From) == 0 {
				return
			}
			add := func(msg string) { problems = append(problems, inputProblem(ptr, path, channel, name, msg)) }
			if channel != "flag" {
				add("sets `from`, which applies to flags only")
				return
			}
			if t := getSchemaType(schema); t == "bool" {
				add("sets `from` but its type is bool; a bool takes no value to resolve")
			}
			if slices.Contains(schema.From, "stdin") {
				if stdinClaim != "" {
					add(fmt.Sprintf("sets `from: stdin` but %s already consumes stdin; stdin has one consumer", stdinClaim))
					return
				}
				stdinClaim = fmt.Sprintf("flag %q", name)
			}
		})
	})
	return problems
}

// lintDeprecatedIdentifiers requires a command's deprecated_identifiers to be among its
// aliases and a flag's to be among its declared or derived identifiers; any other token
// could never be reported as deprecated.
func lintDeprecatedIdentifiers(spec *Spec) []error {
	var problems []error
	subset := func(report func(msg string), declared, deprecated []string, vocab string) {
		known := map[string]bool{}
		for _, d := range declared {
			known[d] = true
		}
		for _, d := range deprecated {
			if !known[d] {
				report(didYouMean(fmt.Sprintf("`deprecated_identifiers` entry %q is not one of its %s; it could never be reported as deprecated", d, vocab), d, declared))
			}
		}
	}
	walkCommandsAt(spec, func(c *Command, path, ptr string) {
		if c != &spec.Command && len(c.DeprecatedIdentifiers) > 0 {
			subset(func(msg string) {
				problems = append(problems, &problem{kind: "spec", ptr: ptr + "/deprecated_identifiers", loc: "command " + path, msg: msg})
			}, c.Aliases, c.DeprecatedIdentifiers, "aliases")
		}
		for i, f := range c.Flags {
			if len(f.DeprecatedIdentifiers) > 0 {
				subset(func(msg string) {
					problems = append(problems, inputProblem(fmt.Sprintf("%s/flags/%d", ptr, i), path, "flag", f.Name, msg))
				}, flagIdentifiers(f), f.DeprecatedIdentifiers, "identifiers")
			}
		}
	})
	return problems
}

// lintPluginTimeouts rejects a plugin timeout that is not a positive Go duration, which
// codegen would otherwise drop, leaving the plugin unbounded.
func lintPluginTimeouts(spec *Spec) []error {
	var problems []error
	walkCommandsAt(spec, func(c *Command, path, ptr string) {
		for i, r := range c.Plugins {
			if r.Timeout == "" {
				continue
			}
			if d, err := time.ParseDuration(r.Timeout); err != nil || d <= 0 {
				problems = append(problems, &problem{
					kind: "spec", ptr: fmt.Sprintf("%s/plugins/%d", ptr, i), loc: "command " + path,
					msg: fmt.Sprintf("plugins %q: `timeout` %q is not a positive Go duration (e.g. \"10s\", \"1m30s\")", r.Name, r.Timeout),
				})
			}
		}
	})
	return problems
}

// lintImportConsistency reports each `type` declared with conflicting `import` values across
// the spec, once per type, at its first conflicting import.
func lintImportConsistency(spec *Spec) []error {
	type site struct{ loc, ptr string }
	byType := map[string]map[string]bool{}
	conflictAt := map[string]site{} // type → where its first differing import appears
	var order []string              // types in the order their conflict was found
	record := func(at site) func(BaseSchema) {
		return func(b BaseSchema) {
			typ, imp := strings.TrimSpace(b.Type), strings.TrimSpace(b.Import)
			if typ == "" || imp == "" {
				return
			}
			if byType[typ] == nil {
				byType[typ] = map[string]bool{}
			}
			if _, seen := conflictAt[typ]; !seen && len(byType[typ]) > 0 && !byType[typ][imp] {
				conflictAt[typ] = at
				order = append(order, typ)
			}
			byType[typ][imp] = true
		}
	}
	walkCommandsAt(spec, func(c *Command, path, ptr string) {
		eachInputAt(c, ptr, func(_, _, ptr string, s *InputSchema) {
			if s != nil {
				walkSchemaTree(s.BaseSchema, record(site{"command " + path, ptr}))
			}
		})
		if c.Output != nil {
			walkSchemaTree(c.Output.BaseSchema, record(site{"command " + path, ptr + "/output"}))
		}
	})
	for _, name := range slices.Sorted(maps.Keys(spec.Command.Schemas)) {
		walkSchemaTree(spec.Command.Schemas[name].BaseSchema, record(site{rootLabel(spec), rootPointer + "/schemas/" + name}))
	}

	problems := make([]error, 0, len(order))
	for _, typ := range order {
		at := conflictAt[typ]
		problems = append(problems, &problem{
			kind: "spec", ptr: at.ptr, loc: at.loc,
			msg: fmt.Sprintf("type %q is declared with conflicting imports (%s); a type must have one backing package",
				typ, quotedList(slices.Sorted(maps.Keys(byType[typ])))),
		})
	}
	return problems
}

// lintLocalTimeout rejects `timeout` on a command: it is honored only on a plugins entry.
func lintLocalTimeout(spec *Spec) []error {
	var problems []error
	walkCommandsAt(spec, func(c *Command, path, ptr string) {
		if strings.TrimSpace(c.Timeout) != "" {
			problems = append(problems, &problem{
				kind: "spec", ptr: ptr + "/timeout",
				loc: "command " + path,
				msg: "sets `timeout`, which is not supported on a local command; it is a plugin-only, host-side bound with no effect here; set it on a `plugins` entry's `timeout` instead",
			})
		}
	})
	return problems
}

// lintFlagGroups rejects a flag_groups entry referencing a flag the command does not
// declare, which would silently never match.
func lintFlagGroups(spec *Spec) []error {
	var problems []error
	walkCommandsAt(spec, func(c *Command, path, ptr string) {
		if c.inputs() == nil || len(c.inputs().FlagGroups) == 0 {
			return
		}
		known, ordered := flagNames(c)
		for i, g := range c.FlagGroups {
			for _, name := range g.Flags {
				if !known[name] {
					msg := fmt.Sprintf("`flag_groups` entry (%s) references unknown flag %q; it has no matching entry in this command's `flags`", g.Kind, name)
					problems = append(problems, &problem{kind: "spec", ptr: fmt.Sprintf("%s/flag_groups/%d", ptr, i), loc: "command " + path, msg: didYouMean(msg, name, ordered)})
				}
			}
		}
	})
	return problems
}

// lintFlagDependencies rejects a flag_dependencies entry whose When or Requires references a
// flag the command does not declare.
func lintFlagDependencies(spec *Spec) []error {
	var problems []error
	walkCommandsAt(spec, func(c *Command, path, ptr string) {
		if c.inputs() == nil || len(c.inputs().FlagDependencies) == 0 {
			return
		}
		known, ordered := flagNames(c)
		for i, dep := range c.FlagDependencies {
			report := func(name string) {
				msg := fmt.Sprintf("`flag_dependencies` entry references unknown flag %q; it has no matching entry in this command's `flags`", name)
				problems = append(problems, &problem{kind: "spec", ptr: fmt.Sprintf("%s/flag_dependencies/%d", ptr, i), loc: "command " + path, msg: didYouMean(msg, name, ordered)})
			}
			if !known[dep.When] {
				report(dep.When)
			}
			for _, name := range dep.Requires {
				if !known[name] {
					report(name)
				}
			}
		}
	})
	return problems
}

// lintDuplicateFlagIdentifiers rejects a flag identifier claimed twice on one command, a
// collision the parser would resolve silently. It checks effective identifiers (declared, or
// derived "--<name>"), so "dry_run" and "dry-run" collide.
func lintDuplicateFlagIdentifiers(spec *Spec) []error {
	var problems []error
	walkCommandsAt(spec, func(c *Command, path, ptr string) {
		if c.inputs() == nil {
			return
		}
		claimedBy := map[string]string{} // identifier -> the flag name that first claimed it
		for i, f := range c.Flags {
			for _, id := range flagIdentifiers(f) {
				if prev, dup := claimedBy[id]; dup {
					problems = append(problems, inputProblem(fmt.Sprintf("%s/flags/%d", ptr, i), path, "flag", f.Name,
						fmt.Sprintf("identifier %q is already declared by flag %q", id, prev)))
					continue
				}
				claimedBy[id] = f.Name
			}
		}
	})
	return problems
}

// lintSchemaRefs rejects a "$ref": "#/schemas/X" naming an undeclared schema, which would
// otherwise fail to compile in generated code, and suggests the closest declared name.
func lintSchemaRefs(spec *Spec) []error {
	declared := map[string]bool{}
	names := slices.Sorted(maps.Keys(spec.Command.Schemas)) // sorted for a deterministic suggestion
	for _, name := range names {
		declared[name] = true
	}
	var problems []error
	reported := map[string]bool{}
	checkAt := func(loc, subject, at string) func(BaseSchema) {
		return func(b BaseSchema) {
			name := refTypeName(b.Ref)
			if name == "" || declared[name] {
				return
			}
			key := name + "\x00" + loc + "\x00" + subject
			if reported[key] {
				return
			}
			reported[key] = true
			msg := fmt.Sprintf("%s: `$ref` %q points to an undeclared schema (no %q under the root command's `schemas`)", subject, b.Ref, name)
			problems = append(problems, &problem{kind: "spec", ptr: at, loc: loc, msg: didYouMean(msg, name, names)})
		}
	}
	walkCommandsAt(spec, func(c *Command, path, ptr string) {
		eachInputAt(c, ptr, func(channel, name, ptr string, s *InputSchema) {
			if s == nil {
				return
			}
			subject := channel
			if name != "" {
				subject = fmt.Sprintf("%s %q", channel, name)
			}
			walkSchemaTree(s.BaseSchema, checkAt("command "+path, subject, ptr))
		})
		if c.Output != nil {
			walkSchemaTree(c.Output.BaseSchema, checkAt("command "+path, "output", ptr+"/output"))
		}
	})
	for _, name := range names {
		walkSchemaTree(spec.Command.Schemas[name].BaseSchema, checkAt(rootLabel(spec), fmt.Sprintf("schema %q", name), rootPointer+"/schemas/"+name))
	}
	return problems
}

// lintHandlerFilenames rejects two commands resolving to the same stub file and a `filename`
// override that is not a bare "*.go" name or that the go tool reads specially (_test.go,
// GOOS/GOARCH suffixes). Composed commands generate no stub and are skipped. The walk mirrors
// the generator's derivation.
func lintHandlerFilenames(spec *Spec) []error {
	rootName := spec.Command.Name
	var problems []error
	byFile := map[string]string{} // stub file name -> the command path that first produced it
	var walk func(c *Command, path, display, ptr string)
	walk = func(c *Command, path, display, ptr string) {
		if ov := c.Filename; ov != "" {
			switch {
			case strings.ContainsAny(ov, `/\`):
				problems = append(problems, &problem{kind: "spec", ptr: ptr + "/filename", loc: "command " + display, msg: fmt.Sprintf("`filename` %q must be a bare file name with no directory", ov)})
			case !strings.HasSuffix(ov, ".go"):
				problems = append(problems, &problem{kind: "spec", ptr: ptr + "/filename", loc: "command " + display, msg: fmt.Sprintf("`filename` %q must end in \".go\"", ov)})
			case reservedTrailingToken(strings.TrimSuffix(ov, ".go")):
				problems = append(problems, &problem{kind: "spec", ptr: ptr + "/filename", loc: "command " + display, msg: fmt.Sprintf("`filename` %q would be read specially by the go tool (a _test.go test file, or a GOOS/GOARCH build constraint); choose another name", ov)})
			}
		}
		fn := commandStubFilename(rootName, path, c.Filename)
		if prev, dup := byFile[fn]; dup {
			if prev == display {
				// Two siblings share this name; lintSiblingCollisions already reports it.
				return
			}
			problems = append(problems, &problem{kind: "spec", ptr: ptr, loc: "command " + display, msg: fmt.Sprintf("generates handler file %q, already used by command %q", fn, prev)})
		} else {
			byFile[fn] = display
		}
		for i := range c.Commands {
			child := &c.Commands[i]
			if child.Ref != "" {
				continue // composed: its stub lives in the child's package
			}
			childPath := child.Name
			if path != "" {
				childPath = path + "_" + child.Name
			}
			walk(child, childPath, display+"/"+child.Name, fmt.Sprintf("%s/commands/%d", ptr, i))
		}
	}
	display := rootName
	if display == "" {
		display = "(root)"
	}
	walk(&spec.Command, "", display, rootPointer)
	return problems
}

// lintSchemaTypes rejects an input `type:` that cannot become a Go type. The schema leaves
// `type` free-form, so the rule resolves aliases and requires the result to parse as a Go type
// expression, rejects unknown lowercase names (typos), and requires an `import` for an
// author-written qualified type.
func lintSchemaTypes(spec *Spec) []error {
	var problems []error
	walkCommandsAt(spec, func(c *Command, path, ptr string) {
		eachInputAt(c, ptr, func(channel, name, ptr string, schema *InputSchema) {
			if schema == nil || schema.Type == "" {
				return
			}
			add := func(msg string) { problems = append(problems, inputProblem(ptr, path, channel, name, msg)) }
			resolved := jsonSchemaTypeToGo(schema.Type)
			expr, err := parser.ParseExpr(resolved)
			if err != nil || !isGoTypeExpr(expr) {
				add(fmt.Sprintf("`type` %q is not a Go type; use a builtin (string, int, bool, []string, map[string]int), a rotini alias (count, duration, date), a JSON Schema name (integer, number, array, object), or an imported type with `import`", schema.Type))
				return
			}
			// A lowercase unqualified name must be a Go builtin or rotini alias. A
			// capitalized one may be a type the author defines in the generated package.
			if name := unknownLowercaseTypeName(expr); name != "" {
				add(didYouMean(fmt.Sprintf("`type` %q is not a type rotini knows; %q is neither a Go builtin nor a rotini type", schema.Type, name),
					name, knownTypeNames()))
				return
			}
			// rotini supplies imports only for types its aliases resolve to.
			if resolved == schema.Type && isQualifiedType(expr) && strings.TrimSpace(schema.Import) == "" {
				add(fmt.Sprintf("`type` %q is qualified but declares no `import`; the generated code would reference a package it does not import", schema.Type))
			}
		})
	})
	return problems
}

// goPredeclaredTypes are the type names Go makes available in every package.
var goPredeclaredTypes = []string{
	"bool", "string", "int", "int8", "int16", "int32", "int64",
	"uint", "uint8", "uint16", "uint32", "uint64", "uintptr",
	"float32", "float64", "complex64", "complex128", "byte", "rune", "any",
}

// knownTypeNames is every name a spec may write unqualified: Go's builtins and rotini's aliases.
func knownTypeNames() []string {
	return append(slices.Clone(goPredeclaredTypes), rotiniTypeAliases...)
}

// unknownLowercaseTypeName returns the first unqualified lowercase identifier in a type
// expression that is not a Go builtin, or "".
func unknownLowercaseTypeName(expr ast.Expr) string {
	switch e := expr.(type) {
	case *ast.Ident:
		if e.Name != "" && unicode.IsLower(rune(e.Name[0])) && !slices.Contains(goPredeclaredTypes, e.Name) {
			return e.Name
		}
	case *ast.StarExpr:
		return unknownLowercaseTypeName(e.X)
	case *ast.ArrayType:
		return unknownLowercaseTypeName(e.Elt)
	case *ast.MapType:
		if n := unknownLowercaseTypeName(e.Key); n != "" {
			return n
		}
		return unknownLowercaseTypeName(e.Value)
	}
	return ""
}

// isGoTypeExpr reports whether a parsed expression denotes a Go type rather than a value
// expression such as "not-a-type", which parses as two subtractions.
func isGoTypeExpr(expr ast.Expr) bool {
	switch e := expr.(type) {
	case *ast.Ident:
		return true
	case *ast.SelectorExpr: // pkg.Type
		_, ok := e.X.(*ast.Ident)
		return ok
	case *ast.StarExpr:
		return isGoTypeExpr(e.X)
	case *ast.ArrayType:
		return e.Len == nil && isGoTypeExpr(e.Elt) // slices only; a fixed-size array is not an input shape
	case *ast.MapType:
		return isGoTypeExpr(e.Key) && isGoTypeExpr(e.Value)
	case *ast.InterfaceType:
		return len(e.Methods.List) == 0 // interface{} — the `any` spelling
	default:
		return false
	}
}

// isQualifiedType reports whether a type expression names a package-qualified type
// anywhere within it (T, []T, map[string]T, *T).
func isQualifiedType(expr ast.Expr) bool {
	switch e := expr.(type) {
	case *ast.SelectorExpr:
		return true
	case *ast.StarExpr:
		return isQualifiedType(e.X)
	case *ast.ArrayType:
		return isQualifiedType(e.Elt)
	case *ast.MapType:
		return isQualifiedType(e.Key) || isQualifiedType(e.Value)
	default:
		return false
	}
}

// keyList renders spec keys for a message, each in backticks ("minimum/maximum" becomes
// "`minimum`/`maximum`"), joined with commas.
func keyList(keys []string) string {
	out := make([]string, len(keys))
	for i, k := range keys {
		out[i] = "`" + strings.ReplaceAll(k, "/", "`/`") + "`"
	}
	return strings.Join(out, ", ")
}

// quotedList renders tokens as a comma-separated quoted list.
func quotedList(tokens []string) string {
	quoted := make([]string, len(tokens))
	for i, t := range tokens {
		quoted[i] = strconv.Quote(t)
	}
	return strings.Join(quoted, ", ")
}

// repeatableSchema reports whether an input has a list or map type and so can hold several
// values.
func repeatableSchema(schema *InputSchema) bool {
	t := schema.Type
	return strings.HasPrefix(t, "[]") || t == "array" ||
		t == "map" || t == "object" || strings.HasPrefix(t, "map[")
}

// nonScalarElement names the kind of the first non-scalar element of a multi-value default,
// or "" when every element is a scalar.
func nonScalarElement(v any) string {
	var items []any
	switch x := v.(type) {
	case []any:
		items = x
	case map[string]any:
		for _, k := range slices.Sorted(maps.Keys(x)) { // sorted for a deterministic message
			items = append(items, x[k])
		}
	}
	for _, it := range items {
		switch it.(type) {
		case string, bool, float64, int, int64, nil:
			continue
		}
		return defaultKindName(it)
	}
	return ""
}

// lintDefaultConstraints rejects a `default` that the input's own enum, bounds, length,
// pattern or item-count constraints forbid, since every run relying on it would fail.
//
// It checks the strings codegen emits (defaultString/defaultList) with checks mirroring
// parser.go's checkNumericBounds, checkStringBounds and checkItemCount;
// TestDefaultConstraintsAgreeWithRuntime pins the agreement. Per-value constraints apply to
// each element of a multi-value default; item counts apply to the collection.
func lintDefaultConstraints(spec *Spec) []error {
	var problems []error
	walkCommandsAt(spec, func(c *Command, path, ptr string) {
		eachInputAt(c, ptr, func(channel, name, ptr string, schema *InputSchema) {
			if schema == nil || objectRef(schema, spec.Command.Schemas) != "" || schema.Default == nil {
				return // an object input: lintObjectFlags
			}
			add := func(msg string) { problems = append(problems, inputProblem(ptr, path, channel, name, msg)) }

			values := defaultList(schema.Default)
			multi := len(values) > 0
			if !multi {
				if d := defaultString(schema.Default); d != "" {
					values = []string{d}
				}
			}
			if len(values) == 0 {
				return
			}

			if multi {
				if n := len(values); schema.MinItems > 0 && n < schema.MinItems {
					add(fmt.Sprintf("`default` has %d %s but `minItems` is %d; %s",
						n, pluralWord("value", n), schema.MinItems, defaultFails))
				} else if schema.MaxItems > 0 && n > schema.MaxItems {
					add(fmt.Sprintf("`default` has %d %s but `maxItems` is %d; %s",
						n, pluralWord("value", n), schema.MaxItems, defaultFails))
				}
			}

			label := "`default`"
			if multi {
				label = "`default` element"
			}
			for _, v := range values {
				if msg := defaultViolation(schema, label, v); msg != "" {
					add(msg + "; " + defaultFails)
				}
			}
		})
	})
	return problems
}

// defaultFails is the consequence appended to every unusable-default message.
const defaultFails = "every run that leaves it unset would fail"

// defaultViolation reports why v violates schema's constraints, or "". label names the
// subject in the message. The order and comparisons mirror parser.go: enum membership, numeric
// bounds on a value that parses as a number, then length and pattern.
func defaultViolation(schema *InputSchema, label, v string) string {
	if len(schema.Enum) > 0 && !enumMember(schema, v) {
		return fmt.Sprintf("%s %q is not one of the declared `enum` values (%s)",
			label, v, strings.Join(schema.Enum, ", "))
	}

	if n, err := strconv.ParseFloat(v, 64); err == nil {
		switch {
		case bound(schema.Minimum) != nil && n < *bound(schema.Minimum):
			return fmt.Sprintf("%s %s is below the declared `minimum` %s", label, v, formatBound(*bound(schema.Minimum)))
		case bound(schema.Maximum) != nil && n > *bound(schema.Maximum):
			return fmt.Sprintf("%s %s is above the declared `maximum` %s", label, v, formatBound(*bound(schema.Maximum)))
		case bound(schema.ExclusiveMinimum) != nil && n <= *bound(schema.ExclusiveMinimum):
			return fmt.Sprintf("%s %s is not above the declared `exclusiveMinimum` %s", label, v, formatBound(*bound(schema.ExclusiveMinimum)))
		case bound(schema.ExclusiveMaximum) != nil && n >= *bound(schema.ExclusiveMaximum):
			return fmt.Sprintf("%s %s is not below the declared `exclusiveMaximum` %s", label, v, formatBound(*bound(schema.ExclusiveMaximum)))
		case bound(schema.MultipleOf) != nil && !isDefaultMultipleOf(n, *bound(schema.MultipleOf)):
			return fmt.Sprintf("%s %s is not a multiple of the declared `multipleOf` %s", label, v, formatBound(*bound(schema.MultipleOf)))
		}
	}

	if ln := utf8.RuneCountInString(v); schema.MinLength > 0 && ln < schema.MinLength {
		return fmt.Sprintf("%s %q is %d %s, below the declared `minLength` %d",
			label, v, ln, pluralWord("character", ln), schema.MinLength)
	} else if schema.MaxLength > 0 && ln > schema.MaxLength {
		return fmt.Sprintf("%s %q is %d %s, above the declared `maxLength` %d",
			label, v, ln, pluralWord("character", ln), schema.MaxLength)
	}

	if schema.Pattern != "" {
		// An uncompilable pattern is lintPatternCompiles's problem, not this rule's.
		if ok, err := regexp.MatchString(schema.Pattern, v); err == nil && !ok {
			return fmt.Sprintf("%s %q does not match the declared `pattern` %s", label, v, schema.Pattern)
		}
	}
	return ""
}

// pluralWord returns word, pluralized with "s" unless n is 1.
func pluralWord(word string, n int) string {
	if n == 1 {
		return word
	}
	return word + "s"
}

// formatBound renders a bound as the runtime's formatNum does, with no trailing zeros.
func formatBound(f float64) string { return strconv.FormatFloat(f, 'g', -1, 64) }

// isDefaultMultipleOf mirrors parser.go's isMultipleOf, including its float tolerance (1.2
// is a multiple of 0.1).
func isDefaultMultipleOf(n, m float64) bool {
	if m <= 0 {
		return false
	}
	q := n / m
	return math.Abs(q-math.Round(q)) < 1e-9*math.Max(1, math.Abs(q))
}

// lintItemConstraints reports `items` constraints on an array input that would not take
// effect: a per-value constraint that conflicts with the list's own (hoistItemConstraints keeps
// the list-level value), and minItems/maxItems on items, since each element is a single value.
// Stdin is exempt because its schema has full JSON Schema semantics.
func lintItemConstraints(spec *Spec) []error {
	var problems []error
	walkCommandsAt(spec, func(c *Command, path, ptr string) {
		eachInputAt(c, ptr, func(channel, name, ptr string, schema *InputSchema) {
			if channel == "stdin" || schema == nil || schema.Items == nil || !isArrayInputSchema(schema) {
				return
			}
			add := func(msg string) { problems = append(problems, inputProblem(ptr, path, channel, name, msg)) }
			it := &schema.Items.BaseSchema

			if it.MinItems > 0 || it.MaxItems > 0 {
				add("`minItems`/`maxItems` on `items` would count values inside ONE element, but an element of a " +
					channel + " list is a single value; put the bound on the list itself")
			}

			conflict := func(key string, differ bool) {
				if differ {
					add(fmt.Sprintf("%#q is declared on both the list and its `items`, with different values; "+
						"they mean the same thing (a rule every element must pass), so declare it once", key))
				}
			}
			conflict("enum", len(it.Enum) > 0 && !slices.Equal(it.Enum, schema.Enum))
			conflict("pattern", it.Pattern != "" && it.Pattern != schema.Pattern)
			conflict("minimum", floatBoundsDiffer(bound(it.Minimum), bound(schema.Minimum)))
			conflict("maximum", floatBoundsDiffer(bound(it.Maximum), bound(schema.Maximum)))
			conflict("exclusiveMinimum", floatBoundsDiffer(bound(it.ExclusiveMinimum), bound(schema.ExclusiveMinimum)))
			conflict("exclusiveMaximum", floatBoundsDiffer(bound(it.ExclusiveMaximum), bound(schema.ExclusiveMaximum)))
			conflict("multipleOf", floatBoundsDiffer(bound(it.MultipleOf), bound(schema.MultipleOf)))
			conflict("minLength", it.MinLength != 0 && it.MinLength != schema.MinLength)
			conflict("maxLength", it.MaxLength != 0 && it.MaxLength != schema.MaxLength)
		})
	})
	return problems
}

// floatBoundsDiffer reports whether an items-level bound is set and disagrees with the list's.
func floatBoundsDiffer(item, list *float64) bool {
	return item != nil && (list == nil || *item != *list)
}
