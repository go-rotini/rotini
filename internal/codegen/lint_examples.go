package codegen

import (
	"errors"
	"fmt"
	"path"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/go-rotini/rotini"
)

// lintExamplesParse checks that each `examples:` line that runs the program parses against the
// spec: its command path, flags, enum values and argument count, through the runtime's own
// parser, so the lint and a run can't disagree. Values are not executed: nothing is read from
// files or stdin, and an input that has an environment or config fallback may be left out.
//
// Every pipeline segment (split at |, ;, && and the like) whose first word is the program's
// name, or its display name's words, is checked; prose and other commands are skipped. A word
// with a shell expansion or a glob, or a <placeholder>, is opaque: a complaint about it is
// dropped, and an opaque word where a flag is expected skips the segment. A command composed
// from another spec, or a plugin, accepts whatever follows it.
func lintExamplesParse(spec *Spec) []error {
	var problems []error
	check := newExampleChecker(spec)
	walkCommandsAt(spec, func(c *Command, cmdPath, ptr string) {
		for i, line := range c.Examples {
			at := fmt.Sprintf("%s/examples/%d", ptr, i)
			msg, unclosed := check.complaint(line)
			switch {
			case unclosed:
				problems = append(problems, &problem{kind: "spec", ptr: at, loc: "command " + cmdPath, sev: severityWarning,
					msg: fmt.Sprintf("example %q has an unclosed quote", line)})
			case msg != "":
				problems = append(problems, &problem{kind: "spec", ptr: at, loc: "command " + cmdPath,
					msg: fmt.Sprintf("example %q does not parse: %s", line, msg)})
			}
		}
	})
	return problems
}

// exampleChecker parses examples against one spec. Its Definition is built on first use.
type exampleChecker struct {
	spec    *Spec
	program [][]string
	def     *rotini.Definition
}

// newExampleChecker returns a checker for spec's examples.
func newExampleChecker(spec *Spec) *exampleChecker {
	return &exampleChecker{spec: spec, program: exampleProgramWords(spec)}
}

// complaint returns the runtime's complaint about the first command in line that runs the
// program and doesn't parse, or "" when every such command parses (or none runs the program).
// unclosed reports a quote left open, which leaves the line unchecked.
func (ec *exampleChecker) complaint(line string) (msg string, unclosed bool) {
	segments, unclosed := exampleSegments(line)
	if unclosed {
		return "", true
	}
	for _, words := range segments {
		argv, ok := exampleArgv(words, ec.program)
		if !ok || reachesUnnamedRef(&ec.spec.Command, argv) {
			continue
		}
		if ec.def == nil {
			d := specDefinition(ec.spec)
			ec.def = &d
		}
		if msg := exampleComplaint(*ec.def, argv); msg != "" {
			return msg, false
		}
	}
	return "", false
}

// exampleProgramWords are the spellings that start an example of the program: its name, and
// its display name's words when it has one (`kubectl unready`).
func exampleProgramWords(spec *Spec) [][]string {
	out := [][]string{{spec.Command.Name}}
	if d := strings.Fields(spec.Command.DisplayName); len(d) > 0 {
		out = append(out, d)
	}
	return out
}

// exampleArgv returns the words after the program's name, when the segment runs the program.
func exampleArgv(words []exampleWord, program [][]string) ([]exampleWord, bool) {
	for _, prog := range program {
		if len(words) < len(prog) {
			continue
		}
		match := true
		for i, w := range prog {
			text := words[i].text
			if i == 0 {
				text = path.Base(text)
			}
			if words[i].opaque || text != w {
				match = false
				break
			}
		}
		if match {
			return words[len(prog):], true
		}
	}
	return nil, false
}

