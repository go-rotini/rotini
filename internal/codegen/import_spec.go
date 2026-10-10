package codegen

import (
	"fmt"
	"maps"
	"path/filepath"
	"slices"
	"strings"
)

// importSpecBuilder turns an importer's description into the spec YAML, in the shape `rotini
// init` seeds: the framework's own help and version flags and help command become the seed's,
// and its completion command keeps its shell argument and switches the completion feature on.
type importSpecBuilder struct {
	res        *importResult
	name       string // the CLI's name: --name, else the program's root name
	moduleRoot string
	comments   bool // write hook pointers and lossy/unsupported notes as YAML comments

	completion bool              // a completion command was emitted
	dropped    []importNote      // examples left out, reported as lossy notes
	unparsed   map[string]string // examples that don't parse, with the parser's complaint
}

// build returns the spec document's YAML.
func (sb *importSpecBuilder) build(version string) []byte {
	root := sb.res.Command
	doc := newYAMLMap()
	doc.set("$schema", "./.rotini-schema.spec.json")
	doc.set("version", version)
	cmd := sb.command(root, root.Name, true)
	doc.set("command", cmd)
	var d yamlDoc
	d.writeMap(doc, 0)
	return d.b.Bytes()
}

// command converts one command and its sub-commands; path is its path in the program.
func (sb *importSpecBuilder) command(c *importCommand, path string, isRoot bool) *yamlMap {
	m := newYAMLMap()
	examples := sb.examples(c, path) // first, so a dropped example is among its comments
	if sb.comments {
		m.comments = sb.commentsFor(path)
	}
	name := c.Name
	if isRoot {
		name = sb.name
	}
	m.set("name", name)
	m.set("display_name", c.DisplayName)
	m.set("aliases", c.Aliases)
	m.set("summary", c.Summary)
	m.set("description", c.Description)
	m.set("examples", examples)
	m.set("group", c.Group)
	if isRoot {
		m.set("footer", fmt.Sprintf("Use %q for more information about a command.", sb.name+" help <command>"))
	}
	m.set("hidden", c.Hidden)
	m.set("deprecated", c.Deprecated)
	m.set("options_first", c.OptionsFirst)
	m.set("passthrough", c.Passthrough)

	flags := make([]any, 0, len(c.Flags))
	for _, f := range c.Flags {
		flags = append(flags, importFlagMap(f))
	}
	m.set("flags", flags)
	args := make([]any, 0, len(c.Arguments))
	for _, a := range c.Arguments {
		am := newYAMLMap()
		am.set("name", a.Name)
		am.set("summary", a.Summary)
		am.set("schema", importSchemaMap(a.Schema))
		args = append(args, am)
	}
	m.set("arguments", args)
	groups := make([]any, 0, len(c.FlagGroups))
	for _, g := range c.FlagGroups {
		gm := newYAMLMap()
		gm.set("kind", g.Kind)
		gm.set("flags", g.Flags)
		groups = append(groups, gm)
	}
	m.set("flag_groups", groups)

	var subs []any
	for _, sub := range c.Commands {
		if strings.HasPrefix(sub.Name, "__complete") {
			continue
		}
		subPath := path + " " + sub.Name
		switch sub.Builtin {
		case "help":
			subs = append(subs, sb.helpCommand(subPath))
			continue
		case "completion":
			sb.completion = true
		}
		subs = append(subs, sb.command(sub, subPath, false))
	}
	m.set("commands", subs)
	return m
}

// helpCommand is the seed's help command.
func (sb *importSpecBuilder) helpCommand(path string) *yamlMap {
	m := newYAMLMap()
	if sb.comments {
		m.comments = sb.commentsFor(path)
	}
	m.set("name", "help")
	m.set("summary", "print help")
	m.set("description", "Print help for a command.")
	arg := newYAMLMap()
	arg.set("name", "command")
	arg.set("summary", "the command path to print help for")
	schema := newYAMLMap()
	schema.set("type", "[]string")
	complete := newYAMLMap()
	complete.set("kind", "command")
	schema.set("complete", complete)
	arg.set("schema", schema)
	m.set("arguments", []any{arg})
	return m
}

// importFlagMap converts a flag. The framework's help and version flags take the seed's
// summary; their identifiers, and the version flag's place on one command, are kept.
func importFlagMap(f *importFlag) *yamlMap {
	m := newYAMLMap()
	m.set("name", f.Name)
	m.set("identifiers", f.Identifiers)
	summary := f.Summary
	switch f.Builtin {
	case "help":
		summary = "print help"
	case "version":
		summary = "print version"
	}
	m.set("summary", summary)
	m.set("cascading", f.Cascading)
	m.set("short_circuit", f.ShortCircuit)
	m.set("hidden", f.Hidden)
	m.set("deprecated", f.Deprecated)
	m.set("deprecated_identifiers", f.DeprecatedIdentifiers)
	m.set("schema", importSchemaMap(f.Schema))
	return m
}