// exampleComplaint parses argv against def and returns the runtime's complaint, or "" when it
// parses (or the complaint is about an opaque word).
func exampleComplaint(def rotini.Definition, words []exampleWord) string {
	argv := make([]string, len(words))
	for i, w := range words {
		if w.opaque && strings.HasPrefix(w.text, "-") {
			return "" // an expansion where a flag may be expected: nothing to check
		}
		argv[i] = w.text
	}
	ctx := lintContext(def, argv).WithStdin(strings.NewReader(""))
	err := rotini.NewParser().Parse(ctx, &struct{}{})
	if err == nil {
		return ""
	}
	msg := err.Error()
	for _, w := range words {
		if w.opaque && strings.Contains(msg, strconv.Quote(w.text)) {
			return ""
		}
	}
	var pe *rotini.ParseError
	if errors.As(err, &pe) && pe.Kind == rotini.ParseKindUnknownCommand && specDiscoversPlugins(def) {
		return "" // the word may name a discovered plugin
	}
	return msg
}

// specDiscoversPlugins reports whether any command in def discovers plugins, which accept any
// command word.
func specDiscoversPlugins(def rotini.Definition) bool {
	if def.PluginDiscovery != nil {
		return true
	}
	var walk func([]rotini.CommandDef) bool
	walk = func(cmds []rotini.CommandDef) bool {
		for _, c := range cmds {
			if c.PluginDiscovery != nil || walk(c.Commands) {
				return true
			}
		}
		return false
	}
	return walk(def.Commands)
}

// ─── the in-memory Definition ─────────────────────────────────────────────────.

// specDefinition builds the runtime Definition of a spec for parsing examples: names, aliases
// (hidden and deprecated ones too), flag spellings, types, enums, groups and passthrough. It
// leaves out what a parse at lint time mustn't do or needn't know: `from` (files and stdin),
// response files, defaults and constraints. An input with an environment or config fallback
// isn't required, since an example may rely on the fallback.
func specDefinition(spec *Spec) rotini.Definition {
	root := &spec.Command
	prefix := root.EnvPrefix
	schemas := root.Schemas
	def := rotini.Definition{
		Name: root.Name, Handler: "Root",
		Flags: exampleFlagDefs(root, prefix, schemas), Arguments: exampleArgDefs(root, prefix, schemas),
		FlagGroups: exampleFlagGroups(root), FlagDependencies: exampleFlagDependencies(root),
		Commands: exampleCommandDefs(root.Commands, prefix, schemas), Passthrough: root.Passthrough,
		OptionsFirst: root.OptionsFirst,
	}
	if root.PluginDiscovery != nil {
		def.PluginDiscovery = &rotini.PluginDiscoveryDef{}
	}
	for _, pl := range root.Plugins {
		def.Commands = append(def.Commands, acceptAnything(pl.Name, pl.Aliases, nil))
	}
	return def
}

// exampleCommandDefs builds the CommandDefs of a command's children. A composed child accepts
// anything, since its spec is linted on its own.
func exampleCommandDefs(cmds []Command, prefix string, schemas map[string]Schema) []rotini.CommandDef {
	var out []rotini.CommandDef
	for i := range cmds {
		c := &cmds[i]
		if c.Ref != "" {
			if c.Name != "" {
				out = append(out, acceptAnything(c.Name, c.Aliases, c.HiddenAliases))
			}
			continue
		}
		cd := rotini.CommandDef{
			Name: c.Name, Aliases: c.Aliases, HiddenAliases: c.HiddenAliases, DeprecatedIdentifiers: c.DeprecatedIdentifiers,
			Handler: "Command", Hidden: c.Hidden, Deprecated: c.Deprecated,
			Flags: exampleFlagDefs(c, prefix, schemas), Arguments: exampleArgDefs(c, prefix, schemas),
			FlagGroups: exampleFlagGroups(c), FlagDependencies: exampleFlagDependencies(c),
			Commands: exampleCommandDefs(c.Commands, prefix, schemas), Passthrough: c.Passthrough, OptionsFirst: c.OptionsFirst,
		}
		if c.PluginDiscovery != nil {
			cd.PluginDiscovery = &rotini.PluginDiscoveryDef{}
		}
		for _, pl := range c.Plugins {
			cd.Commands = append(cd.Commands, acceptAnything(pl.Name, pl.Aliases, nil))
		}
		out = append(out, cd)
	}
	return out
}

// exampleFlagDefs builds a command's FlagDefs.
func exampleFlagDefs(c *Command, prefix string, schemas map[string]Schema) []rotini.FlagDef {
	out := make([]rotini.FlagDef, 0, len(c.Flags))
	for _, f := range c.Flags {
		fd := rotini.FlagDef{
			Name: f.Name, Identifiers: flagIdentifiers(f), HiddenIdentifiers: f.HiddenIdentifiers,
			DeprecatedIdentifiers: f.DeprecatedIdentifiers, Type: exampleType(definitionType(f.Schema, schemas)),
			Hidden: f.Hidden, Deprecated: f.Deprecated, ShortCircuit: f.ShortCircuit,
		}
		if s := f.Schema; s != nil {
			fd.Required = s.Required && flagEnvVar(s, flagReconKey(f.Name, s), prefix) == ""
			exampleEnum(s, &fd.Enum, &fd.EnumValues, &fd.IgnoreCase)
			if s.Separator != "" {
				fd.Separator = runtimeSeparator(s.Separator)
			}
			if s.ImplicitValue != nil {
				fd.ImplicitValue = defaultString(s.ImplicitValue)
			}
			fd.Negatable, fd.Negation = negation(s)
			fd.NoRepeat = s.Repeatable != nil && !*s.Repeatable
			fd.DottedKeys = s.DottedKeys
			fd.KeyPaths = keyPaths(s)
			fd.ObjectSchema = objectSchemaFor(s, schemas)
		}
		out = append(out, fd)
	}
	return out
}

// exampleType is typ with the path kinds, which a run checks against the file system, read as
// strings: an example's paths name files on the user's machine, not the spec author's.
func exampleType(typ string) string {
	base := strings.TrimPrefix(typ, "[]")
	switch base {
	case "existingfile", "existingdir", "inputfile", "outputfile":
		return strings.TrimSuffix(typ, base) + "string"
	}
	return typ
}

// exampleArgDefs builds a command's ArgDefs.
func exampleArgDefs(c *Command, prefix string, schemas map[string]Schema) []rotini.ArgDef {
	out := make([]rotini.ArgDef, 0, len(c.Arguments))
	stdinFallback := c.Stdin != nil && c.Stdin.UnlessArgument != ""
	for _, a := range c.Arguments {
		ad := rotini.ArgDef{Name: a.Name, Type: exampleType(definitionType(a.Schema, schemas)), Hidden: a.Hidden, Passthrough: a.Passthrough}
		ad.Variadic = strings.HasPrefix(ad.Type, "[]")
		if s := a.Schema; s != nil {
			env, _ := argumentFallback(a, prefix, true)
			ad.Required = s.Required && len(env) == 0 && flagReconKey(a.Name, s) == "" && len(s.From) == 0 &&
				(!stdinFallback || c.Stdin.UnlessArgument != a.Name)
			exampleEnum(s, &ad.Enum, &ad.EnumValues, &ad.IgnoreCase)
			if s.Separator != "" {
				ad.Separator = runtimeSeparator(s.Separator)
			}
		}
		out = append(out, ad)
	}
	return out
}

// exampleEnum sets an input's enum, its values' aliases, and ignore_case.
func exampleEnum(s *InputSchema, enum *[]string, values *[]rotini.EnumValue, ignoreCase *bool) {
	if len(s.Enum) == 0 {
		return
	}
	*enum = enumStrings(s.Enum)
	*ignoreCase = s.IgnoreCase
	if !enumDetailed(s.Enum) {
		return
	}
	for _, v := range enumValues(s.Enum) {
		*values = append(*values, rotini.EnumValue{Value: v.Value, Aliases: v.Aliases, DeprecatedAliases: v.DeprecatedAliases, Hidden: v.Hidden, Deprecated: v.Deprecated})
	}
}

// exampleFlagGroups builds a command's flag groups.
func exampleFlagGroups(c *Command) []rotini.FlagGroup {
	out := make([]rotini.FlagGroup, 0, len(c.FlagGroups))
	for _, g := range c.FlagGroups {
		out = append(out, rotini.FlagGroup{Kind: rotini.FlagGroupKind(g.Kind), Flags: g.Flags})
	}
	return out
}