// importSchemaMap converts an input schema.
func importSchemaMap(s *importSchema) *yamlMap {
	if s == nil {
		return nil
	}
	m := newYAMLMap()
	m.set("type", s.Type)
	m.set("items", importSchemaMap(s.Items))
	m.set("required", s.Required)
	m.set("default", importValue(s.Default))
	if len(s.Enum) > 0 {
		enum := make([]any, len(s.Enum))
		for i, e := range s.Enum {
			if e.Summary == "" {
				enum[i] = e.Value
				continue
			}
			em := newYAMLMap()
			em.set("value", e.Value)
			em.set("summary", e.Summary)
			enum[i] = em
		}
		m.set("enum", enum)
	}
	if s.MinItems != nil {
		m.set("minItems", *s.MinItems)
	}
	if s.MaxItems != nil {
		m.set("maxItems", *s.MaxItems)
	}
	m.set("separator", s.Separator)
	m.set("implicit_value", s.ImplicitValue)
	m.set("placeholder", s.Placeholder)
	if s.Complete != nil {
		cm := newYAMLMap()
		cm.set("kind", s.Complete.Kind)
		cm.set("extensions", s.Complete.Extensions)
		m.set("complete", cm)
	}
	return m
}

// importValue converts a decoded default: lists and maps become YAML lists and mappings.
func importValue(v any) any {
	switch t := v.(type) {
	case []any:
		out := make([]any, len(t))
		for i, e := range t {
			out[i] = importValue(e)
		}
		if len(out) == 0 {
			return nil
		}
		return out
	case map[string]any:
		if len(t) == 0 {
			return nil
		}
		m := newYAMLMap()
		for _, k := range slices.Sorted(maps.Keys(t)) {
			m.pairs = append(m.pairs, yamlPair{key: yamlString(k, 0), value: importValue(t[k])})
		}
		return m
	}
	return v
}

// examples keeps the command's examples that run the program and parse against the spec,
// written with the CLI's name; the rest (comments, other programs' commands, stale examples)
// are dropped with a lossy note each.
func (sb *importSpecBuilder) examples(c *importCommand, path string) []string {
	program := sb.res.Command.Name
	words := [][]string{{program}}
	if d := strings.Fields(sb.res.Command.DisplayName); len(d) > 0 {
		words = append(words, d)
	}
	var kept []string
	for _, ex := range c.Examples {
		line := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(ex), "$ "))
		if !runsProgram(line, words) {
			sb.dropped = append(sb.dropped, importNote{Path: path, Level: "lossy",
				Msg: fmt.Sprintf("example %q dropped: it doesn't run %s", ex, program)})
			continue
		}
		if first, rest, _ := strings.Cut(line, " "); first == program {
			line = strings.TrimSpace(sb.name + " " + rest)
		}
		if msg, bad := sb.unparsed[line]; bad {
			sb.dropped = append(sb.dropped, importNote{Path: path, Level: "lossy",
				Msg: fmt.Sprintf("example %q dropped: it doesn't parse: %s", ex, msg)})
			continue
		}
		kept = append(kept, line)
	}
	return kept
}

// runsProgram reports whether a command in the example line starts with one of program's
// spellings.
func runsProgram(line string, program [][]string) bool {
	segments, unclosed := exampleSegments(line)
	if unclosed {
		return false
	}
	for _, words := range segments {
		if _, ok := exampleArgv(words, program); ok {
			return true
		}
	}
	return false
}

// findUnparsedExamples reads the spec a first build wrote and records each example that
// doesn't parse against it, with the parser's complaint, so the next build drops it. It
// reports whether it found any.
func (sb *importSpecBuilder) findUnparsedExamples(specYAML []byte) bool {
	spec, err := decodeData[Spec](formatYAML, specYAML, ".rotini.spec.yaml")
	if err != nil {
		return false // validation reports it
	}
	check := newExampleChecker(spec)
	sb.unparsed = map[string]string{}
	walkCommandsAt(spec, func(c *Command, _, _ string) {
		for _, line := range c.Examples {
			if msg, _ := check.complaint(line); msg != "" {
				sb.unparsed[line] = msg
			}
		}
	})
	return len(sb.unparsed) > 0
}

// commentsFor returns the YAML comment lines for the command at path: a pointer to each hook
// to port, then each lossy or unsupported note. Info notes stay on stderr only.
func (sb *importSpecBuilder) commentsFor(path string) []string {
	var lines []string
	for _, h := range sb.res.Hooks {
		if h.Path != path {
			continue
		}
		where := h.File
		if rel, err := filepath.Rel(sb.moduleRoot, h.File); err == nil && filepath.IsAbs(h.File) && !strings.HasPrefix(rel, "..") {
			where = filepath.ToSlash(rel)
		}
		if h.Line > 0 {
			where = fmt.Sprintf("%s:%d", where, h.Line)
		}
		lines = append(lines, fmt.Sprintf("%s %s -> rotini %s: %s", sb.res.Source.Framework, h.Framework, h.Rotini, where))
	}
	for _, n := range append(sb.res.Notes, sb.dropped...) {
		if n.Path == path && (n.Level == "lossy" || n.Level == "unsupported") {
			lines = append(lines, fmt.Sprintf("[%s] %s", n.Level, strings.Join(strings.Fields(n.Msg), " ")))
		}
	}
	return lines
}