// exampleFlagDependencies builds a command's flag dependencies.
func exampleFlagDependencies(c *Command) []rotini.FlagDependency {
	out := make([]rotini.FlagDependency, 0, len(c.FlagDependencies))
	for _, d := range c.FlagDependencies {
		out = append(out, rotini.FlagDependency{When: d.When, Requires: d.Requires, Equals: dependencyEquals(d), Unless: d.Unless, Forbids: d.Forbids})
	}
	return out
}

// ─── splitting an example into words ──────────────────────────────────────────.

// exampleWord is one shell word of an example. An opaque word holds an expansion, a glob or a
// <placeholder>, so its value is unknown.
type exampleWord struct {
	text   string
	opaque bool
}

// examplePrompt is a leading shell prompt to strip.
var examplePrompt = regexp.MustCompile(`^\s*[$%]\s+`)

// exampleAssignment is a leading VAR=value word, which sets the environment for the command.
var exampleAssignment = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*=`)

// examplePlaceholder is a <placeholder> word, which isn't a redirect.
var examplePlaceholder = regexp.MustCompile(`^<[^<>\s]+>`)

// exampleSegments splits an example into the words of each command in it, POSIX style: single
// quotes are literal, double quotes take \" \\ \$ and \`, a backslash outside quotes escapes the
// next character, and a backslash-newline joins lines. Segments end at |, ||, &&, &, ;, |&; a
// redirect (>, >>, <, <<, 2>, 2>&1, &>) ends the words of its segment. Leading VAR=value words
// are dropped. unclosed reports a quote left open.
func exampleSegments(line string) (segments [][]exampleWord, unclosed bool) {
	l := &exampleLexer{src: []rune(examplePrompt.ReplaceAllString(line, ""))}
	for ; l.i < len(l.src); l.i++ {
		if !l.step() {
			return l.segments, true
		}
	}
	l.endSegment()
	return l.segments, false
}

// exampleLexer is the state of splitting one example.
type exampleLexer struct {
	src      []rune
	i        int
	segments [][]exampleWord
	words    []exampleWord
	cur      strings.Builder

	inWord, opaque, redirected bool
}

// step reads the rune at l.i, and any it consumes after it. It reports false on an unclosed
// quote.
func (l *exampleLexer) step() bool {
	r := l.src[l.i]
	switch {
	case r == '\\':
		l.escape()
	case r == '\'':
		end := indexRune(l.src, l.i+1, '\'')
		if end < 0 {
			return false
		}
		l.cur.WriteString(string(l.src[l.i+1 : end]))
		l.inWord, l.i = true, end
	case r == '"':
		return l.doubleQuoted()
	case r == ' ' || r == '\t' || r == '\n':
		l.endWord()
	case r == '|' || r == ';' || (r == '&' && (l.i+1 >= len(l.src) || l.src[l.i+1] != '>')):
		l.endSegment()
		if l.i+1 < len(l.src) && (l.src[l.i+1] == '|' || l.src[l.i+1] == '&') {
			l.i++
		}
	case r == '<' && examplePlaceholder.MatchString(string(l.src[l.i:])):
		m := examplePlaceholder.FindString(string(l.src[l.i:]))
		l.cur.WriteString(m)
		l.inWord, l.opaque = true, true
		l.i += len([]rune(m)) - 1
	case r == '>' || r == '<' || r == '&':
		l.redirect()
	default:
		if strings.ContainsRune("$`*?[", r) {
			l.opaque = true
		}
		l.cur.WriteRune(r)
		l.inWord = true
	}
	return true
}

// escape takes the character after a backslash literally; a backslash-newline joins lines.
func (l *exampleLexer) escape() {
	if l.i+1 >= len(l.src) {
		return
	}
	l.i++
	if l.src[l.i] != '\n' {
		l.cur.WriteRune(l.src[l.i])
		l.inWord = true
	}
}

// doubleQuoted reads a double-quoted string. It reports false when the quote is left open.
func (l *exampleLexer) doubleQuoted() bool {
	j := l.i + 1
	for ; j < len(l.src) && l.src[j] != '"'; j++ {
		switch c := l.src[j]; {
		case c == '\\' && j+1 < len(l.src) && strings.ContainsRune("\"\\$`\n", l.src[j+1]):
			j++
			if l.src[j] != '\n' {
				l.cur.WriteRune(l.src[j])
			}
		case c == '$' || c == '`':
			l.opaque = true
			l.cur.WriteRune(c)
		default:
			l.cur.WriteRune(c)
		}
	}
	if j >= len(l.src) {
		return false
	}
	l.inWord, l.i = true, j
	return true
}

// redirect ends the segment's words at a redirect, dropping a file descriptor number before it.
func (l *exampleLexer) redirect() {
	if l.inWord && (l.cur.String() == "1" || l.cur.String() == "2") {
		l.cur.Reset()
		l.inWord = false
	}
	l.endWord()
	l.redirected = true
	for l.i+1 < len(l.src) && strings.ContainsRune("<>&12", l.src[l.i+1]) {
		l.i++
	}
}

// endWord finishes the current word, unless a redirect has ended the segment's words.
func (l *exampleLexer) endWord() {
	if l.inWord && !l.redirected {
		l.words = append(l.words, exampleWord{text: l.cur.String(), opaque: l.opaque})
	}
	l.cur.Reset()
	l.inWord, l.opaque = false, false
}

// endSegment finishes the current command, dropping its leading VAR=value words.
func (l *exampleLexer) endSegment() {
	l.endWord()
	words := l.words
	for len(words) > 0 && exampleAssignment.MatchString(words[0].text) && !words[0].opaque {
		words = words[1:]
	}
	if len(words) > 0 {
		l.segments = append(l.segments, words)
	}
	l.words, l.redirected = nil, false
}

// indexRune returns the index of r in src at or after from, or -1.
func indexRune(src []rune, from int, r rune) int {
	for i := from; i < len(src); i++ {
		if src[i] == r {
			return i
		}
	}
	return -1
}

// acceptAnything is a command whose words the lint can't check (a plugin, or a command composed
// from another spec): it takes every word after it as an argument.
func acceptAnything(name string, aliases, hidden []string) rotini.CommandDef {
	return rotini.CommandDef{
		Name: name, Aliases: aliases, HiddenAliases: hidden, Handler: "Opaque", Passthrough: true,
		Arguments: []rotini.ArgDef{{Name: "args", Type: "[]string", Variadic: true}},
	}
}

// reachesUnnamedRef reports whether argv's command words lead to a command whose children
// include a `$ref` without a name: its name comes from another spec, so any word there may
// name it. The walk follows command names past flags, taking the next word as a flag's value
// when the flag takes one and has none attached.
func reachesUnnamedRef(c *Command, argv []exampleWord) bool {
	for i := 0; i < len(argv); i++ {
		w := argv[i].text
		if w == "--" {
			return false
		}
		if strings.HasPrefix(w, "-") {
			if f := exampleFlagNamed(c, w); f != nil && !strings.Contains(w, "=") {
				if t := definitionType(f.Schema, nil); t != "bool" && t != "count" && (f.Schema == nil || f.Schema.ImplicitValue == nil) {
					i++
				}
			}
			continue
		}
		var next *Command
		for j := range c.Commands {
			if slices.Contains(dispatchTokens(&c.Commands[j]), w) {
				next = &c.Commands[j]
			}
		}
		if next == nil {
			return slices.ContainsFunc(c.Commands, func(ch Command) bool { return ch.Ref != "" && ch.Name == "" })
		}
		c = next
	}
	return false
}

// exampleFlagNamed returns c's flag that an argv word spells, or nil.
func exampleFlagNamed(c *Command, word string) *FlagInput {
	name, _, _ := strings.Cut(word, "=")
	for i := range c.Flags {
		if slices.Contains(matchIdentifiers(c.Flags[i]), name) {
			return &c.Flags[i]
		}
	}
	return nil
}
